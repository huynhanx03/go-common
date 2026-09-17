package forge

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// --- Backpressure ---

func TestBackpressureRejectsWhenFull(t *testing.T) {
	dir := t.TempDir()
	b, err := NewBroker(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	// MaxPendingBytes = 100 bytes, BatchSize high so it never auto-flushes.
	p, err := b.NewProducer("bp-test",
		WithMaxPendingBytes(100),
		WithBatchSize(1<<20),
		WithLinger(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	// Fill up the buffer.
	bigValue := make([]byte, 80)
	if err := p.Send(nil, bigValue, nil); err != nil {
		t.Fatalf("first send should succeed: %v", err)
	}

	// Second send should exceed MaxPendingBytes.
	if err := p.Send(nil, bigValue, nil); err != ErrBackpressure {
		t.Fatalf("expected ErrBackpressure, got %v", err)
	}

	// After flush, should be able to send again.
	if err := p.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := p.Send(nil, bigValue, nil); err != nil {
		t.Fatalf("send after flush should succeed: %v", err)
	}
}

func TestProducerContainsClockPanicAndRestoresBatch(t *testing.T) {
	log, err := NewCommitLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	producer, err := NewProducer(
		log,
		WithBatchSize(1<<20),
		WithLinger(time.Hour),
		WithClock(func() int64 { panic("clock") }),
	)
	if err != nil {
		_ = log.Close()
		t.Fatal(err)
	}
	if err := producer.Send(nil, []byte("retained"), nil); err != nil {
		t.Fatal(err)
	}
	if err := producer.Flush(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Flush clock panic error = %v, want ErrInvalidConfig", err)
	}
	if got := producer.PendingRecords(); got != 1 {
		t.Fatalf("pending records after clock panic = %d, want 1", got)
	}
	if err := producer.Close(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Close clock panic error = %v, want ErrInvalidConfig", err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultPendingBudgetAcceptsNormalWorkload(t *testing.T) {
	dir := t.TempDir()
	b, err := NewBroker(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	p, err := b.NewProducer("bp-unlimited",
		WithBatchSize(1<<20),
		WithLinger(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	// The default is bounded but comfortably accepts an ordinary batch.
	for i := 0; i < 100; i++ {
		if err := p.Send(nil, []byte("data"), nil); err != nil {
			t.Fatalf("send %d failed: %v", i, err)
		}
	}
}

// --- Async Producer (OnError) ---

func TestAsyncProducerRoutesErrorsToCallback(t *testing.T) {
	dir := t.TempDir()
	log, err := NewCommitLog(dir)
	if err != nil {
		t.Fatal(err)
	}

	var errorCount atomic.Int32
	p, err := NewProducer(log,
		WithBatchSize(1), // flush every message
		WithOnError(func(err error) {
			errorCount.Add(1)
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	// Send a message — should flush immediately.
	if err := p.Send(nil, []byte("hello"), nil); err != nil {
		t.Fatal(err)
	}

	p.Close()
	log.Close()

	// Close the log, then try to flush — should trigger OnError.
	log2, _ := NewCommitLog(dir)
	p2, err := NewProducer(log2,
		WithBatchSize(1),
		WithOnError(func(err error) {
			errorCount.Add(1)
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	log2.Close()
	// Send to closed log — flush error goes to OnError, Send returns nil.
	err = p2.Send(nil, []byte("fail"), nil)
	if err != nil {
		t.Fatalf("async Send should return nil even on flush error, got: %v", err)
	}

	time.Sleep(10 * time.Millisecond) // let linger flush happen
	p2.Close()

	if errorCount.Load() == 0 {
		t.Fatal("expected OnError callback to be called at least once")
	}
}

// --- Graceful Shutdown ---

func TestGracefulShutdownFlushesOnClose(t *testing.T) {
	dir := t.TempDir()
	b, err := NewBroker(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	p, err := b.NewProducer("shutdown-test",
		WithLinger(time.Hour), // never auto-flush via linger
		WithBatchSize(1<<20),  // never auto-flush via size
	)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 10; i++ {
		p.Send(nil, []byte("pending"), nil)
	}

	// Close should flush within timeout.
	if err := p.Close(); err != nil {
		t.Fatalf("close with shutdown timeout failed: %v", err)
	}

	// Verify records were flushed.
	c, err := b.NewConsumer("g", "shutdown-test")
	if err != nil {
		t.Fatal(err)
	}
	records, err := c.Poll(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 10 {
		t.Fatalf("expected 10 records after shutdown flush, got %d", len(records))
	}
}

// --- Metrics Hook ---

func TestMetricsHookFires(t *testing.T) {
	dir := t.TempDir()
	b, err := NewBroker(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	var flushCount, pollCount atomic.Int32
	metrics := &MetricsHook{
		OnFlush: func(topic string, records, bytes int) {
			flushCount.Add(1)
		},
		OnPoll: func(group, topic string, records int) {
			pollCount.Add(1)
		},
	}

	p, err := b.NewProducer("metrics-test",
		WithBatchSize(1), // flush every message
		WithMetrics(metrics),
	)
	if err != nil {
		t.Fatal(err)
	}

	p.Send(nil, []byte("m1"), nil)
	p.Send(nil, []byte("m2"), nil)
	p.Close()

	if flushCount.Load() < 1 {
		t.Fatalf("expected at least one flush hook, got %d", flushCount.Load())
	}

	c, err := b.NewConsumer("g", "metrics-test", WithConsumerMetrics(metrics))
	if err != nil {
		t.Fatal(err)
	}
	c.Poll(100)

	if pollCount.Load() < 1 {
		t.Fatalf("expected >= 1 poll hook, got %d", pollCount.Load())
	}
}

func TestMetricsBackpressureHook(t *testing.T) {
	dir := t.TempDir()
	b, err := NewBroker(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	var bpCount atomic.Int32
	metrics := &MetricsHook{
		OnBackpressure: func(topic string) {
			bpCount.Add(1)
		},
	}

	p, err := b.NewProducer("bp-metrics",
		WithMaxPendingBytes(50),
		WithBatchSize(1<<20),
		WithLinger(time.Hour),
		WithMetrics(metrics),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	p.Send(nil, make([]byte, 40), nil)
	p.Send(nil, make([]byte, 40), nil) // should trigger backpressure

	if bpCount.Load() < 1 {
		t.Fatalf("expected >= 1 backpressure hook, got %d", bpCount.Load())
	}
}

// --- DLQ (Dead Letter Queue) ---

func TestDLQNackRoutesMessage(t *testing.T) {
	dir := t.TempDir()
	b, err := NewBroker(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	p, _ := b.NewProducer("orders", WithBatchSize(1))
	p.Send([]byte("k1"), []byte("bad-order"), nil)
	p.Close()

	// Create consumer with DLQ.
	consumer, dlqProducer, err := b.NewDLQConsumer("worker", "orders")
	if err != nil {
		t.Fatal(err)
	}
	defer dlqProducer.Close()

	records, _ := consumer.Poll(10)
	if len(records) == 0 {
		t.Fatal("expected records from orders topic")
	}

	// Nack the first record — should go to DLQ.
	if err := consumer.Nack(records[0]); err != nil {
		t.Fatalf("Nack failed: %v", err)
	}
	dlqProducer.Flush()

	// Read from DLQ topic.
	dlqConsumer, _ := b.NewConsumer("dlq-reader", "orders.dlq")
	dlqRecords, _ := dlqConsumer.Poll(10)

	if len(dlqRecords) != 1 {
		t.Fatalf("expected 1 DLQ record, got %d", len(dlqRecords))
	}
	if string(dlqRecords[0].Value) != "bad-order" {
		t.Fatalf("expected DLQ value 'bad-order', got %q", string(dlqRecords[0].Value))
	}

	// Verify original-topic header.
	found := false
	for _, h := range dlqRecords[0].Headers {
		if string(h.Key) == "forge-original-topic" && string(h.Value) == "orders" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected forge-original-topic header in DLQ record")
	}
}

func TestNackWithoutDLQReturnsError(t *testing.T) {
	dir := t.TempDir()
	b, err := NewBroker(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	p, _ := b.NewProducer("no-dlq", WithBatchSize(1))
	p.Send(nil, []byte("msg"), nil)
	p.Close()

	c, _ := b.NewConsumer("g", "no-dlq")
	records, _ := c.Poll(10)
	if len(records) == 0 {
		t.Fatal("expected records")
	}

	if err := c.Nack(records[0]); !errors.Is(err, ErrDLQNotConfigured) {
		t.Fatalf("Nack without DLQ error = %v, want ErrDLQNotConfigured", err)
	}
}

func TestNackDeliveryContextPreservesSourceCoordinates(t *testing.T) {
	b, err := NewBroker(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	producer, err := b.NewProducer("coordinates")
	if err != nil {
		t.Fatal(err)
	}
	if err := producer.SendContext(context.Background(), []byte("key"), []byte("bad"), nil, AckFsync); err != nil {
		t.Fatal(err)
	}
	consumer, _, err := b.NewDLQConsumer("worker", "coordinates")
	if err != nil {
		t.Fatal(err)
	}
	deliveries, err := consumer.Fetch(context.Background(), 1)
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("Fetch = (%v, %v)", deliveries, err)
	}
	if err := consumer.NackDeliveryContext(context.Background(), deliveries[0]); err != nil {
		t.Fatalf("NackDeliveryContext: %v", err)
	}

	dlq, err := b.NewConsumer("auditor", "coordinates.dlq")
	if err != nil {
		t.Fatal(err)
	}
	records, err := dlq.Fetch(context.Background(), 1)
	if err != nil || len(records) != 1 {
		t.Fatalf("DLQ Fetch = (%v, %v)", records, err)
	}
	headers := make(map[string]string)
	for _, header := range records[0].Headers {
		headers[string(header.Key)] = string(header.Value)
	}
	if headers[dlqOriginalTopicKey] != "coordinates" ||
		headers[dlqOriginalOffsetKey] != "0" ||
		headers[dlqOriginalTimestampKey] == "" {
		t.Fatalf("DLQ source headers = %v", headers)
	}
}
