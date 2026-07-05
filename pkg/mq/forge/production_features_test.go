package forge

import (
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

func TestBackpressureUnlimitedByDefault(t *testing.T) {
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

	// Should never return ErrBackpressure with default config (MaxPendingBytes=0).
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
	p := NewProducer(log,
		WithBatchSize(1), // flush every message
		WithOnError(func(err error) {
			errorCount.Add(1)
		}),
	)

	// Send a message — should flush immediately.
	if err := p.Send(nil, []byte("hello"), nil); err != nil {
		t.Fatal(err)
	}

	p.Close()
	log.Close()

	// Close the log, then try to flush — should trigger OnError.
	log2, _ := NewCommitLog(dir)
	log2.Close()

	p2 := NewProducer(log2,
		WithBatchSize(1),
		WithOnError(func(err error) {
			errorCount.Add(1)
		}),
	)
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
		WithShutdownTimeout(5*time.Second),
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

	if flushCount.Load() < 2 {
		t.Fatalf("expected >= 2 flush hooks, got %d", flushCount.Load())
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

func TestNackWithoutDLQSilentlyDrops(t *testing.T) {
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

	// Nack without DLQ — should return nil (silent drop).
	if err := c.Nack(records[0]); err != nil {
		t.Fatalf("Nack without DLQ should return nil, got: %v", err)
	}
}
