package forge

import (
	"sync"

	"github.com/huynhanx03/go-common/pkg/common/locks"
)

// Consumer reads records from a CommitLog with offset tracking.
// All methods are safe for concurrent use.
// Uses SpinLock: consumers are typically single-goroutine, low contention.
type Consumer struct {
	mu          sync.Locker // SpinLock — low contention, fast uncontended CAS
	log         *CommitLog
	group       string
	topic       string
	offset      uint64
	offsetStore *OffsetStore
	metrics     *MetricsHook

	// DLQ support: optional producer to route failed messages.
	dlq *Producer
}

// ConsumerOption configures a Consumer.
type ConsumerOption func(*consumerConfig)

type consumerConfig struct {
	dlq     *Producer
	metrics *MetricsHook
}

// WithDLQ attaches a dead-letter queue producer to the consumer.
func WithDLQ(p *Producer) ConsumerOption {
	return func(c *consumerConfig) { c.dlq = p }
}

// WithConsumerMetrics attaches observability hooks to the consumer.
func WithConsumerMetrics(m *MetricsHook) ConsumerOption {
	return func(c *consumerConfig) { c.metrics = m }
}

// NewConsumer creates a consumer that reads from the given commit log.
// It loads the last committed offset for the group/topic pair.
func NewConsumer(log *CommitLog, group, topic string, store *OffsetStore, opts ...ConsumerOption) (*Consumer, error) {
	offset, err := store.Load(group, topic)
	if err != nil {
		return nil, err
	}

	var cfg consumerConfig
	for _, o := range opts {
		o(&cfg)
	}

	return &Consumer{
		mu:          locks.NewSpinLock(),
		log:         log,
		group:       group,
		topic:       topic,
		offset:      offset,
		offsetStore: store,
		metrics:     cfg.metrics,
		dlq:         cfg.dlq,
	}, nil
}

// Poll reads up to maxRecords from the current offset.
// Advances the in-memory offset (but does not commit).
func (c *Consumer) Poll(maxRecords int) ([]Record, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	maxBytes := maxRecords * estimatedBytesPerRecord
	if maxBytes < minReadBytes {
		maxBytes = minReadBytes
	}

	batches, err := c.log.Read(c.offset, maxBytes)
	if err != nil {
		return nil, err
	}

	records := make([]Record, 0, maxRecords)
	for _, batch := range batches {
		for _, rec := range batch.Records {
			absOffset := batch.BaseOffset + uint64(rec.OffsetDelta)
			if absOffset < c.offset {
				continue // already consumed
			}
			records = append(records, rec)
			if len(records) >= maxRecords {
				c.offset = absOffset + 1
				c.metrics.pollHook(c.group, c.topic, len(records))
				return records, nil
			}
		}
		// Advance past the entire batch.
		c.offset = batch.BaseOffset + uint64(batch.RecordCount)
	}

	if len(records) > 0 {
		c.metrics.pollHook(c.group, c.topic, len(records))
	}
	return records, nil
}

// Nack sends a failed record to the dead-letter queue.
// Returns nil if no DLQ is configured (record is silently dropped).
func (c *Consumer) Nack(rec Record) error {
	if c.dlq == nil {
		c.metrics.dropHook(c.topic, "no DLQ configured")
		return nil
	}

	// Copy headers to avoid aliasing the caller's slice.
	headers := make([]Header, len(rec.Headers)+1)
	copy(headers, rec.Headers)
	headers[len(rec.Headers)] = Header{
		Key:   []byte(dlqOriginalTopicKey),
		Value: []byte(c.topic),
	}

	err := c.dlq.Send(rec.Key, rec.Value, headers)
	if err != nil {
		c.metrics.dropHook(c.topic, "DLQ send failed: "+err.Error())
	}
	return err
}

// Commit persists the current offset to the offset store.
func (c *Consumer) Commit() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.offsetStore.Commit(c.group, c.topic, c.offset)
}

// Seek sets the consumer's read position to the given offset.
func (c *Consumer) Seek(offset uint64) {
	c.mu.Lock()
	c.offset = offset
	c.mu.Unlock()
}

// SeekToBeginning resets to the oldest available offset.
func (c *Consumer) SeekToBeginning() {
	c.mu.Lock()
	c.offset = c.log.OldestOffset()
	c.mu.Unlock()
}

// SeekToEnd jumps to the newest offset (tail).
func (c *Consumer) SeekToEnd() {
	c.mu.Lock()
	c.offset = c.log.NewestOffset()
	c.mu.Unlock()
}

// Offset returns the current read position.
func (c *Consumer) Offset() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.offset
}
