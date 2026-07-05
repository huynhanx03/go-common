package forge

import (
	"context"
	"sync"
	"time"

	"github.com/huynhanx03/go-common/pkg/common/locks"
)

// ProducerConfig configures a Producer.
type ProducerConfig struct {
	BatchSize       int           // max bytes before flush (default 16KB)
	LingerTime      time.Duration // max wait before flush (default 5ms)
	Compression     uint8         // CompressionNone or CompressionLZ4
	Clock           func() int64  // injectable timestamp source (unix nanos), nil = time.Now
	MaxPendingBytes int           // backpressure limit (0 = unlimited)
	OnError         func(error)   // async mode: non-nil = Send() never blocks on flush errors
	ShutdownTimeout time.Duration // max wait for flush on Close (0 = block forever)
	Metrics         *MetricsHook  // observability callbacks
	Topic           string        // topic name for metrics reporting
}

// Producer accumulates records and flushes them as batches to a CommitLog.
// Inspired by Kafka's RecordAccumulator: batching + linger for throughput.
const estimatedRecordsPerBatch = 64

type Producer struct {
	mu             sync.Locker  // SpinLock — Send() hold time <50ns
	log            *CommitLog
	config         ProducerConfig
	pending        []Record
	backBuf        []Record     // double-buffer: swap with pending on flush to avoid alloc
	bufSize        int          // estimated bytes of pending records
	closed         bool         // set by Close(), checked by Send()
	clock          func() int64 // timestamp source
	maxMessageSize int

	flushCh  chan struct{} // signals the linger goroutine
	stopCh   chan struct{}
	wg       sync.WaitGroup
	closeOnce sync.Once
}

func defaultProducerConfig() ProducerConfig {
	return ProducerConfig{
		BatchSize:  defaultBatchBytes,
		LingerTime: defaultLingerTime,
	}
}

// ProducerOption configures a Producer.
type ProducerOption func(*ProducerConfig)

// WithBatchSize sets the max accumulated bytes before auto-flush.
func WithBatchSize(n int) ProducerOption {
	return func(c *ProducerConfig) {
		if n > 0 {
			c.BatchSize = n
		}
	}
}

// WithLinger sets the max time to wait before flushing a partial batch.
func WithLinger(d time.Duration) ProducerOption {
	return func(c *ProducerConfig) {
		if d > 0 {
			c.LingerTime = d
		}
	}
}

// WithCompression sets the batch compression type (CompressionNone or CompressionLZ4).
func WithCompression(ct uint8) ProducerOption {
	return func(c *ProducerConfig) { c.Compression = ct }
}

// WithClock injects a timestamp source (e.g., CachedTimer.Now) to avoid syscall per flush.
func WithClock(fn func() int64) ProducerOption {
	return func(c *ProducerConfig) {
		if fn != nil {
			c.Clock = fn
		}
	}
}

// WithMaxPendingBytes sets the backpressure limit. Send() returns ErrBackpressure when exceeded.
func WithMaxPendingBytes(n int) ProducerOption {
	return func(c *ProducerConfig) {
		if n > 0 {
			c.MaxPendingBytes = n
		}
	}
}

// WithOnError enables async mode. Flush errors are sent to the callback instead of returned by Send().
func WithOnError(fn func(error)) ProducerOption {
	return func(c *ProducerConfig) { c.OnError = fn }
}

// WithShutdownTimeout sets the max time Close() waits for pending flushes.
func WithShutdownTimeout(d time.Duration) ProducerOption {
	return func(c *ProducerConfig) {
		if d > 0 {
			c.ShutdownTimeout = d
		}
	}
}

// WithMetrics attaches observability hooks to the producer.
func WithMetrics(m *MetricsHook) ProducerOption {
	return func(c *ProducerConfig) { c.Metrics = m }
}

// NewProducer creates a producer that writes to the given commit log.
func NewProducer(log *CommitLog, opts ...ProducerOption) *Producer {
	cfg := defaultProducerConfig()
	for _, o := range opts {
		o(&cfg)
	}

	clock := cfg.Clock
	if clock == nil {
		clock = func() int64 { return time.Now().UnixNano() }
	}

	p := &Producer{
		mu:             locks.NewSpinLock(),
		log:            log,
		config:         cfg,
		pending:        make([]Record, 0, estimatedRecordsPerBatch),
		backBuf:        make([]Record, 0, estimatedRecordsPerBatch),
		clock:          clock,
		maxMessageSize: log.config.MaxMessageSize,
		flushCh:        make(chan struct{}, 1),
		stopCh:         make(chan struct{}),
	}

	p.wg.Add(1)
	go p.lingerLoop()

	return p
}

// Send enqueues a record. Flushes automatically when batch size is reached.
// Returns ErrBackpressure if MaxPendingBytes is set and exceeded.
// In async mode (OnError set), flush errors go to the callback.
func (p *Producer) Send(key, value []byte, headers []Header) error {
	if p.maxMessageSize > 0 && len(value) > p.maxMessageSize {
		return ErrMessageTooLarge
	}

	rec := Record{
		Key:     key,
		Value:   value,
		Headers: headers,
	}

	recSize := varintBytesSize(key) + varintBytesSize(value) + estimatedRecordOverhead

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return ErrClosed
	}
	// Backpressure check.
	if p.config.MaxPendingBytes > 0 && p.bufSize+recSize > p.config.MaxPendingBytes {
		p.mu.Unlock()
		p.config.Metrics.backpressureHook(p.config.Topic)
		return ErrBackpressure
	}
	p.pending = append(p.pending, rec)
	p.bufSize += recSize
	shouldFlush := p.bufSize >= p.config.BatchSize
	p.mu.Unlock()

	if shouldFlush {
		return p.flush()
	}

	// Signal linger timer that there's data.
	select {
	case p.flushCh <- struct{}{}:
	default:
	}
	return nil
}

// flush drains pending records and writes them as a batch.
// In async mode, errors are sent to OnError callback instead of returned.
func (p *Producer) flush() error {
	p.mu.Lock()
	if len(p.pending) == 0 {
		p.mu.Unlock()
		return nil
	}

	// Double-buffer swap: reuse backBuf as new pending, avoid allocation.
	records := p.pending
	estimatedBytes := p.bufSize
	p.pending = p.backBuf[:0]
	p.backBuf = nil
	p.bufSize = 0
	p.mu.Unlock()

	now := p.clock()
	batch := &RecordBatch{
		Compression: p.config.Compression,
		Timestamp:   now,
		MaxTimestamp: now,
		Records:     records,
	}

	_, err := p.log.Append(batch)

	// Metrics: report flush with estimated payload bytes.
	if err == nil {
		p.config.Metrics.flushHook(p.config.Topic, len(records), estimatedBytes)
	}

	// Reclaim records slice for reuse. Clear references to allow GC of key/value bytes.
	for i := range records {
		records[i] = Record{}
	}
	p.mu.Lock()
	if p.backBuf == nil {
		p.backBuf = records[:0]
	}
	p.mu.Unlock()

	// Async mode: route errors to callback.
	if err != nil && p.config.OnError != nil {
		p.config.OnError(err)
		return nil
	}

	return err
}

// Flush forces all pending records to be written as a batch.
func (p *Producer) Flush() error {
	return p.flush()
}

// lingerLoop flushes pending records after LingerTime if batch isn't full yet.
func (p *Producer) lingerLoop() {
	defer p.wg.Done()
	timer := time.NewTimer(p.config.LingerTime)
	defer timer.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-p.flushCh:
			// Data arrived — wait up to LingerTime then flush.
			timer.Reset(p.config.LingerTime)
			select {
			case <-timer.C:
				p.flush()
			case <-p.stopCh:
				return
			}
		}
	}
}

// Close flushes remaining records and stops the linger goroutine.
// Safe to call multiple times. Respects ShutdownTimeout if configured.
func (p *Producer) Close() error {
	var err error
	p.closeOnce.Do(func() {
		close(p.stopCh)
		p.wg.Wait()

		// Mark closed AFTER linger goroutine exits but BEFORE final flush,
		// so Send() is rejected but flush() can still drain pending records.
		p.mu.Lock()
		p.closed = true
		p.mu.Unlock()

		if p.config.ShutdownTimeout <= 0 {
			err = p.flush()
			return
		}

		// Graceful shutdown with timeout.
		ctx, cancel := context.WithTimeout(context.Background(), p.config.ShutdownTimeout)
		defer cancel()

		done := make(chan error, 1)
		go func() { done <- p.flush() }()

		select {
		case e := <-done:
			err = e
		case <-ctx.Done():
			err = ctx.Err()
		}
	})
	return err
}
