package forge

import (
	"errors"
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

func TestMaximumSegmentAgeRollsToANewSegment(t *testing.T) {
	broker, err := NewBroker(t.TempDir(), WithMaxSegmentAge(time.Nanosecond), WithoutAutomaticRetention())
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	producer, err := broker.NewProducer("age-limit", WithBatchSize(1))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"first", "second"} {
		if err := producer.Send(nil, []byte(value), nil); err != nil {
			t.Fatal(err)
		}
		if err := producer.Flush(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	topic := broker.getTopic("age-limit")
	topic.log.mu.RLock()
	segments := len(topic.log.segments)
	topic.log.mu.RUnlock()
	if segments < 2 {
		t.Fatalf("segment age limit left %d segment; want a rolled segment", segments)
	}
}

func TestRetentionErrorsReachConfiguredCallbackWithoutCrashingLoop(t *testing.T) {
	failure := errors.New("retention storage failure")
	t.Run("callback receives enforcement failure", func(t *testing.T) {
		reported := make(chan error, 1)
		broker, err := NewBroker(t.TempDir(),
			WithRetentionMode(RetainByAgeAndSize),
			WithOnRetentionError(func(err error) { reported <- err }),
			WithoutAutomaticRetention(),
		)
		if err != nil {
			t.Fatal(err)
		}
		defer broker.Close()
		producer, err := broker.NewProducer("retention-errors", WithBatchSize(1))
		if err != nil {
			t.Fatal(err)
		}
		if err := producer.Send(nil, []byte("record"), nil); err != nil {
			t.Fatal(err)
		}
		topic := broker.getTopic("retention-errors")
		topic.log.mu.Lock()
		topic.log.storageErr = failure
		topic.log.mu.Unlock()

		broker.runRetention()
		select {
		case err := <-reported:
			if !errors.Is(err, failure) {
				t.Fatalf("reported retention error = %v", err)
			}
		default:
			t.Fatal("retention failure was not reported")
		}
	})

	t.Run("callback panic is contained", func(t *testing.T) {
		broker, err := NewBroker(t.TempDir(),
			WithRetentionMode(RetainByAgeAndSize),
			WithOnRetentionError(func(error) { panic("observer bug") }),
			WithoutAutomaticRetention(),
		)
		if err != nil {
			t.Fatal(err)
		}
		defer broker.Close()
		producer, err := broker.NewProducer("retention-panic", WithBatchSize(1))
		if err != nil {
			t.Fatal(err)
		}
		if err := producer.Send(nil, []byte("record"), nil); err != nil {
			t.Fatal(err)
		}
		topic := broker.getTopic("retention-panic")
		topic.log.mu.Lock()
		topic.log.storageErr = failure
		topic.log.mu.Unlock()

		broker.runRetention()
	})
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
