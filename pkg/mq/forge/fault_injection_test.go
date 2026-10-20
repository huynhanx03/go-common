package forge

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestProducerCopiesCallerOwnedBytesAtAdmission(t *testing.T) {
	dir := t.TempDir()
	log, err := NewCommitLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()

	producer, err := NewProducer(log, WithBatchSize(1<<20), WithLinger(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()

	key := []byte("original-key")
	value := []byte("original-value")
	headers := []Header{{Key: []byte("header-key"), Value: []byte("header-value")}}
	if err := producer.Send(key, value, headers); err != nil {
		t.Fatal(err)
	}

	copy(key, "mutated-key!")
	copy(value, "mutated-value!")
	copy(headers[0].Key, "mutated-key")
	copy(headers[0].Value, "mutated-value")
	headers[0] = Header{Key: []byte("replacement"), Value: []byte("replacement")}

	if err := producer.Flush(); err != nil {
		t.Fatal(err)
	}
	batches, err := log.Read(0, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	record := batches[0].Records[0]
	if string(record.Key) != "original-key" ||
		string(record.Value) != "original-value" ||
		string(record.Headers[0].Key) != "header-key" ||
		string(record.Headers[0].Value) != "header-value" {
		t.Fatalf("persisted record aliases caller memory: %#v", record)
	}
}

func TestProducerRestoresBatchAfterShortWrite(t *testing.T) {
	dir := t.TempDir()
	ops := defaultFileOperations()
	var failOnce atomic.Bool
	failOnce.Store(true)
	ops.write = func(file *os.File, data []byte) (int, error) {
		if failOnce.CompareAndSwap(true, false) {
			n, err := file.Write(data[:len(data)/2])
			if err != nil {
				return n, err
			}
			return n, nil
		}
		return file.Write(data)
	}

	log, err := NewCommitLog(dir, withFileOperations(ops))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	producer, err := NewProducer(log, WithBatchSize(1<<20), WithLinger(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()

	if err := producer.Send(nil, []byte("must-survive"), nil); err != nil {
		t.Fatal(err)
	}
	if err := producer.Flush(); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("first Flush() error = %v, want io.ErrShortWrite", err)
	}
	if producer.PendingRecords() != 1 {
		t.Fatalf("pending records = %d, want 1 after failed append", producer.PendingRecords())
	}
	if info, err := os.Stat(log.activeSegment.logFile.Name()); err != nil || info.Size() != 0 {
		t.Fatalf("partial append was not rolled back: info=%v err=%v", info, err)
	}

	if err := producer.Flush(); err != nil {
		t.Fatalf("retry Flush() error = %v", err)
	}
	batches, err := log.Read(0, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 1 || len(batches[0].Records) != 1 ||
		string(batches[0].Records[0].Value) != "must-survive" {
		t.Fatalf("record lost or duplicated after retry: %#v", batches)
	}
}

func TestCommitLogFailsClosedWhenPartialWriteCannotRollback(t *testing.T) {
	ops := defaultFileOperations()
	failOnce := true
	ops.write = func(file *os.File, data []byte) (int, error) {
		if !failOnce || filepath.Ext(file.Name()) != extLog {
			return file.Write(data)
		}
		failOnce = false
		n, writeErr := file.Write(data[:len(data)/2])
		closeErr := file.Close()
		return n, errors.Join(io.ErrUnexpectedEOF, writeErr, closeErr)
	}

	log, err := NewCommitLog(t.TempDir(), withFileOperations(ops))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	if _, err := log.Append(&RecordBatch{Records: []Record{{Value: []byte("ambiguous")}}}); !errors.Is(err, ErrInvalidSegmentLayout) {
		t.Fatalf("unrecoverable short write error = %v, want terminal storage error", err)
	}
	if got := log.NewestOffset(); got != 0 {
		t.Fatalf("ambiguous partial write advanced in-memory offset to %d", got)
	}
	if _, err := log.Append(&RecordBatch{Records: []Record{{Value: []byte("must-not-write")}}}); !errors.Is(err, ErrInvalidSegmentLayout) {
		t.Fatalf("append after rollback failure error = %v, want terminal storage error", err)
	}
}

func TestFsyncFailureAdvancesAppendOffsetAndReturnsTypedAcknowledgment(t *testing.T) {
	dir := t.TempDir()
	ops := defaultFileOperations()
	var failOnce atomic.Bool
	ops.sync = func(file *os.File) error {
		if filepath.Ext(file.Name()) == extLog && failOnce.CompareAndSwap(true, false) {
			return errors.New("injected fsync failure")
		}
		return file.Sync()
	}

	log, err := NewCommitLog(dir, WithFsyncEvery(1), withFileOperations(ops))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	failOnce.Store(true)

	base, err := log.Append(&RecordBatch{Records: []Record{{Value: []byte("appended")}}})
	if base != 0 {
		t.Fatalf("base offset = %d, want 0", base)
	}
	var ackErr *AcknowledgmentError
	if !errors.As(err, &ackErr) {
		t.Fatalf("Append() error = %v, want *AcknowledgmentError", err)
	}
	if ackErr.Level != AckFsync || !ackErr.Appended {
		t.Fatalf("ack error = %#v, want fsync + appended", ackErr)
	}
	if log.NewestOffset() != 1 {
		t.Fatalf("NewestOffset() = %d, want 1 after successful append", log.NewestOffset())
	}

	nextBase, err := log.Append(&RecordBatch{Records: []Record{{Value: []byte("next")}}})
	if err != nil {
		t.Fatalf("second Append() error = %v", err)
	}
	if nextBase != 1 {
		t.Fatalf("second base offset = %d, want 1", nextBase)
	}
}

func TestProducerExplicitAcknowledgmentLevels(t *testing.T) {
	dir := t.TempDir()
	log, err := NewCommitLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	producer, err := NewProducer(log, WithBatchSize(1<<20), WithLinger(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()

	if err := producer.SendContext(context.Background(), nil, []byte("memory"), nil, AckMemory); err != nil {
		t.Fatalf("memory admission: %v", err)
	}
	if log.NewestOffset() != 0 {
		t.Fatal("memory acknowledgment must not claim append")
	}
	if err := producer.SendContext(context.Background(), nil, []byte("append"), nil, AckAppend); err != nil {
		t.Fatalf("append acknowledgment: %v", err)
	}
	if log.NewestOffset() != 2 {
		t.Fatalf("append acknowledgment returned before append, offset=%d", log.NewestOffset())
	}
	if err := producer.SendContext(context.Background(), nil, []byte("fsync"), nil, AckFsync); err != nil {
		t.Fatalf("fsync acknowledgment: %v", err)
	}
	if log.NewestOffset() != 3 {
		t.Fatalf("fsync acknowledgment returned before append, offset=%d", log.NewestOffset())
	}
}

func TestCommitLogRejectsCompleteCorruptionWithoutTruncating(t *testing.T) {
	dir := t.TempDir()
	log, err := NewCommitLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := log.Append(&RecordBatch{Records: []Record{{Value: []byte("valid")}}}); err != nil {
		t.Fatal(err)
	}
	path := log.activeSegment.logFile.Name()
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}

	file, err := os.OpenFile(path, os.O_RDWR, filePerm)
	if err != nil {
		t.Fatal(err)
	}
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	beforeSize := info.Size()
	var one [1]byte
	if _, err := file.ReadAt(one[:], batchHeaderSize); err != nil {
		t.Fatal(err)
	}
	one[0] ^= 0xff
	if _, err := file.WriteAt(one[:], batchHeaderSize); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = NewCommitLog(dir)
	var corruption *CorruptionError
	if !errors.As(err, &corruption) {
		t.Fatalf("reopen error = %v, want *CorruptionError", err)
	}
	afterInfo, statErr := os.Stat(path)
	if statErr != nil {
		t.Fatal(statErr)
	}
	if afterInfo.Size() != beforeSize {
		t.Fatalf("complete corruption was silently truncated: before=%d after=%d", beforeSize, afterInfo.Size())
	}
}

func TestCommitLogTruncatesOnlyIncompleteCrashTail(t *testing.T) {
	dir := t.TempDir()
	log, err := NewCommitLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := log.Append(&RecordBatch{Records: []Record{{Value: []byte("valid")}}}); err != nil {
		t.Fatal(err)
	}
	validSize := log.activeSegment.size
	path := log.activeSegment.logFile.Name()
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, filePerm)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte{1, 2, 3, 4, 5}); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	recovered, err := NewCommitLog(dir)
	if err != nil {
		t.Fatalf("reopen incomplete tail: %v", err)
	}
	defer recovered.Close()
	if recovered.activeSegment.size != validSize {
		t.Fatalf("recovered size = %d, want %d", recovered.activeSegment.size, validSize)
	}
	if recovered.NewestOffset() != 1 {
		t.Fatalf("recovered offset = %d, want 1", recovered.NewestOffset())
	}
}

func TestConcurrentProducerPreservesPerSenderOrder(t *testing.T) {
	dir := t.TempDir()
	log, err := NewCommitLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	producer, err := NewProducer(log, WithBatchSize(128), WithLinger(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}

	const senders = 8
	const perSender = 100
	var wg sync.WaitGroup
	for sender := 0; sender < senders; sender++ {
		sender := sender
		wg.Add(1)
		go func() {
			defer wg.Done()
			key := []byte(strconv.Itoa(sender))
			for sequence := 0; sequence < perSender; sequence++ {
				if err := producer.Send(key, []byte(strconv.Itoa(sequence)), nil); err != nil {
					t.Errorf("Send(%d,%d): %v", sender, sequence, err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if err := producer.Close(); err != nil {
		t.Fatal(err)
	}

	batches, err := log.Read(0, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	last := make(map[string]int, senders)
	for sender := 0; sender < senders; sender++ {
		last[strconv.Itoa(sender)] = -1
	}
	count := 0
	for _, batch := range batches {
		for _, record := range batch.Records {
			sender := string(record.Key)
			sequence, err := strconv.Atoi(string(record.Value))
			if err != nil {
				t.Fatal(err)
			}
			if sequence != last[sender]+1 {
				t.Fatalf("sender %s sequence = %d after %d", sender, sequence, last[sender])
			}
			last[sender] = sequence
			count++
		}
	}
	if count != senders*perSender {
		t.Fatalf("record count = %d, want %d", count, senders*perSender)
	}
}

func TestProducerRejectsOversizedKeyAndHeaders(t *testing.T) {
	log, err := NewCommitLog(t.TempDir(), WithMaxMessageSize(64))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	producer, err := NewProducer(log)
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()

	if err := producer.Send(bytes.Repeat([]byte("k"), 65), nil, nil); !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("oversized key error = %v", err)
	}
	headers := []Header{{Key: []byte("key"), Value: bytes.Repeat([]byte("v"), 65)}}
	if err := producer.Send(nil, nil, headers); !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("oversized headers error = %v", err)
	}
}
