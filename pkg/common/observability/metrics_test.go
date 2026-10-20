package observability

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
)

func TestRegistryRejectsUnboundedOrUnsafeConfiguration(t *testing.T) {
	t.Parallel()

	tests := []Options{
		{MaxMetrics: -1},
		{MaxMetrics: hardMaxMetrics + 1},
		{MaxSeries: -1},
		{MaxSeries: hardMaxSeries + 1},
		{DeniedLabels: []string{"not a label"}},
	}
	for _, options := range tests {
		if _, err := NewRegistry(options); !errors.Is(err, ErrInvalidOptions) {
			t.Fatalf("NewRegistry(%+v) error = %v, want ErrInvalidOptions", options, err)
		}
	}
}

func TestZeroValueHandlesFailClosed(t *testing.T) {
	t.Parallel()

	if _, err := new(Registry).Register(Descriptor{
		Name: "requests_total", Help: "Requests.", Kind: CounterKind,
	}); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("zero Registry.Register() error = %v, want ErrInvalidOptions", err)
	}
	if err := new(Instrument).Inc(context.Background(), nil); !errors.Is(err, ErrWrongKind) {
		t.Fatalf("zero Instrument.Inc() error = %v, want ErrWrongKind", err)
	}
	if err := new(Instrument).Add(nil, 1, nil); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("Instrument.Add(nil context) error = %v, want ErrInvalidValue", err)
	}
	if err := new(Instrument).Set(nil, 1, nil); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("Instrument.Set(nil context) error = %v, want ErrInvalidValue", err)
	}
	if err := new(Instrument).Observe(nil, 1, nil); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("Instrument.Observe(nil context) error = %v, want ErrInvalidValue", err)
	}
}

func TestRegistryRejectsUnsafeDescriptorsAndCopiesConfiguration(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry(Options{
		MaxMetrics:   4,
		MaxSeries:    8,
		DeniedLabels: []string{"account"},
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	invalid := []Descriptor{
		{Name: "requests", Help: "counter", Kind: CounterKind},
		{Name: "requests_total", Help: "counter", Kind: CounterKind, Labels: []LabelSpec{{Name: "user_id", MaxValues: 2}}},
		{Name: "requests_total", Help: "counter", Kind: CounterKind, Labels: []LabelSpec{{Name: "account", MaxValues: 2}}},
		{Name: "requests_total", Help: "counter", Kind: CounterKind, Labels: []LabelSpec{{Name: "outcome", Values: []string{"ok", string([]byte{0xff})}}}},
		{Name: "latency_seconds", Help: "histogram", Kind: HistogramKind, Buckets: []float64{1, 1}},
		{Name: "queue_depth", Help: "gauge", Kind: GaugeKind, MaxSeries: 9},
	}
	for _, descriptor := range invalid {
		if _, registerErr := registry.Register(descriptor); !errors.Is(registerErr, ErrInvalidDescriptor) {
			t.Fatalf("Register(%+v) error = %v, want ErrInvalidDescriptor", descriptor, registerErr)
		}
	}

	values := []string{"ready", "running"}
	buckets := []float64{0.1, 1}
	descriptor := Descriptor{
		Name:      "work_seconds",
		Help:      "Bounded work duration.",
		Kind:      HistogramKind,
		Labels:    []LabelSpec{{Name: "state", Values: values}},
		Buckets:   buckets,
		MaxSeries: 2,
	}
	instrument, err := registry.Register(descriptor)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	values[0] = "mutated"
	buckets[0] = 999
	if err := instrument.Observe(context.Background(), 0.05, Labels{"state": "ready"}); err != nil {
		t.Fatalf("Observe() after caller mutation error = %v", err)
	}
	if err := instrument.Observe(context.Background(), 0.05, Labels{"state": "mutated"}); !errors.Is(err, ErrInvalidLabels) {
		t.Fatalf("Observe() with mutated caller value error = %v, want ErrInvalidLabels", err)
	}
}

func TestInstrumentRejectsInvalidUTF8LabelValue(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry(Options{MaxMetrics: 1, MaxSeries: 2})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	instrument, err := registry.Register(Descriptor{
		Name: "queue_depth", Help: "Queue depth.", Kind: GaugeKind,
		Labels: []LabelSpec{{Name: "queue", MaxValues: 2}}, MaxSeries: 2,
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := instrument.Set(context.Background(), 1, Labels{
		"queue": string([]byte{'q', 0xff}),
	}); !errors.Is(err, ErrInvalidLabels) {
		t.Fatalf("Set() error = %v, want ErrInvalidLabels", err)
	}
}

func TestRegistryEnforcesMetricAndGlobalSeriesBudgets(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry(Options{MaxMetrics: 2, MaxSeries: 2})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	descriptor := func(name string) Descriptor {
		return Descriptor{
			Name:      name,
			Help:      "Bounded state.",
			Kind:      GaugeKind,
			Labels:    []LabelSpec{{Name: "state", Values: []string{"ready", "running"}}},
			MaxSeries: 2,
		}
	}
	first, err := registry.Register(descriptor("first_state"))
	if err != nil {
		t.Fatalf("register first: %v", err)
	}
	second, err := registry.Register(descriptor("second_state"))
	if err != nil {
		t.Fatalf("register second: %v", err)
	}
	if err := first.Set(context.Background(), 1, Labels{"state": "ready"}); err != nil {
		t.Fatalf("set first series: %v", err)
	}
	if err := second.Set(context.Background(), 1, Labels{"state": "ready"}); err != nil {
		t.Fatalf("set second series: %v", err)
	}
	if err := second.Set(context.Background(), 1, Labels{"state": "running"}); !errors.Is(err, ErrCardinalityLimit) {
		t.Fatalf("set over global series budget error = %v, want ErrCardinalityLimit", err)
	}
	if _, exists := second.state.labelValues[0]["running"]; exists {
		t.Fatal("rejected series consumed label cardinality")
	}
	if _, err := registry.Register(descriptor("third_state")); !errors.Is(err, ErrCardinalityLimit) {
		t.Fatalf("register over metric budget error = %v, want ErrCardinalityLimit", err)
	}
	if _, err := registry.Register(descriptor("first_state")); !errors.Is(err, ErrDuplicateMetric) {
		t.Fatalf("duplicate at capacity error = %v, want ErrDuplicateMetric", err)
	}
}

func TestInstrumentOperationsAreConcurrentAndContextAware(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry(Options{MaxMetrics: 1, MaxSeries: 2})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	counter, err := registry.Register(Descriptor{
		Name: "operations_total", Help: "Completed operations.", Kind: CounterKind,
		Labels:    []LabelSpec{{Name: "outcome", Values: []string{"success", "error"}}},
		MaxSeries: 2,
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	registryCopy := *registry
	if _, err := registryCopy.Register(Descriptor{
		Name: "copy_probe", Help: "Copy-safe registry probe.", Kind: GaugeKind, MaxSeries: 1,
	}); !errors.Is(err, ErrCardinalityLimit) {
		t.Fatalf("copied registry must share metric budget, error = %v", err)
	}
	instrumentCopy := *counter

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := counter.Inc(cancelled, Labels{"outcome": "error"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Inc(cancelled) error = %v, want context.Canceled", err)
	}

	const goroutines = 16
	const increments = 500
	var group sync.WaitGroup
	errorsFound := make(chan error, goroutines)
	for index := range goroutines {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			for range increments {
				target := counter
				if index%2 == 1 {
					target = &instrumentCopy
				}
				if incrementErr := target.Inc(context.Background(), Labels{"outcome": "success"}); incrementErr != nil {
					errorsFound <- incrementErr
					return
				}
			}
		}(index)
	}
	group.Wait()
	close(errorsFound)
	for incrementErr := range errorsFound {
		t.Fatalf("concurrent Inc() error = %v", incrementErr)
	}

	_, snapshots := counter.snapshot()
	if len(snapshots) != 1 {
		t.Fatalf("snapshot series = %d, want 1", len(snapshots))
	}
	if got, want := snapshots[0].value, float64(goroutines*increments); got != want {
		t.Fatalf("counter = %v, want %v", got, want)
	}
	if registry.state.totalSeries != 1 {
		t.Fatalf("registry total series = %d, want 1", registry.state.totalSeries)
	}
}

func TestInstrumentRejectsOverflowWithoutPartialMutation(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry(Options{MaxMetrics: 2, MaxSeries: 2})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	counter, err := registry.Register(Descriptor{
		Name: "bytes_total", Help: "Processed bytes.", Kind: CounterKind, MaxSeries: 1,
	})
	if err != nil {
		t.Fatalf("register counter: %v", err)
	}
	if err := counter.Add(context.Background(), math.MaxFloat64, nil); err != nil {
		t.Fatalf("seed counter: %v", err)
	}
	if err := counter.Add(context.Background(), math.MaxFloat64, nil); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("overflowing counter error = %v, want ErrInvalidValue", err)
	}
	_, counterSnapshots := counter.snapshot()
	if got := counterSnapshots[0].value; got != math.MaxFloat64 {
		t.Fatalf("counter after rejected overflow = %v, want %v", got, math.MaxFloat64)
	}

	histogram, err := registry.Register(Descriptor{
		Name: "latency_seconds", Help: "Request latency.", Kind: HistogramKind,
		Buckets: []float64{1, 2}, MaxSeries: 1,
	})
	if err != nil {
		t.Fatalf("register histogram: %v", err)
	}
	if err := histogram.Observe(context.Background(), 0.5, nil); err != nil {
		t.Fatalf("seed histogram: %v", err)
	}
	histogram.state.mu.Lock()
	current := histogram.state.series[""]
	current.bucket[0] = ^uint64(0)
	beforeCount, beforeSum, beforeSecondBucket := current.count, current.sum, current.bucket[1]
	histogram.state.mu.Unlock()

	if err := histogram.Observe(context.Background(), 0.5, nil); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("overflowing histogram error = %v, want ErrInvalidValue", err)
	}
	histogram.state.mu.RLock()
	defer histogram.state.mu.RUnlock()
	if current.count != beforeCount || current.sum != beforeSum || current.bucket[1] != beforeSecondBucket {
		t.Fatalf("histogram mutated partially after overflow: %+v", current)
	}
}
