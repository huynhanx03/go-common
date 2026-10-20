package forge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBrokerDirectoryHasExclusiveProcessOwnership(t *testing.T) {
	dir := t.TempDir()
	firstBroker, err := NewBroker(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer firstBroker.Close()
	if _, err := NewBroker(dir); !errors.Is(err, ErrBrokerBusy) {
		t.Fatalf("second broker error = %v, want ErrBrokerBusy", err)
	}
	if err := firstBroker.Close(); err != nil {
		t.Fatal(err)
	}
	replacement, err := NewBroker(dir)
	if err != nil {
		t.Fatalf("broker after release: %v", err)
	}
	if err := replacement.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestConstructorsRejectInvalidOptions(t *testing.T) {
	for name, option := range map[string]Option{
		"segment bytes":      WithMaxSegmentBytes(0),
		"storage bytes":      WithMaxStorageBytes(-1),
		"message bytes":      WithMaxMessageSize(0),
		"fsync":              WithFsyncEvery(-1),
		"retention time":     WithRetentionTime(0),
		"retention bytes":    WithRetentionBytes(0),
		"retention interval": WithRetentionInterval(0),
		"retention mode":     WithRetentionMode(RetentionMode(255)),
		"index interval":     WithIndexInterval(MaximumIndexInterval + 1),
		"topics":             WithMaxTopics(0),
		"producers":          WithMaxProducers(0),
		"consumers":          WithMaxConsumers(0),
		"consumer groups":    WithMaxConsumerGroups(0),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewBroker(t.TempDir(), option); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("NewBroker() error = %v, want ErrInvalidConfig", err)
			}
		})
	}

	log, err := NewCommitLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	for name, option := range map[string]ProducerOption{
		"batch":       WithBatchSize(0),
		"linger":      WithLinger(0),
		"compression": WithCompression(255),
		"clock":       WithClock(nil),
		"pending":     WithMaxPendingBytes(0),
	} {
		t.Run("producer "+name, func(t *testing.T) {
			if _, err := NewProducer(log, option); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("NewProducer() error = %v, want ErrInvalidConfig", err)
			}
		})
	}
	store, err := NewOffsetStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewConsumer(log, "group", "topic", store, WithDLQ(nil)); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("NewConsumer() error = %v, want ErrInvalidConfig", err)
	}
	if _, registered, err := store.MinimumOffset("topic"); err != nil || registered {
		t.Fatalf("invalid consumer registered retention state: registered=%v err=%v", registered, err)
	}
	if _, err := NewBroker(t.TempDir(), WithIndexInterval(1)); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("unsafe index-memory budget error = %v, want ErrInvalidConfig", err)
	}
}

func TestBrokerEnforcesConfiguredResourceBounds(t *testing.T) {
	t.Run("topics", func(t *testing.T) {
		broker, err := NewBroker(t.TempDir(), WithMaxTopics(1))
		if err != nil {
			t.Fatal(err)
		}
		defer broker.Close()
		producer, err := broker.NewProducer("first")
		if err != nil {
			t.Fatal(err)
		}
		defer producer.Close()
		if _, err := broker.NewProducer("second"); !errors.Is(err, ErrResourceLimit) {
			t.Fatalf("second topic error = %v, want ErrResourceLimit", err)
		}
	})

	t.Run("producers", func(t *testing.T) {
		broker, err := NewBroker(t.TempDir(), WithMaxProducers(1))
		if err != nil {
			t.Fatal(err)
		}
		defer broker.Close()
		producer, err := broker.NewProducer("events")
		if err != nil {
			t.Fatal(err)
		}
		defer producer.Close()
		if _, err := broker.NewProducer("unused"); !errors.Is(err, ErrResourceLimit) {
			t.Fatalf("second producer error = %v, want ErrResourceLimit", err)
		}
		if topics := broker.Topics(); len(topics) != 1 || topics[0] != "events" {
			t.Fatalf("rejected producer created a topic: %v", topics)
		}
	})

	t.Run("consumers", func(t *testing.T) {
		broker, err := NewBroker(t.TempDir(), WithMaxConsumers(1))
		if err != nil {
			t.Fatal(err)
		}
		defer broker.Close()
		consumer, err := broker.NewConsumer("first", "events")
		if err != nil {
			t.Fatal(err)
		}
		defer consumer.Close()
		if _, err := broker.NewConsumer("second", "events"); !errors.Is(err, ErrResourceLimit) {
			t.Fatalf("second consumer error = %v, want ErrResourceLimit", err)
		}
	})

	t.Run("durable consumer groups", func(t *testing.T) {
		broker, err := NewBroker(t.TempDir(), WithMaxConsumerGroups(1))
		if err != nil {
			t.Fatal(err)
		}
		defer broker.Close()
		consumer, err := broker.NewConsumer("first", "events")
		if err != nil {
			t.Fatal(err)
		}
		if err := consumer.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := broker.NewConsumer("second", "events"); !errors.Is(err, ErrResourceLimit) {
			t.Fatalf("second group error = %v, want ErrResourceLimit", err)
		}
	})
}

func TestBrokerRejectsInvalidChildOptionsBeforeCreatingTopics(t *testing.T) {
	broker, err := NewBroker(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	if _, err := broker.NewProducer("invalid-producer", WithBatchSize(0)); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("NewProducer invalid option error = %v", err)
	}
	if _, err := broker.NewConsumer("group", "invalid-consumer", WithDLQ(nil)); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("NewConsumer invalid option error = %v", err)
	}
	if topics := broker.Topics(); len(topics) != 0 {
		t.Fatalf("invalid child construction created topics: %v", topics)
	}
}

func TestConstructorsContainOptionPanicsBeforeOwningResources(t *testing.T) {
	brokerDirectory := filepath.Join(t.TempDir(), "broker")
	if _, err := NewBroker(brokerDirectory, func(*Config) error {
		panic("must not escape")
	}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("NewBroker panic error = %v, want ErrInvalidConfig", err)
	}
	if _, err := os.Stat(brokerDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("panicking broker option created resources: %v", err)
	}

	log, err := NewCommitLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	if _, err := NewProducer(log, func(*ProducerConfig) error {
		panic("must not escape")
	}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("NewProducer panic error = %v, want ErrInvalidConfig", err)
	}

	broker, err := NewBroker(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	if _, err := broker.NewConsumer("group", "topic", func(*consumerConfig) error {
		panic("must not escape")
	}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("NewConsumer panic error = %v, want ErrInvalidConfig", err)
	}
	if topics := broker.Topics(); len(topics) != 0 {
		t.Fatalf("panicking consumer option created topics: %v", topics)
	}
}

func TestConsumerGroupHasExclusiveOwnership(t *testing.T) {
	broker, err := NewBroker(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()

	first, err := broker.NewConsumer("workers", "events")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	if _, err := broker.NewConsumer("workers", "events"); !errors.Is(err, ErrConsumerBusy) {
		t.Fatalf("second consumer error = %v, want ErrConsumerBusy", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := broker.NewConsumer("workers", "events")
	if err != nil {
		t.Fatalf("consumer after release: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFetchUsesExplicitMonotonicPartialCommit(t *testing.T) {
	dir := t.TempDir()
	broker, err := NewBroker(dir)
	if err != nil {
		t.Fatal(err)
	}
	producer, err := broker.NewProducer("events", WithBatchSize(1<<20), WithLinger(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 3; index++ {
		if err := producer.SendContext(
			context.Background(),
			nil,
			[]byte(strconv.Itoa(index)),
			nil,
			AckMemory,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := producer.Flush(); err != nil {
		t.Fatal(err)
	}

	consumer, err := broker.NewConsumer("workers", "events")
	if err != nil {
		t.Fatal(err)
	}
	deliveries, err := consumer.Fetch(context.Background(), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 3 {
		t.Fatalf("deliveries = %d, want 3", len(deliveries))
	}
	for index, delivery := range deliveries {
		if delivery.Offset != uint64(index) || string(delivery.Value) != strconv.Itoa(index) {
			t.Fatalf("delivery %d = %#v", index, delivery)
		}
	}

	if err := consumer.CommitOffset(context.Background(), deliveries[0].Offset+1); err != nil {
		t.Fatalf("partial CommitOffset: %v", err)
	}
	if consumer.CommittedOffset() != 1 {
		t.Fatalf("committed offset = %d, want 1", consumer.CommittedOffset())
	}
	if err := consumer.CommitOffset(context.Background(), 0); !errors.Is(err, ErrInvalidOffsetCommit) {
		t.Fatalf("backward commit error = %v, want ErrInvalidOffsetCommit", err)
	}
	if err := consumer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := broker.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := NewBroker(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	redeliveryConsumer, err := restarted.NewConsumer("workers", "events")
	if err != nil {
		t.Fatal(err)
	}
	defer redeliveryConsumer.Close()
	redeliveries, err := redeliveryConsumer.Fetch(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(redeliveries) != 2 || redeliveries[0].Offset != 1 || redeliveries[1].Offset != 2 {
		t.Fatalf("redeliveries = %#v, want offsets [1,2]", redeliveries)
	}
}

func TestFsyncAcknowledgmentLeavesNoPendingRecords(t *testing.T) {
	broker, err := NewBroker(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	producer, err := broker.NewProducer(
		"durable-jobs",
		WithBatchSize(1<<20),
		WithLinger(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := producer.SendContext(
		context.Background(),
		nil,
		[]byte("job"),
		nil,
		AckFsync,
	); err != nil {
		t.Fatal(err)
	}
	if pending := producer.PendingRecords(); pending != 0 {
		t.Fatalf("pending records after fsync acknowledgment = %d, want 0", pending)
	}
}

func TestMemoryAcknowledgmentDoesNotPerformFilesystemIO(t *testing.T) {
	writeStarted := make(chan struct{})
	releaseWrite := make(chan struct{})
	var startOnce sync.Once
	ops := defaultFileOperations()
	ops.write = func(file *os.File, data []byte) (int, error) {
		startOnce.Do(func() { close(writeStarted) })
		<-releaseWrite
		return file.Write(data)
	}

	log, err := NewCommitLog(t.TempDir(), withFileOperations(ops))
	if err != nil {
		t.Fatal(err)
	}
	producer, err := NewProducer(log, WithBatchSize(1), WithLinger(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		close(releaseWrite)
		_ = producer.Close()
		_ = log.Close()
	}()

	result := make(chan error, 1)
	go func() {
		result <- producer.SendContext(
			context.Background(),
			nil,
			[]byte("admission-only"),
			nil,
			AckMemory,
		)
	}()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-writeStarted:
		select {
		case err := <-result:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(50 * time.Millisecond):
			t.Fatal("AckMemory waited for filesystem I/O")
		}
	case <-time.After(time.Second):
		t.Fatal("AckMemory did not return")
	}
}

func TestProducerRejectsAggregateBatchOverflowBeforeAdmission(t *testing.T) {
	t.Parallel()

	commitLog, err := NewCommitLog(t.TempDir())
	if err != nil {
		t.Fatalf("NewCommitLog: %v", err)
	}
	producer, err := NewProducer(
		commitLog,
		WithBatchSize(maxEncodedBatchBytes-batchHeaderSize),
		WithLinger(time.Hour),
	)
	if err != nil {
		_ = commitLog.Close()
		t.Fatalf("NewProducer: %v", err)
	}

	<-producer.flushGate
	gateHeld := true
	defer func() {
		if gateHeld {
			producer.flushGate <- struct{}{}
		}
		_ = producer.Close()
		_ = commitLog.Close()
	}()

	payload := make([]byte, (maxEncodedBatchBytes-batchHeaderSize)/16-128)
	for index := 0; index < 16; index++ {
		if err := producer.Send(nil, payload, nil); err != nil {
			t.Fatalf("Send(%d): %v", index, err)
		}
	}
	if err := producer.Send(nil, payload, nil); !errors.Is(err, ErrBackpressure) {
		t.Fatalf("overflow Send error = %v, want ErrBackpressure", err)
	}

	producer.flushGate <- struct{}{}
	gateHeld = false
	if err := producer.Flush(); err != nil {
		t.Fatalf("Flush bounded batch: %v", err)
	}
	if got := producer.PendingRecords(); got != 0 {
		t.Fatalf("PendingRecords = %d, want 0", got)
	}
}

func TestLingerIsMaximumBatchDelayUnderContinuousAdmission(t *testing.T) {
	log, err := NewCommitLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	const linger = 30 * time.Millisecond
	producer, err := NewProducer(log, WithBatchSize(1<<20), WithLinger(linger))
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()

	deadline := time.Now().Add(4 * linger)
	for sequence := 0; time.Now().Before(deadline); sequence++ {
		if err := producer.Send(nil, []byte(strconv.Itoa(sequence)), nil); err != nil {
			t.Fatal(err)
		}
		if log.NewestOffset() > 0 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("continuous admissions postponed the first flush beyond linger")
}

func TestMetricsCallbacksRunOutsideQueueSynchronization(t *testing.T) {
	broker, err := NewBroker(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()

	var producer *Producer
	producerMetrics := &MetricsHook{OnFlush: func(string, int, int) {
		_ = producer.Flush()
	}}
	producer, err = broker.NewProducer(
		"events",
		WithBatchSize(1),
		WithMetrics(producerMetrics),
	)
	if err != nil {
		t.Fatal(err)
	}
	producerResult := make(chan error, 1)
	go func() {
		producerResult <- producer.SendContext(
			context.Background(), nil, []byte("value"), nil, AckAppend,
		)
	}()
	select {
	case err := <-producerResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("producer metrics callback deadlocked on a reentrant operation")
	}

	var consumer *Consumer
	consumerMetrics := &MetricsHook{OnPoll: func(string, string, int) {
		_ = consumer.Offset()
	}}
	consumer, err = broker.NewConsumer("projector", "events", WithConsumerMetrics(consumerMetrics))
	if err != nil {
		t.Fatal(err)
	}
	consumerResult := make(chan error, 1)
	go func() {
		_, fetchErr := consumer.Fetch(context.Background(), 1)
		consumerResult <- fetchErr
	}()
	select {
	case err := <-consumerResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("consumer metrics callback deadlocked on a reentrant operation")
	}
}

func TestNackWithoutDLQReturnsTypedError(t *testing.T) {
	broker, err := NewBroker(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	consumer, err := broker.NewConsumer("projector", "events")
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()
	if err := consumer.Nack(Record{Value: []byte("failed")}); !errors.Is(err, ErrDLQNotConfigured) {
		t.Fatalf("Nack() error = %v, want ErrDLQNotConfigured", err)
	}
}

func TestFsyncAcknowledgmentPersistsSegmentDirectoryEntries(t *testing.T) {
	dir := t.TempDir()
	defaultOps := defaultFileOperations()
	var mu sync.Mutex
	directorySyncs := make(map[string]int)
	ops := defaultOps
	ops.syncDir = func(path string) error {
		if err := defaultOps.syncDir(path); err != nil {
			return err
		}
		mu.Lock()
		directorySyncs[path]++
		mu.Unlock()
		return nil
	}
	log, err := NewCommitLog(
		dir,
		WithMaxSegmentBytes(96),
		WithMaxMessageSize(64),
		withFileOperations(ops),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	producer, err := NewProducer(log, WithBatchSize(1), WithLinger(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()

	for sequence := 0; sequence < 3; sequence++ {
		if err := producer.SendContext(
			context.Background(),
			nil,
			[]byte("payload-that-rolls-the-segment"),
			nil,
			AckFsync,
		); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	got := directorySyncs[dir]
	mu.Unlock()
	if got < 2 {
		t.Fatalf("segment directory fsync count = %d, want at least 2 for create and roll", got)
	}
}

func TestRetentionCrashWindowLeavesRecoverableAuthoritativeLog(t *testing.T) {
	var failLogRemoval atomic.Bool
	defaultOps := defaultFileOperations()
	ops := defaultOps
	ops.remove = func(path string) error {
		if filepath.Ext(path) == extLog && failLogRemoval.CompareAndSwap(true, false) {
			return errors.New("injected log removal failure")
		}
		return defaultOps.remove(path)
	}
	dir := t.TempDir()
	log, err := NewCommitLog(
		dir,
		WithMaxSegmentBytes(96),
		WithMaxMessageSize(64),
		withFileOperations(ops),
	)
	if err != nil {
		t.Fatal(err)
	}
	for sequence := 0; sequence < 4; sequence++ {
		if _, err := log.Append(&RecordBatch{
			Records: []Record{{Value: []byte("payload-that-rolls-the-segment")}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if len(log.segments) < 2 {
		t.Fatal("fixture did not roll a segment")
	}
	first := log.segments[0]
	firstLog := first.logFile.Name()
	firstIndex := first.index.file.Name()
	failLogRemoval.Store(true)
	if err := log.DeleteBefore(first.nextOffset); err == nil {
		t.Fatal("DeleteBefore() succeeded despite injected log removal failure")
	}
	if _, err := os.Stat(firstLog); err != nil {
		t.Fatalf("authoritative log was lost: %v", err)
	}
	if _, err := os.Stat(firstIndex); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rebuildable index remains or unexpected error: %v", err)
	}
	if _, err := log.Append(&RecordBatch{Records: []Record{{Value: []byte("must-not-append")}}}); err == nil {
		t.Fatal("commit log remained writable after an incomplete retention deletion")
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}

	recovered, err := NewCommitLog(dir)
	if err != nil {
		t.Fatalf("reopen after retention crash window: %v", err)
	}
	defer recovered.Close()
	if recovered.NewestOffset() != 4 {
		t.Fatalf("recovered newest offset = %d, want 4", recovered.NewestOffset())
	}
}

func TestRetentionDirectorySyncFailurePoisonsCommitLog(t *testing.T) {
	dir := t.TempDir()
	injected := errors.New("injected retention directory sync failure")
	var failDirectorySync atomic.Bool
	defaultOps := defaultFileOperations()
	ops := defaultOps
	ops.syncDir = func(path string) error {
		if failDirectorySync.Load() && filepath.Clean(path) == filepath.Clean(dir) {
			return injected
		}
		return defaultOps.syncDir(path)
	}
	log, err := NewCommitLog(
		dir,
		WithMaxSegmentBytes(96),
		WithMaxMessageSize(64),
		withFileOperations(ops),
	)
	if err != nil {
		t.Fatal(err)
	}
	for sequence := 0; sequence < 4; sequence++ {
		if _, err := log.Append(&RecordBatch{
			Records: []Record{{Value: []byte("payload-that-rolls-the-segment")}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if len(log.segments) < 2 {
		t.Fatal("fixture did not roll a segment")
	}
	deleteBefore := log.segments[0].nextOffset
	failDirectorySync.Store(true)
	if err := log.DeleteBefore(deleteBefore); !errors.Is(err, ErrStorageUnavailable) || !errors.Is(err, injected) {
		t.Fatalf("DeleteBefore() error = %v, want terminal directory-sync error", err)
	}
	if _, err := log.Append(&RecordBatch{Records: []Record{{Value: []byte("must-not-append")}}}); !errors.Is(err, ErrStorageUnavailable) {
		t.Fatalf("Append() after retention sync failure = %v, want ErrStorageUnavailable", err)
	}
	if _, err := log.Read(log.OldestOffset(), 128); !errors.Is(err, ErrStorageUnavailable) {
		t.Fatalf("Read() after retention sync failure = %v, want ErrStorageUnavailable", err)
	}
	if err := log.Sync(); !errors.Is(err, ErrStorageUnavailable) {
		t.Fatalf("Sync() after retention sync failure = %v, want ErrStorageUnavailable", err)
	}
	failDirectorySync.Store(false)
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestConsumerRejectsCommittedOffsetPastLogEnd(t *testing.T) {
	dir := t.TempDir()
	log, err := NewCommitLog(filepath.Join(dir, "topic"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	store, err := NewOffsetStore(filepath.Join(dir, "offsets"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Commit("group", "events", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := NewConsumer(log, "group", "events", store); !errors.Is(err, ErrInvalidOffsetCommit) {
		t.Fatalf("NewConsumer() error = %v, want ErrInvalidOffsetCommit", err)
	}
}

func TestOffsetStoreRejectsNonCanonicalFileSize(t *testing.T) {
	store, err := NewOffsetStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	groupDirectory := filepath.Join(store.dir, "group")
	if err := os.MkdirAll(groupDirectory, dirPerm); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		store.path("group", "events"),
		make([]byte, offsetByteSize+1),
		filePerm,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load("group", "events"); !errors.Is(err, ErrCorruptRecord) {
		t.Fatalf("Load noncanonical offset error = %v, want ErrCorruptRecord", err)
	}
}

func TestStoragePermissionsExcludeGroupAndOtherUsers(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "forge")
	broker, err := NewBroker(dir)
	if err != nil {
		t.Fatal(err)
	}
	producer, err := broker.NewProducer("events", WithBatchSize(1))
	if err != nil {
		t.Fatal(err)
	}
	if err := producer.SendContext(
		context.Background(), nil, []byte("value"), nil, AckFsync,
	); err != nil {
		t.Fatal(err)
	}
	if err := broker.Close(); err != nil {
		t.Fatal(err)
	}

	entries, err := filepath.Glob(filepath.Join(dir, "topics", "events", "*"))
	if err != nil {
		t.Fatal(err)
	}
	paths := append([]string{dir, filepath.Join(dir, "topics"), filepath.Join(dir, "topics", "events")}, entries...)
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0077 != 0 {
			t.Fatalf("%s permissions = %o, group/other bits must be zero", path, info.Mode().Perm())
		}
	}
}

func TestQueueSafeRetentionSerializesWithConsumerRegistration(t *testing.T) {
	broker, err := NewBroker(
		t.TempDir(),
		WithMaxSegmentBytes(96),
		WithMaxMessageSize(64),
		WithRetentionTime(time.Nanosecond),
		WithRetentionBytes(96),
		WithoutAutomaticRetention(),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	producer, err := broker.NewProducer("events", WithBatchSize(1))
	if err != nil {
		t.Fatal(err)
	}
	for sequence := 0; sequence < 4; sequence++ {
		if err := producer.SendContext(
			context.Background(),
			nil,
			[]byte("payload-that-rolls-the-segment"),
			nil,
			AckAppend,
		); err != nil {
			t.Fatal(err)
		}
	}
	consumer, err := broker.NewConsumer("existing", "events")
	if err != nil {
		t.Fatal(err)
	}
	deliveries, err := consumer.Fetch(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 4 {
		t.Fatalf("deliveries = %d, want 4", len(deliveries))
	}
	if err := consumer.CommitOffset(context.Background(), deliveries[3].Offset+1); err != nil {
		t.Fatal(err)
	}
	if err := consumer.Close(); err != nil {
		t.Fatal(err)
	}

	topic := broker.getTopic("events")
	topic.log.mu.RLock()
	before := len(topic.log.segments)
	topic.log.mu.RUnlock()
	if before < 2 {
		t.Fatal("fixture did not create sealed data")
	}

	broker.groupMu.Lock()
	done := make(chan struct{})
	go func() {
		broker.runRetention()
		close(done)
	}()
	time.Sleep(30 * time.Millisecond)
	topic.log.mu.RLock()
	during := len(topic.log.segments)
	topic.log.mu.RUnlock()
	if during != before {
		broker.groupMu.Unlock()
		t.Fatal("retention changed segments outside the consumer-registration fence")
	}
	broker.groupMu.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("retention did not resume after registration fence released")
	}
}

func TestConsumerRollbackAndReplayDoNotAcknowledge(t *testing.T) {
	dir := t.TempDir()
	broker, err := NewBroker(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	producer, err := broker.NewProducer("events", WithBatchSize(1))
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 2; index++ {
		if err := producer.Send(nil, []byte(strconv.Itoa(index)), nil); err != nil {
			t.Fatal(err)
		}
	}

	consumer, err := broker.NewConsumer("projector", "events")
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()
	first, err := consumer.Fetch(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 {
		t.Fatalf("first fetch = %d", len(first))
	}
	if err := consumer.Rollback(); err != nil {
		t.Fatal(err)
	}
	second, err := consumer.Fetch(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 2 || second[0].Offset != first[0].Offset {
		t.Fatalf("rollback did not redeliver: %#v", second)
	}
	if err := consumer.Replay(1); err != nil {
		t.Fatal(err)
	}
	replayed, err := consumer.Fetch(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(replayed) != 1 || replayed[0].Offset != 1 || consumer.CommittedOffset() != 0 {
		t.Fatalf("replay changed acknowledgment state: %#v committed=%d", replayed, consumer.CommittedOffset())
	}
}

func TestConsumerMethodsHonorCancellationAndClose(t *testing.T) {
	broker, err := NewBroker(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	consumer, err := broker.NewConsumer("group", "topic")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := consumer.Fetch(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("Fetch canceled error = %v", err)
	}
	if err := consumer.CommitOffset(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("CommitOffset canceled error = %v", err)
	}
	if err := consumer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := consumer.Close(); err != nil {
		t.Fatalf("second Close() = %v", err)
	}
	if _, err := consumer.Fetch(context.Background(), 1); !errors.Is(err, ErrClosed) {
		t.Fatalf("Fetch after close error = %v", err)
	}
}

func TestDeprecatedSeekRejectsOffsetPastLogEnd(t *testing.T) {
	broker, err := NewBroker(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	consumer, err := broker.NewConsumer("group", "topic")
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()

	if err := consumer.Seek(^uint64(0)); !errors.Is(err, ErrOffsetNotFound) {
		t.Fatalf("Seek past log end error = %v, want ErrOffsetNotFound", err)
	}
	if err := consumer.Commit(); err != nil {
		t.Fatalf("Commit after rejected Seek error = %v", err)
	}
	committed, err := broker.offsetStore.Load("group", "topic")
	if err != nil {
		t.Fatal(err)
	}
	if committed != 0 {
		t.Fatalf("invalid seek persisted offset %d, want 0", committed)
	}
}

func TestFetchWaitWakesOnAppendAndCancellation(t *testing.T) {
	broker, err := NewBroker(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	consumer, err := broker.NewConsumer("worker", "jobs")
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()
	producer, err := broker.NewProducer("jobs", WithBatchSize(1))
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		deliveries []Delivery
		err        error
	}
	resultChannel := make(chan result, 1)
	go func() {
		deliveries, fetchErr := consumer.FetchWait(context.Background(), 1, time.Second)
		resultChannel <- result{deliveries: deliveries, err: fetchErr}
	}()
	time.Sleep(10 * time.Millisecond)
	if err := producer.Send(nil, []byte("wake"), nil); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-resultChannel:
		if got.err != nil || len(got.deliveries) != 1 ||
			string(got.deliveries[0].Value) != "wake" {
			t.Fatalf("FetchWait result = %#v, err=%v", got.deliveries, got.err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("FetchWait did not wake on append")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := consumer.FetchWait(ctx, 1, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("FetchWait canceled error = %v", err)
	}
}

func TestFetchWaitWakesPromptlyWhenConsumerCloses(t *testing.T) {
	broker, err := NewBroker(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	consumer, err := broker.NewConsumer("worker", "jobs")
	if err != nil {
		t.Fatal(err)
	}

	resultChannel := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		_, fetchErr := consumer.FetchWait(context.Background(), 1, time.Minute)
		resultChannel <- fetchErr
	}()
	<-started
	if err := consumer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case fetchErr := <-resultChannel:
		if !errors.Is(fetchErr, ErrClosed) {
			t.Fatalf("FetchWait after close error = %v, want ErrClosed", fetchErr)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("FetchWait did not wake when the consumer closed")
	}
}

func TestDeleteConsumerGroupRequiresReleaseAndUnpinsRetention(t *testing.T) {
	broker, err := NewBroker(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	consumer, err := broker.NewConsumer("obsolete", "events")
	if err != nil {
		t.Fatal(err)
	}
	if err := broker.DeleteConsumerGroup("obsolete", "events"); !errors.Is(err, ErrConsumerBusy) {
		t.Fatalf("DeleteConsumerGroup(active) = %v", err)
	}
	if err := consumer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, registered, err := broker.offsetStore.MinimumOffset("events"); err != nil || !registered {
		t.Fatalf("registered before delete = %v, err=%v", registered, err)
	}
	if err := broker.DeleteConsumerGroup("obsolete", "events"); err != nil {
		t.Fatal(err)
	}
	if _, registered, err := broker.offsetStore.MinimumOffset("events"); err != nil || registered {
		t.Fatalf("registered after delete = %v, err=%v", registered, err)
	}
}

func TestMetricsCallbacksCannotCrashQueueOperations(t *testing.T) {
	metrics := &MetricsHook{
		OnFlush:        func(string, int, int) { panic("flush") },
		OnPoll:         func(string, string, int) { panic("poll") },
		OnDrop:         func(string, string) { panic("drop") },
		OnBackpressure: func(string) { panic("backpressure") },
	}
	broker, err := NewBroker(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	producer, err := broker.NewProducer("events", WithBatchSize(1), WithMetrics(metrics))
	if err != nil {
		t.Fatal(err)
	}
	if err := producer.Send(nil, []byte("value"), nil); err != nil {
		t.Fatal(err)
	}
	consumer, err := broker.NewConsumer("group", "events", WithConsumerMetrics(metrics))
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()
	if _, err := consumer.Fetch(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if err := consumer.Nack(Record{Value: []byte("failed")}); !errors.Is(err, ErrDLQNotConfigured) {
		t.Fatalf("Nack() error = %v, want ErrDLQNotConfigured", err)
	}
}

func TestBrokerOwnsProducerMetricTopicIdentity(t *testing.T) {
	broker, err := NewBroker(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	var observedTopic string
	producer, err := broker.NewProducer(
		"actual-topic",
		func(config *ProducerConfig) error {
			config.Topic = "spoofed-topic"
			return nil
		},
		WithMetrics(&MetricsHook{OnFlush: func(topic string, _, _ int) {
			observedTopic = topic
		}}),
		WithBatchSize(1),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := producer.SendContext(
		context.Background(), nil, []byte("value"), nil, AckAppend,
	); err != nil {
		t.Fatal(err)
	}
	if observedTopic != "actual-topic" {
		t.Fatalf("metric topic = %q, want broker-owned identity", observedTopic)
	}
}

func TestNewDLQConsumerOwnsGeneratedDeadLetterRoute(t *testing.T) {
	broker, err := NewBroker(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	other, err := broker.NewProducer("other-dlq", WithBatchSize(1))
	if err != nil {
		t.Fatal(err)
	}
	consumer, _, err := broker.NewDLQConsumer(
		"group",
		"events",
		WithDLQ(other),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()
	if err := consumer.NackContext(
		context.Background(),
		Record{Value: []byte("failed")},
	); err != nil {
		t.Fatal(err)
	}

	generated, err := broker.NewConsumer("generated-reader", "events"+dlqSuffix)
	if err != nil {
		t.Fatal(err)
	}
	defer generated.Close()
	deliveries, err := generated.Fetch(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 1 || string(deliveries[0].Value) != "failed" {
		t.Fatalf("generated DLQ deliveries = %#v", deliveries)
	}
	otherReader, err := broker.NewConsumer("other-reader", "other-dlq")
	if err != nil {
		t.Fatal(err)
	}
	defer otherReader.Close()
	otherDeliveries, err := otherReader.Fetch(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(otherDeliveries) != 0 {
		t.Fatalf("caller option overrode generated DLQ: %#v", otherDeliveries)
	}
}

func TestBrokerHealthStatsAndConsumerLag(t *testing.T) {
	broker, err := NewBroker(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	producer, err := broker.NewProducer("events", WithBatchSize(1))
	if err != nil {
		t.Fatal(err)
	}
	if err := producer.SendContext(
		context.Background(), nil, []byte("value"), nil, AckAppend,
	); err != nil {
		t.Fatal(err)
	}
	if err := broker.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	stats, err := broker.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 ||
		stats[0].Topic != "events" ||
		stats[0].NewestOffset != 1 ||
		stats[0].StorageBytes <= 0 {
		t.Fatalf("stats = %#v", stats)
	}
	lag, err := broker.ConsumerLag(context.Background(), "projector", "events")
	if err != nil {
		t.Fatal(err)
	}
	if lag != 1 {
		t.Fatalf("lag = %d, want 1", lag)
	}
	if err := broker.Close(); err != nil {
		t.Fatal(err)
	}
	if err := broker.Check(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("Check after close = %v", err)
	}
}

func TestNilBrokerHealthFailsClosed(t *testing.T) {
	t.Parallel()

	var broker *Broker
	if err := broker.Check(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("nil Broker.Check() = %v, want ErrClosed", err)
	}
}

func TestDirectoryIterationHonorsEntryLimit(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	for _, name := range []string{"a", "b", "c"} {
		if err := os.WriteFile(filepath.Join(directory, name), nil, filePerm); err != nil {
			t.Fatal(err)
		}
	}
	visited := 0
	err := forEachDirectoryEntryLimit(directory, 2, func(os.DirEntry) error {
		visited++
		return nil
	})
	if !errors.Is(err, ErrResourceLimit) || visited != 2 {
		t.Fatalf("bounded directory scan = (visited=%d, err=%v)", visited, err)
	}
}

func TestBrokerCloseDrainsOwnedProducerAndClosesChildren(t *testing.T) {
	dir := t.TempDir()
	broker, err := NewBroker(dir)
	if err != nil {
		t.Fatal(err)
	}
	producer, err := broker.NewProducer(
		"events",
		WithBatchSize(1<<20),
		WithLinger(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := broker.NewConsumer("workers", "events")
	if err != nil {
		t.Fatal(err)
	}
	if err := producer.Send(nil, []byte("pending"), nil); err != nil {
		t.Fatal(err)
	}

	if err := broker.Close(); err != nil {
		t.Fatal(err)
	}
	if err := producer.Send(nil, []byte("late"), nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("producer after broker close = %v", err)
	}
	if _, err := consumer.Fetch(context.Background(), 1); !errors.Is(err, ErrClosed) {
		t.Fatalf("consumer after broker close = %v", err)
	}

	restarted, err := NewBroker(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	restartedConsumer, err := restarted.NewConsumer("workers", "events")
	if err != nil {
		t.Fatal(err)
	}
	defer restartedConsumer.Close()
	deliveries, err := restartedConsumer.Fetch(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 1 || string(deliveries[0].Value) != "pending" {
		t.Fatalf("broker close did not drain producer: %#v", deliveries)
	}
}

func TestStorageLimitBackpressuresWithoutDeletingUnconsumedData(t *testing.T) {
	dir := t.TempDir()
	broker, err := NewBroker(
		dir,
		WithMaxSegmentBytes(96),
		WithMaxMessageSize(64),
		WithMaxStorageBytes(256),
		WithRetentionTime(time.Nanosecond),
		WithRetentionBytes(96),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	producer, err := broker.NewProducer("events", WithBatchSize(1))
	if err != nil {
		t.Fatal(err)
	}

	accepted := 0
	for {
		err := producer.SendContext(
			context.Background(),
			nil,
			[]byte("12345678901234567890123456789012"),
			nil,
			AckAppend,
		)
		if errors.Is(err, ErrStorageFull) {
			var acknowledgmentError *AcknowledgmentError
			if !errors.As(err, &acknowledgmentError) ||
				!acknowledgmentError.Admitted ||
				acknowledgmentError.Appended {
				t.Fatalf("storage acknowledgment = %#v", acknowledgmentError)
			}
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		accepted++
		if accepted > 100 {
			t.Fatal("storage limit did not apply")
		}
	}
	if accepted == 0 {
		t.Fatal("fixture accepted no records")
	}
	broker.runRetention()

	consumer, err := broker.NewConsumer("workers", "events")
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()
	deliveries, err := consumer.Fetch(context.Background(), accepted+1)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != accepted {
		t.Fatalf("retention deleted unconsumed records: got=%d want=%d", len(deliveries), accepted)
	}
}

func TestRetentionIsPinnedBySlowestCommittedConsumer(t *testing.T) {
	dir := t.TempDir()
	broker, err := NewBroker(
		dir,
		WithMaxSegmentBytes(80),
		WithMaxStorageBytes(8<<20),
		WithRetentionTime(time.Nanosecond),
		WithRetentionBytes(80),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	producer, err := broker.NewProducer("events", WithBatchSize(1))
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 20; index++ {
		if err := producer.SendContext(
			context.Background(),
			nil,
			[]byte("retention-payload-"+strconv.Itoa(index)),
			nil,
			AckAppend,
		); err != nil {
			t.Fatal(err)
		}
	}

	fast, err := broker.NewConsumer("fast", "events")
	if err != nil {
		t.Fatal(err)
	}
	slow, err := broker.NewConsumer("slow", "events")
	if err != nil {
		t.Fatal(err)
	}
	fastDeliveries, err := fast.Fetch(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	slowDeliveries, err := slow.Fetch(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := fast.CommitOffset(context.Background(), fastDeliveries[len(fastDeliveries)-1].Offset+1); err != nil {
		t.Fatal(err)
	}
	if err := slow.CommitOffset(context.Background(), slowDeliveries[0].Offset+1); err != nil {
		t.Fatal(err)
	}
	segmentsBefore := len(broker.topics["events"].log.segments)
	broker.runRetention()
	segmentsPinned := len(broker.topics["events"].log.segments)
	if segmentsPinned == 0 || broker.topics["events"].log.OldestOffset() > 1 {
		t.Fatal("retention advanced beyond the slow consumer")
	}

	if err := slow.Replay(1); err != nil {
		t.Fatal(err)
	}
	remaining, err := slow.Fetch(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := slow.CommitOffset(context.Background(), remaining[len(remaining)-1].Offset+1); err != nil {
		t.Fatal(err)
	}
	broker.runRetention()
	segmentsAfter := len(broker.topics["events"].log.segments)
	if segmentsAfter >= segmentsPinned || segmentsAfter >= segmentsBefore {
		t.Fatalf(
			"retention did not advance after slow consumer: before=%d pinned=%d after=%d",
			segmentsBefore,
			segmentsPinned,
			segmentsAfter,
		)
	}
}
