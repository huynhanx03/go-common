package forge

import (
	"fmt"
	"testing"
	"time"
)

func TestBrokerAutoRetentionKeepsActiveSegment(t *testing.T) {
	broker, err := NewBroker(
		t.TempDir(),
		WithRetentionMode(RetainByAgeAndSize),
		WithRetentionTime(50*time.Millisecond),
		WithRetentionInterval(30*time.Millisecond),
		WithMaxSegmentBytes(200),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()

	producer, err := broker.NewProducer("retention-test", WithLinger(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 50; index++ {
		if err := producer.Send(
			[]byte(fmt.Sprintf("key-%d", index)),
			[]byte("value"),
			nil,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := producer.Flush(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)

	topic := broker.getTopic("retention-test")
	if topic == nil {
		t.Fatal("topic not found")
	}
	topic.log.mu.RLock()
	segmentCount := len(topic.log.segments)
	active := topic.log.activeSegment
	topic.log.mu.RUnlock()
	if segmentCount == 0 || active == nil {
		t.Fatalf("retention removed active segment: count=%d active=%v", segmentCount, active)
	}
}

func TestBrokerCloseStopsRetention(t *testing.T) {
	broker, err := NewBroker(t.TempDir(), WithRetentionInterval(10*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- broker.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("broker close did not stop retention")
	}
}
