package forge

import (
	"bytes"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestConcurrentProducers verifies safety with multiple concurrent producers.
func TestConcurrentProducers(t *testing.T) {
	dir := t.TempDir()
	b, err := NewBroker(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	const numProducers = 4
	const msgsPerProducer = 500

	var wg sync.WaitGroup
	var errCount atomic.Int32

	for p := 0; p < numProducers; p++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			prod, err := b.NewProducer("concurrent", WithBatchSize(4096))
			if err != nil {
				errCount.Add(1)
				return
			}
			defer prod.Close()

			for i := 0; i < msgsPerProducer; i++ {
				err := prod.Send(
					[]byte(fmt.Sprintf("p%d", id)),
					[]byte(fmt.Sprintf("msg-%d-%d", id, i)),
					nil,
				)
				if err != nil {
					errCount.Add(1)
					return
				}
			}
		}(p)
	}

	wg.Wait()

	if errCount.Load() > 0 {
		t.Fatalf("%d producer errors", errCount.Load())
	}

	// Consume all messages.
	c, _ := b.NewConsumer("verify", "concurrent")
	var total int
	for {
		records, err := c.Poll(1000)
		if err != nil {
			t.Fatal(err)
		}
		if len(records) == 0 {
			break
		}
		total += len(records)
	}

	expected := numProducers * msgsPerProducer
	if total != expected {
		t.Errorf("consumed %d records, want %d", total, expected)
	}
}

// TestConcurrentProducerConsumer runs producers and consumers simultaneously.
func TestConcurrentProducerConsumer(t *testing.T) {
	dir := t.TempDir()
	b, err := NewBroker(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	const totalMsgs = 2000
	var produced atomic.Int32
	var consumed atomic.Int32

	// Producer goroutine.
	go func() {
		p, _ := b.NewProducer("stream", WithBatchSize(2048), WithLinger(time.Millisecond))
		defer p.Close()
		for i := 0; i < totalMsgs; i++ {
			p.Send(nil, []byte(fmt.Sprintf("s-%d", i)), nil)
			produced.Add(1)
		}
	}()

	// Consumer goroutine — polls until all messages are consumed.
	c, _ := b.NewConsumer("reader", "stream")
	deadline := time.After(5 * time.Second)
	for consumed.Load() < totalMsgs {
		select {
		case <-deadline:
			t.Fatalf("timeout: produced=%d consumed=%d", produced.Load(), consumed.Load())
		default:
		}

		records, err := c.Poll(100)
		if err != nil {
			t.Fatal(err)
		}
		consumed.Add(int32(len(records)))
		if len(records) == 0 {
			time.Sleep(time.Millisecond)
		}
	}

	if consumed.Load() != totalMsgs {
		t.Errorf("consumed %d, want %d", consumed.Load(), totalMsgs)
	}
}

// TestConsumerCommitRestart stress-tests commit + restart cycle.
func TestConsumerCommitRestart(t *testing.T) {
	dir := t.TempDir()

	// Produce 100 messages.
	b, _ := NewBroker(dir)
	p, _ := b.NewProducer("commit-stress")
	for i := 0; i < 100; i++ {
		p.Send(nil, []byte(fmt.Sprintf("r-%d", i)), nil)
	}
	p.Close()
	b.Close()

	// Consume in batches of 10, commit, restart each time.
	for batch := 0; batch < 10; batch++ {
		b, err := NewBroker(dir)
		if err != nil {
			t.Fatal(err)
		}

		c, _ := b.NewConsumer("restarter", "commit-stress")
		expectedOffset := uint64(batch * 10)
		if c.Offset() != expectedOffset {
			t.Fatalf("batch %d: offset = %d, want %d", batch, c.Offset(), expectedOffset)
		}

		records, _ := c.Poll(10)
		if len(records) != 10 {
			t.Fatalf("batch %d: got %d records, want 10", batch, len(records))
		}

		expected := fmt.Sprintf("r-%d", batch*10)
		if !bytes.Equal(records[0].Value, []byte(expected)) {
			t.Errorf("batch %d: first = %q, want %q", batch, records[0].Value, expected)
		}

		c.Commit()
		b.Close()
	}
}
