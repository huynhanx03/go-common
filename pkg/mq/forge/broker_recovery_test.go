package forge

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestBrokerLoadsOnlyValidTopicDirectoriesAndReleasesOwnershipOnFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	topicsDir := filepath.Join(dir, "topics")
	if err := os.MkdirAll(topicsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(topicsDir, "README"), []byte("not a topic"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(topicsDir, "events"), 0o700); err != nil {
		t.Fatal(err)
	}

	broker, err := NewBroker(dir)
	if err != nil {
		t.Fatalf("NewBroker with regular file in topics directory: %v", err)
	}
	if got := broker.Topics(); len(got) != 1 || got[0] != "events" {
		t.Fatalf("loaded topics = %v, want [events]", got)
	}
	if err := broker.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	invalidTopic := filepath.Join(topicsDir, "invalid\nname")
	if err := os.Mkdir(invalidTopic, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := NewBroker(dir); err == nil {
		t.Fatal("NewBroker accepted an unsafe persisted topic directory")
	}
	if err := os.Remove(invalidTopic); err != nil {
		t.Fatal(err)
	}

	// A failed recovery must release the directory lease so an operator can fix
	// the invalid entry and start the broker again without restarting the process.
	recovered, err := NewBroker(dir)
	if err != nil {
		t.Fatalf("NewBroker after removing invalid topic: %v", err)
	}
	defer recovered.Close()
	if got := recovered.Topics(); len(got) != 1 || got[0] != "events" {
		t.Fatalf("recovered topics = %v, want [events]", got)
	}
}

func TestBrokerRejectsPersistedTopicsBeyondConfiguredLimit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	topicsDir := filepath.Join(dir, "topics")
	for _, topic := range []string{"events", "metrics"} {
		if err := os.MkdirAll(filepath.Join(topicsDir, topic), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := NewBroker(dir, WithMaxTopics(1)); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("NewBroker error = %v, want ErrResourceLimit", err)
	}

	broker, err := NewBroker(dir, WithMaxTopics(2))
	if err != nil {
		t.Fatalf("NewBroker at persisted-topic limit: %v", err)
	}
	defer broker.Close()
	if got := broker.Topics(); len(got) != 2 {
		t.Fatalf("loaded topic count = %d, want 2 (%v)", len(got), got)
	}
}

func TestBrokerReleasesDirectoryLeaseAfterOffsetStoreInitializationFails(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "broker")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	invalidOffsetsPath := filepath.Join(dir, "offsets")
	if err := os.WriteFile(invalidOffsetsPath, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewBroker(dir); err == nil {
		t.Fatal("NewBroker accepted a file where the offset directory belongs")
	}
	if err := os.Remove(invalidOffsetsPath); err != nil {
		t.Fatal(err)
	}

	broker, err := NewBroker(dir)
	if err != nil {
		t.Fatalf("NewBroker after fixing offset path: %v", err)
	}
	defer broker.Close()
}

func TestDLQConsumerFailureReleasesItsProducerCapacity(t *testing.T) {
	t.Parallel()

	broker, err := NewBroker(t.TempDir(), WithMaxProducers(1))
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	active, err := broker.NewConsumer("workers", "events")
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close()

	consumer, producer, err := broker.NewDLQConsumer("workers", "events")
	if !errors.Is(err, ErrConsumerBusy) || consumer != nil || producer != nil {
		t.Fatalf("NewDLQConsumer() = (%v, %v, %v), want busy without returned resources", consumer, producer, err)
	}

	replacement, err := broker.NewProducer("audit")
	if err != nil {
		t.Fatalf("producer capacity remained occupied after failed DLQ construction: %v", err)
	}
	defer replacement.Close()
}
