// Package observability provides a small vendor-neutral metric registry with
// hard cardinality bounds and Prometheus-compatible exposition.
//
// Label schemas are code-owned. Every dynamic label declares an explicit
// maximum number of values and every instrument declares a maximum number of
// series. Correlation IDs belong in logs and traces, never metric labels;
// recording methods still accept context so call sites can preserve one
// end-to-end operation context without extracting it into a label.
package observability

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

type Kind uint8

const (
	CounterKind Kind = iota + 1
	GaugeKind
	HistogramKind
)

const (
	defaultMaxMetrics = 256
	defaultMaxSeries  = 1024
	hardMaxMetrics    = 4096
	hardMaxSeries     = 65536
	maxMetricName     = 128
	maxHelpBytes      = 512
	maxLabelName      = 64
	maxLabelValue     = 256
	maxLabels         = 8
	maxBuckets        = 64
)

var (
	ErrInvalidOptions    = errors.New("observability: invalid options")
	ErrInvalidDescriptor = errors.New("observability: invalid descriptor")
	ErrDuplicateMetric   = errors.New("observability: duplicate metric")
	ErrInvalidLabels     = errors.New("observability: invalid labels")
	ErrCardinalityLimit  = errors.New("observability: cardinality limit reached")
	ErrInvalidValue      = errors.New("observability: invalid value")
	ErrWrongKind         = errors.New("observability: wrong metric kind")
)

var metricNamePattern = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)
var labelNamePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

var sensitiveLabelNames = map[string]struct{}{
	"authorization": {}, "cid": {}, "cookie": {}, "correlation_id": {},
	"email": {}, "id": {}, "ip": {}, "ip_address": {}, "request_id": {},
	"secret": {}, "span_id": {}, "token": {}, "trace_id": {},
}

// LabelSpec defines one code-owned label dimension. Values is a closed enum
// when non-empty. Otherwise MaxValues must be positive and caps the number of
// distinct token values accepted over the process lifetime.
type LabelSpec struct {
	Name      string
	Values    []string
	MaxValues int
}

// Descriptor defines one instrument and all of its cardinality bounds.
type Descriptor struct {
	Name      string
	Help      string
	Kind      Kind
	Labels    []LabelSpec
	Buckets   []float64
	MaxSeries int
}

// Options bounds one registry. DeniedLabels adds consumer-owned label names to
// the built-in generic identifier and sensitive-data policy. Entries are
// normalized case-insensitively and copied during construction.
type Options struct {
	MaxMetrics int
	// MaxSeries is a registry-wide budget. It is also the largest per-metric
	// MaxSeries a descriptor may request, so one instrument can never bypass
	// the process-local memory bound.
	MaxSeries    int
	DeniedLabels []string
}

// Labels are metric dimensions. They must exactly match the descriptor.
type Labels map[string]string

// Registry is a small handle over shared bounded state. Copies retain the same
// metric and total-series budgets and are safe for concurrent use.
type Registry struct {
	state *registryState
}

type registryState struct {
	mu          sync.RWMutex
	maxMetrics  int
	maxSeries   int
	totalSeries int
	denied      map[string]struct{}
	metrics     map[string]*Instrument
}

// Instrument is a small handle over one registered metric. Copies share the
// same synchronization and series state and are safe for concurrent use.
type Instrument struct {
	state *instrumentState
}

type instrumentState struct {
	registry   *registryState
	descriptor Descriptor

	mu          sync.RWMutex
	series      map[string]*series
	labelValues []map[string]struct{}
}

type series struct {
	labels []labelValue
	value  float64
	count  uint64
	sum    float64
	bucket []uint64
}

type labelValue struct {
	name  string
	value string
}

// NewRegistry constructs an empty bounded registry.
func NewRegistry(options Options) (*Registry, error) {
	if options.MaxMetrics < 0 || options.MaxMetrics > hardMaxMetrics ||
		options.MaxSeries < 0 || options.MaxSeries > hardMaxSeries {
		return nil, ErrInvalidOptions
	}
	if options.MaxMetrics == 0 {
		options.MaxMetrics = defaultMaxMetrics
	}
	if options.MaxSeries == 0 {
		options.MaxSeries = defaultMaxSeries
	}
	denied := make(map[string]struct{}, len(options.DeniedLabels))
	for _, name := range options.DeniedLabels {
		normalized := strings.ToLower(name)
		if !labelNamePattern.MatchString(name) {
			return nil, ErrInvalidOptions
		}
		denied[normalized] = struct{}{}
	}
	return &Registry{state: &registryState{
		maxMetrics: options.MaxMetrics,
		maxSeries:  options.MaxSeries,
		denied:     denied,
		metrics:    make(map[string]*Instrument),
	}}, nil
}

// Register validates and adds one metric. The returned instrument is safe for
// concurrent use.
func (registry *Registry) Register(descriptor Descriptor) (*Instrument, error) {
	if !registry.valid() {
		return nil, ErrInvalidOptions
	}
	state := registry.state
	normalized, err := normalizeDescriptor(descriptor, state.maxSeries, state.denied)
	if err != nil {
		return nil, err
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if _, exists := state.metrics[normalized.Name]; exists {
		return nil, fmt.Errorf("%w: %s", ErrDuplicateMetric, normalized.Name)
	}
	if len(state.metrics) >= state.maxMetrics {
		return nil, ErrCardinalityLimit
	}
	instrument := &Instrument{state: &instrumentState{
		registry:    state,
		descriptor:  normalized,
		series:      make(map[string]*series),
		labelValues: make([]map[string]struct{}, len(normalized.Labels)),
	}}
	for index := range instrument.state.labelValues {
		instrument.state.labelValues[index] = make(map[string]struct{})
	}
	state.metrics[normalized.Name] = instrument
	return instrument, nil
}

// Add increases a counter or adjusts a gauge. Counters reject negative deltas.
func (instrument *Instrument) Add(ctx context.Context, delta float64, labels Labels) error {
	if ctx == nil {
		return ErrInvalidValue
	}
	if !instrument.valid() ||
		(instrument.state.descriptor.Kind != CounterKind && instrument.state.descriptor.Kind != GaugeKind) {
		return ErrWrongKind
	}
	state := instrument.state
	if err := ctx.Err(); err != nil {
		return err
	}
	if !finite(delta) {
		return ErrInvalidValue
	}
	if state.descriptor.Kind == CounterKind && delta < 0 {
		return ErrInvalidValue
	}
	current, err := instrument.lookup(labels)
	if err != nil {
		return err
	}
	state.mu.Lock()
	next := current.value + delta
	if !finite(next) {
		state.mu.Unlock()
		return ErrInvalidValue
	}
	current.value = next
	state.mu.Unlock()
	return nil
}

// Inc increments a counter or gauge by one.
func (instrument *Instrument) Inc(ctx context.Context, labels Labels) error {
	return instrument.Add(ctx, 1, labels)
}

// Set replaces a gauge value.
func (instrument *Instrument) Set(ctx context.Context, value float64, labels Labels) error {
	if ctx == nil {
		return ErrInvalidValue
	}
	if !instrument.valid() || instrument.state.descriptor.Kind != GaugeKind {
		return ErrWrongKind
	}
	state := instrument.state
	if !finite(value) {
		return ErrInvalidValue
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := instrument.lookup(labels)
	if err != nil {
		return err
	}
	state.mu.Lock()
	current.value = value
	state.mu.Unlock()
	return nil
}

// Observe records a non-negative histogram sample.
func (instrument *Instrument) Observe(ctx context.Context, value float64, labels Labels) error {
	if ctx == nil {
		return ErrInvalidValue
	}
	if !instrument.valid() || instrument.state.descriptor.Kind != HistogramKind {
		return ErrWrongKind
	}
	state := instrument.state
	if err := ctx.Err(); err != nil {
		return err
	}
	if !finite(value) || value < 0 {
		return ErrInvalidValue
	}
	current, err := instrument.lookup(labels)
	if err != nil {
		return err
	}
	state.mu.Lock()
	nextSum := current.sum + value
	if !finite(nextSum) || current.count == ^uint64(0) {
		state.mu.Unlock()
		return ErrInvalidValue
	}
	for index, upper := range state.descriptor.Buckets {
		if value <= upper && current.bucket[index] == ^uint64(0) {
			state.mu.Unlock()
			return ErrInvalidValue
		}
	}
	current.count++
	current.sum = nextSum
	for index, upper := range state.descriptor.Buckets {
		if value <= upper {
			current.bucket[index]++
		}
	}
	state.mu.Unlock()
	return nil
}

func (instrument *Instrument) lookup(labels Labels) (*series, error) {
	if !instrument.valid() {
		return nil, ErrWrongKind
	}
	state := instrument.state
	ordered, key, err := instrument.validateLabels(labels)
	if err != nil {
		return nil, err
	}
	state.mu.RLock()
	current := state.series[key]
	state.mu.RUnlock()
	if current != nil {
		return current, nil
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if current = state.series[key]; current != nil {
		return current, nil
	}
	if len(state.series) >= state.descriptor.MaxSeries {
		return nil, ErrCardinalityLimit
	}
	for index, item := range ordered {
		spec := state.descriptor.Labels[index]
		observed := state.labelValues[index]
		if _, exists := observed[item.value]; exists {
			continue
		}
		if len(observed) >= spec.MaxValues {
			return nil, fmt.Errorf("%w: label %s", ErrCardinalityLimit, spec.Name)
		}
	}
	if !state.registry.reserveSeries() {
		return nil, ErrCardinalityLimit
	}
	for index, item := range ordered {
		state.labelValues[index][item.value] = struct{}{}
	}
	current = &series{
		labels: ordered,
		bucket: make([]uint64, len(state.descriptor.Buckets)),
	}
	state.series[key] = current
	return current, nil
}

func (state *registryState) reserveSeries() bool {
	if state == nil || state.maxSeries <= 0 || state.metrics == nil {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.totalSeries >= state.maxSeries {
		return false
	}
	state.totalSeries++
	return true
}

func (registry *Registry) valid() bool {
	return registry != nil && registry.state != nil &&
		registry.state.maxMetrics > 0 && registry.state.maxSeries > 0 &&
		registry.state.metrics != nil
}

func (instrument *Instrument) valid() bool {
	return instrument != nil && instrument.state != nil &&
		instrument.state.registry != nil && instrument.state.series != nil
}

func (instrument *Instrument) validateLabels(labels Labels) ([]labelValue, string, error) {
	if !instrument.valid() {
		return nil, "", ErrWrongKind
	}
	descriptor := instrument.state.descriptor
	if len(labels) != len(descriptor.Labels) {
		return nil, "", ErrInvalidLabels
	}
	ordered := make([]labelValue, len(descriptor.Labels))
	var key strings.Builder
	for index, spec := range descriptor.Labels {
		value, exists := labels[spec.Name]
		if !exists || !validLabelValue(value) {
			return nil, "", fmt.Errorf("%w: %s", ErrInvalidLabels, spec.Name)
		}
		if len(spec.Values) > 0 && !slices.Contains(spec.Values, value) {
			return nil, "", fmt.Errorf("%w: %s", ErrInvalidLabels, spec.Name)
		}
		ordered[index] = labelValue{name: spec.Name, value: value}
		key.WriteString(fmt.Sprintf("%d:%s", len(value), value))
	}
	return ordered, key.String(), nil
}

func normalizeDescriptor(
	descriptor Descriptor,
	registryMaxSeries int,
	denied map[string]struct{},
) (Descriptor, error) {
	if len(descriptor.Name) == 0 || len(descriptor.Name) > maxMetricName ||
		!metricNamePattern.MatchString(descriptor.Name) ||
		!validHelp(descriptor.Help) ||
		(descriptor.Kind != CounterKind && descriptor.Kind != GaugeKind && descriptor.Kind != HistogramKind) ||
		len(descriptor.Labels) > maxLabels {
		return Descriptor{}, ErrInvalidDescriptor
	}
	if descriptor.Kind == CounterKind && !strings.HasSuffix(descriptor.Name, "_total") {
		return Descriptor{}, fmt.Errorf("%w: counter names must end in _total", ErrInvalidDescriptor)
	}
	if descriptor.MaxSeries < 0 || descriptor.MaxSeries > registryMaxSeries {
		return Descriptor{}, ErrInvalidDescriptor
	}
	if descriptor.MaxSeries == 0 {
		descriptor.MaxSeries = min(defaultMaxSeries, registryMaxSeries)
	}
	if descriptor.Kind == HistogramKind {
		if len(descriptor.Buckets) == 0 || len(descriptor.Buckets) > maxBuckets {
			return Descriptor{}, ErrInvalidDescriptor
		}
		for index, bucket := range descriptor.Buckets {
			if !finite(bucket) || bucket <= 0 || (index > 0 && bucket <= descriptor.Buckets[index-1]) {
				return Descriptor{}, ErrInvalidDescriptor
			}
		}
	} else if len(descriptor.Buckets) != 0 {
		return Descriptor{}, ErrInvalidDescriptor
	}

	descriptor.Labels = append([]LabelSpec(nil), descriptor.Labels...)
	names := make(map[string]struct{}, len(descriptor.Labels))
	for index := range descriptor.Labels {
		spec := &descriptor.Labels[index]
		if len(spec.Name) == 0 || len(spec.Name) > maxLabelName ||
			!labelNamePattern.MatchString(spec.Name) {
			return Descriptor{}, ErrInvalidDescriptor
		}
		normalizedName := strings.ToLower(spec.Name)
		if unsafeLabelName(normalizedName) {
			return Descriptor{}, fmt.Errorf("%w: unsafe label %s", ErrInvalidDescriptor, spec.Name)
		}
		if _, forbidden := denied[normalizedName]; forbidden {
			return Descriptor{}, fmt.Errorf("%w: denied label %s", ErrInvalidDescriptor, spec.Name)
		}
		if _, exists := names[spec.Name]; exists {
			return Descriptor{}, ErrInvalidDescriptor
		}
		names[spec.Name] = struct{}{}
		if len(spec.Values) > 0 {
			unique := make(map[string]struct{}, len(spec.Values))
			for _, value := range spec.Values {
				if !validLabelValue(value) {
					return Descriptor{}, ErrInvalidDescriptor
				}
				unique[value] = struct{}{}
			}
			if len(unique) != len(spec.Values) {
				return Descriptor{}, ErrInvalidDescriptor
			}
			spec.Values = append([]string(nil), spec.Values...)
			spec.MaxValues = len(spec.Values)
		} else if spec.MaxValues <= 0 || spec.MaxValues > descriptor.MaxSeries {
			return Descriptor{}, ErrInvalidDescriptor
		}
	}
	descriptor.Buckets = append([]float64(nil), descriptor.Buckets...)
	return descriptor, nil
}

func validHelp(value string) bool {
	if len(value) == 0 || len(value) > maxHelpBytes || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if character != '\n' && unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func unsafeLabelName(name string) bool {
	if _, sensitive := sensitiveLabelNames[name]; sensitive {
		return true
	}
	for _, suffix := range []string{
		"_id", "_token", "_secret", "_password", "_email", "_ip",
	} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

func validLabelValue(value string) bool {
	if len(value) == 0 || len(value) > maxLabelValue || !utf8.ValidString(value) {
		return false
	}
	for index := 0; index < len(value); index++ {
		char := value[index]
		if char < 0x20 || char == 0x7f || char == '\\' || char == '"' {
			return false
		}
	}
	return true
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
