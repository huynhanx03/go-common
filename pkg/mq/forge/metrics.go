package forge

// MetricsHook provides observability callbacks for Forge MQ.
// All callbacks are optional — nil means no-op.
type MetricsHook struct {
	// OnFlush is called after a producer flushes a batch.
	// records is the record count; bytes is the admitted logical record size
	// before compression and excludes batch framing.
	OnFlush func(topic string, records int, bytes int)

	// OnPoll is called after a consumer polls records.
	OnPoll func(group, topic string, records int)

	// OnDrop is called when a record is dropped (e.g., DLQ failure).
	OnDrop func(topic string, reason string)

	// OnBackpressure is called when Send() is rejected due to backpressure.
	OnBackpressure func(topic string)
}

func (m *MetricsHook) flushHook(topic string, records, bytes int) {
	if m != nil && m.OnFlush != nil {
		safeMetric(func() { m.OnFlush(topic, records, bytes) })
	}
}

func (m *MetricsHook) pollHook(group, topic string, records int) {
	if m != nil && m.OnPoll != nil {
		safeMetric(func() { m.OnPoll(group, topic, records) })
	}
}

func (m *MetricsHook) dropHook(topic string, reason string) {
	if m != nil && m.OnDrop != nil {
		safeMetric(func() { m.OnDrop(topic, reason) })
	}
}

func (m *MetricsHook) backpressureHook(topic string) {
	if m != nil && m.OnBackpressure != nil {
		safeMetric(func() { m.OnBackpressure(topic) })
	}
}

func safeMetric(callback func()) {
	defer func() {
		_ = recover()
	}()
	callback()
}
