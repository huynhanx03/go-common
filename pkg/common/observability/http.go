package observability

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// HTTPHandler exposes a deterministic Prometheus text endpoint. It intentionally
// performs no authentication; mount it only on a trusted internal listener or
// behind an explicit authorization guard.
func HTTPHandler(registry *Registry) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			http.Error(writer, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		if !registry.valid() {
			http.Error(writer, "metrics registry unavailable", http.StatusServiceUnavailable)
			return
		}
		writer.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		writer.Header().Set("Cache-Control", "no-store")
		if err := registry.writePrometheus(writer); err != nil {
			return
		}
	})
}

func (registry *Registry) writePrometheus(writer io.Writer) error {
	state := registry.state
	state.mu.RLock()
	names := make([]string, 0, len(state.metrics))
	metrics := make(map[string]*Instrument, len(state.metrics))
	for name, metric := range state.metrics {
		names = append(names, name)
		metrics[name] = metric
	}
	state.mu.RUnlock()
	sort.Strings(names)

	for _, name := range names {
		metric := metrics[name]
		descriptor, snapshots := metric.snapshot()
		if _, err := fmt.Fprintf(writer, "# HELP %s %s\n# TYPE %s %s\n",
			descriptor.Name,
			escapeHelp(descriptor.Help),
			descriptor.Name,
			kindName(descriptor.Kind),
		); err != nil {
			return err
		}
		for _, snapshot := range snapshots {
			if err := writeSeries(writer, descriptor, snapshot); err != nil {
				return err
			}
		}
	}
	return nil
}

type seriesSnapshot struct {
	labels []labelValue
	value  float64
	count  uint64
	sum    float64
	bucket []uint64
}

func (instrument *Instrument) snapshot() (Descriptor, []seriesSnapshot) {
	state := instrument.state
	state.mu.RLock()
	keys := make([]string, 0, len(state.series))
	for key := range state.series {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]seriesSnapshot, 0, len(keys))
	for _, key := range keys {
		current := state.series[key]
		result = append(result, seriesSnapshot{
			labels: append([]labelValue(nil), current.labels...),
			value:  current.value,
			count:  current.count,
			sum:    current.sum,
			bucket: append([]uint64(nil), current.bucket...),
		})
	}
	descriptor := state.descriptor
	state.mu.RUnlock()
	return descriptor, result
}

func writeSeries(writer io.Writer, descriptor Descriptor, snapshot seriesSnapshot) error {
	baseLabels := prometheusLabels(snapshot.labels, "")
	if descriptor.Kind != HistogramKind {
		_, err := fmt.Fprintf(writer, "%s%s %s\n",
			descriptor.Name,
			baseLabels,
			strconv.FormatFloat(snapshot.value, 'g', -1, 64),
		)
		return err
	}
	for index, upper := range descriptor.Buckets {
		labels := prometheusLabels(snapshot.labels, strconv.FormatFloat(upper, 'g', -1, 64))
		if _, err := fmt.Fprintf(writer, "%s_bucket%s %d\n", descriptor.Name, labels, snapshot.bucket[index]); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(writer, "%s_bucket%s %d\n",
		descriptor.Name,
		prometheusLabels(snapshot.labels, "+Inf"),
		snapshot.count,
	); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "%s_sum%s %s\n",
		descriptor.Name,
		baseLabels,
		strconv.FormatFloat(snapshot.sum, 'g', -1, 64),
	); err != nil {
		return err
	}
	_, err := fmt.Fprintf(writer, "%s_count%s %d\n", descriptor.Name, baseLabels, snapshot.count)
	return err
}

func prometheusLabels(labels []labelValue, upperBound string) string {
	if len(labels) == 0 && upperBound == "" {
		return ""
	}
	var result strings.Builder
	result.WriteByte('{')
	for index, label := range labels {
		if index > 0 {
			result.WriteByte(',')
		}
		result.WriteString(label.name)
		result.WriteString(`="`)
		result.WriteString(escapeLabel(label.value))
		result.WriteByte('"')
	}
	if upperBound != "" {
		if len(labels) > 0 {
			result.WriteByte(',')
		}
		result.WriteString(`le="`)
		result.WriteString(upperBound)
		result.WriteByte('"')
	}
	result.WriteByte('}')
	return result.String()
}

func escapeHelp(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, "\n", `\n`)
}

func escapeLabel(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	return strings.ReplaceAll(value, `"`, `\"`)
}

func kindName(kind Kind) string {
	switch kind {
	case CounterKind:
		return "counter"
	case GaugeKind:
		return "gauge"
	case HistogramKind:
		return "histogram"
	default:
		return "untyped"
	}
}
