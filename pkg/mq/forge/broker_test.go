package forge

import (
	"bytes"
	"fmt"
	"testing"
	"time"
)

func TestBrokerProduceConsume(t *testing.T) {
	dir := t.TempDir()

	b, err := NewBroker(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	p, err := b.NewProducer("events", WithBatchSize(1024))
	if err != nil {
		t.Fatal(err)
	}

	// Send 10 messages.
	for i := 0; i < 10; i++ {
		if err := p.Send([]byte("key"), []byte(fmt.Sprintf("msg-%d", i)), nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}

	// Consume all.
	c, err := b.NewConsumer("test-group", "events")
	if err != nil {
		t.Fatal(err)
	}

	records, err := c.Poll(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 10 {
		t.Fatalf("polled %d records, want 10", len(records))
	}
	if !bytes.Equal(records[0].Value, []byte("msg-0")) {
		t.Errorf("first record = %q, want %q", records[0].Value, "msg-0")
	}
	if !bytes.Equal(records[9].Value, []byte("msg-9")) {
		t.Errorf("last record = %q, want %q", records[9].Value, "msg-9")
	}

	// Commit offset.
	if err := c.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestBrokerRestartRecovery(t *testing.T) {
	dir := t.TempDir()

	// Phase 1: produce + consume partial + commit.
	b, err := NewBroker(dir)
	if err != nil {
		t.Fatal(err)
	}

	p, _ := b.NewProducer("orders", WithLinger(time.Millisecond))
	for i := 0; i < 5; i++ {
		p.Send(nil, []byte(fmt.Sprintf("order-%d", i)), nil)
	}
	p.Close()

	c, _ := b.NewConsumer("worker", "orders")
	records, _ := c.Poll(3) // consume only 3
	if len(records) != 3 {
		t.Fatalf("phase1: polled %d, want 3", len(records))
	}
	c.Commit()
	b.Close()

	// Phase 2: restart broker, consumer should resume from offset 3.
	b2, err := NewBroker(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b2.Close()

	c2, _ := b2.NewConsumer("worker", "orders")
	if c2.Offset() != 3 {
		t.Errorf("recovered offset = %d, want 3", c2.Offset())
	}

	remaining, _ := c2.Poll(100)
	if len(remaining) != 2 {
		t.Fatalf("phase2: polled %d, want 2", len(remaining))
	}
	if !bytes.Equal(remaining[0].Value, []byte("order-3")) {
		t.Errorf("first remaining = %q, want %q", remaining[0].Value, "order-3")
	}
}

func TestBrokerMultipleTopics(t *testing.T) {
	dir := t.TempDir()

	b, err := NewBroker(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	p1, _ := b.NewProducer("topic-a")
	p2, _ := b.NewProducer("topic-b")

	p1.Send(nil, []byte("a-msg"), nil)
	p1.Flush()
	p2.Send(nil, []byte("b-msg"), nil)
	p2.Flush()

	c1, _ := b.NewConsumer("g", "topic-a")
	c2, _ := b.NewConsumer("g", "topic-b")

	r1, _ := c1.Poll(10)
	r2, _ := c2.Poll(10)

	if len(r1) != 1 || !bytes.Equal(r1[0].Value, []byte("a-msg")) {
		t.Errorf("topic-a: got %d records", len(r1))
	}
	if len(r2) != 1 || !bytes.Equal(r2[0].Value, []byte("b-msg")) {
		t.Errorf("topic-b: got %d records", len(r2))
	}

	p1.Close()
	p2.Close()
}

func TestProducerAutoFlush(t *testing.T) {
	dir := t.TempDir()

	b, err := NewBroker(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	// Tiny batch size to trigger auto-flush.
	p, _ := b.NewProducer("auto", WithBatchSize(50))

	for i := 0; i < 5; i++ {
		p.Send(nil, []byte("this-is-a-reasonably-long-payload-to-trigger-flush"), nil)
	}
	// Small sleep to let linger flush.
	time.Sleep(20 * time.Millisecond)
	p.Close()

	c, _ := b.NewConsumer("g", "auto")
	records, _ := c.Poll(100)
	if len(records) != 5 {
		t.Errorf("auto-flush: got %d records, want 5", len(records))
	}
}

func TestConsumerSeek(t *testing.T) {
	dir := t.TempDir()

	b, err := NewBroker(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	p, _ := b.NewProducer("seek-test")
	for i := 0; i < 10; i++ {
		p.Send(nil, []byte(fmt.Sprintf("s-%d", i)), nil)
	}
	p.Close()

	c, _ := b.NewConsumer("g", "seek-test")

	// Seek to offset 7.
	c.Seek(7)
	records, _ := c.Poll(100)
	if len(records) != 3 {
		t.Fatalf("seek: got %d records, want 3", len(records))
	}
	if !bytes.Equal(records[0].Value, []byte("s-7")) {
		t.Errorf("seek first = %q, want %q", records[0].Value, "s-7")
	}
}
