package forge

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

const (
	crashHelperModeEnv = "GO_COMMON_FORGE_CRASH_HELPER"
	crashHelperDirEnv  = "GO_COMMON_FORGE_CRASH_DIR"
)

func TestFsyncAcknowledgedRecordSurvivesAbruptProcessExit(t *testing.T) {
	if mode := os.Getenv(crashHelperModeEnv); mode != "" {
		if mode == "produce" {
			runCrashProducerHelper()
		}
		return
	}

	dir := t.TempDir()
	runCrashHelper(t, dir, "produce")

	broker, err := NewBroker(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	consumer, err := broker.NewConsumer("workers", "events")
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()
	deliveries, err := consumer.Fetch(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 1 || string(deliveries[0].Value) != "fsynced-before-crash" {
		t.Fatalf("recovered deliveries = %#v", deliveries)
	}
}

func TestUncommittedDeliveryRedeliversAfterAbruptProcessExit(t *testing.T) {
	if mode := os.Getenv(crashHelperModeEnv); mode != "" {
		if mode == "consume-without-commit" {
			runCrashConsumerHelper()
		}
		return
	}

	dir := t.TempDir()
	broker, err := NewBroker(dir)
	if err != nil {
		t.Fatal(err)
	}
	producer, err := broker.NewProducer("events", WithBatchSize(1))
	if err != nil {
		t.Fatal(err)
	}
	if err := producer.SendContext(
		context.Background(),
		nil,
		[]byte("must-redeliver"),
		nil,
		AckFsync,
	); err != nil {
		t.Fatal(err)
	}
	if err := broker.Close(); err != nil {
		t.Fatal(err)
	}

	runCrashHelper(t, dir, "consume-without-commit")

	restarted, err := NewBroker(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	consumer, err := restarted.NewConsumer("workers", "events")
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()
	deliveries, err := consumer.Fetch(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 1 ||
		deliveries[0].Offset != 0 ||
		string(deliveries[0].Value) != "must-redeliver" {
		t.Fatalf("redeliveries = %#v", deliveries)
	}
}

func runCrashHelper(t *testing.T, dir, mode string) {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=TestFsyncAcknowledgedRecordSurvivesAbruptProcessExit|TestUncommittedDeliveryRedeliversAfterAbruptProcessExit")
	command.Env = append(
		os.Environ(),
		crashHelperModeEnv+"="+mode,
		crashHelperDirEnv+"="+dir,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("crash helper %q: %v\n%s", mode, err, output)
	}
}

func runCrashProducerHelper() {
	broker, err := NewBroker(os.Getenv(crashHelperDirEnv))
	if err != nil {
		os.Exit(11)
	}
	producer, err := broker.NewProducer(
		"events",
		WithBatchSize(1<<20),
		WithLinger(time.Hour),
	)
	if err != nil {
		os.Exit(12)
	}
	if err := producer.SendContext(
		context.Background(),
		nil,
		[]byte("fsynced-before-crash"),
		nil,
		AckFsync,
	); err != nil {
		os.Exit(13)
	}
	os.Exit(0)
}

func runCrashConsumerHelper() {
	broker, err := NewBroker(os.Getenv(crashHelperDirEnv))
	if err != nil {
		os.Exit(21)
	}
	consumer, err := broker.NewConsumer("workers", "events")
	if err != nil {
		os.Exit(22)
	}
	deliveries, err := consumer.Fetch(context.Background(), 1)
	if err != nil || len(deliveries) != 1 {
		os.Exit(23)
	}
	os.Exit(0)
}
