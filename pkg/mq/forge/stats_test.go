package forge

import (
	"context"
	"errors"
	"testing"
)

func TestBrokerHealthAndLagFailClosedWhenTopicStorageIsUnavailable(t *testing.T) {
	for _, test := range []struct {
		name    string
		prepare func(*Topic) error
		want    error
	}{
		{
			name:    "topic log closed independently",
			prepare: func(topic *Topic) error { return topic.Close() },
			want:    ErrClosed,
		},
		{
			name: "topic storage poisoned after an ambiguous write",
			prepare: func(topic *Topic) error {
				topic.log.mu.Lock()
				topic.log.storageErr = ErrStorageUnavailable
				topic.log.mu.Unlock()
				return nil
			},
			want: ErrStorageUnavailable,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			broker, err := NewBroker(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer broker.Close()
			if _, err := broker.NewProducer("events"); err != nil {
				t.Fatal(err)
			}
			if err := test.prepare(broker.topics["events"]); err != nil {
				t.Fatal(err)
			}

			if err := broker.Check(context.Background()); !errors.Is(err, test.want) {
				t.Fatalf("Check() error = %v, want %v", err, test.want)
			}
			if _, err := broker.Stats(context.Background()); !errors.Is(err, test.want) {
				t.Fatalf("Stats() error = %v, want %v", err, test.want)
			}
			if _, err := broker.ConsumerLag(context.Background(), "workers", "events"); !errors.Is(err, test.want) {
				t.Fatalf("ConsumerLag() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestBrokerStatsAreSortedAndLagRejectsUnknownOrImpossibleOffsets(t *testing.T) {
	t.Parallel()

	broker, err := NewBroker(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	for _, topic := range []string{"z-events", "a-events"} {
		if _, err := broker.NewProducer(topic); err != nil {
			t.Fatal(err)
		}
	}
	stats, err := broker.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 2 || stats[0].Topic != "a-events" || stats[1].Topic != "z-events" {
		t.Fatalf("Stats() topics = %+v, want deterministic alphabetical order", stats)
	}
	if _, err := broker.ConsumerLag(context.Background(), "workers", "missing"); !errors.Is(err, ErrOffsetNotFound) {
		t.Fatalf("ConsumerLag(missing topic) error = %v, want ErrOffsetNotFound", err)
	}
	if err := broker.offsetStore.Commit("workers", "a-events", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := broker.ConsumerLag(context.Background(), "workers", "a-events"); !errors.Is(err, ErrInvalidOffsetCommit) {
		t.Fatalf("ConsumerLag(future offset) error = %v, want ErrInvalidOffsetCommit", err)
	}
	if _, err := broker.ConsumerLag(context.Background(), "bad/group", "a-events"); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("ConsumerLag(invalid identity) error = %v, want ErrInvalidConfig", err)
	}
}

func TestBrokerHealthPropagatesCallerCancellation(t *testing.T) {
	t.Parallel()

	broker, err := NewBroker(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := broker.Check(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Check(canceled) error = %v, want context.Canceled", err)
	}
	if _, err := broker.Stats(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Stats(canceled) error = %v, want context.Canceled", err)
	}
	if _, err := broker.ConsumerLag(ctx, "workers", "events"); !errors.Is(err, context.Canceled) {
		t.Fatalf("ConsumerLag(canceled) error = %v, want context.Canceled", err)
	}
}
