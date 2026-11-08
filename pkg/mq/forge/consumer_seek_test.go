package forge

import (
	"context"
	"testing"
)

func TestSeekToBeginningAndEndMoveOnlyTheFetchCursor(t *testing.T) {
	ctx := context.Background()
	broker, err := NewBroker(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	producer, err := broker.NewProducer("events", WithBatchSize(1))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"one", "two", "three"} {
		if err := producer.Send(nil, []byte(value), nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := producer.Close(); err != nil {
		t.Fatal(err)
	}
	consumer, err := broker.NewConsumer("workers", "events")
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()
	if _, err := consumer.Fetch(ctx, 3); err != nil {
		t.Fatal(err)
	}
	if consumer.Offset() != 3 {
		t.Fatalf("fetch cursor = %d", consumer.Offset())
	}
	consumer.SeekToBeginning()
	if consumer.Offset() != 0 || consumer.CommittedOffset() != 0 {
		t.Fatalf("seek to beginning changed cursor/commit to %d/%d", consumer.Offset(), consumer.CommittedOffset())
	}
	first, err := consumer.Fetch(ctx, 1)
	if err != nil || len(first) != 1 || string(first[0].Value) != "one" {
		t.Fatalf("first replay = (%+v, %v)", first, err)
	}
	consumer.SeekToEnd()
	if consumer.Offset() != 3 || consumer.CommittedOffset() != 0 {
		t.Fatalf("seek to end changed cursor/commit to %d/%d", consumer.Offset(), consumer.CommittedOffset())
	}
	if remaining, err := consumer.Fetch(ctx, 1); err != nil || len(remaining) != 0 {
		t.Fatalf("fetch after seek to end = (%+v, %v)", remaining, err)
	}
}
