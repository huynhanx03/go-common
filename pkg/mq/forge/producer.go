package forge

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ProducerConfig configures a Producer.
type ProducerConfig struct {
	BatchSize       int
	LingerTime      time.Duration
	Compression     uint8
	Clock           func() int64
	MaxPendingBytes int
	OnError         func(error)
	Metrics         *MetricsHook
	Topic           string
}

const estimatedRecordsPerBatch = 64
const maxFlushRetryDelay = time.Second
const DefaultMaxPendingBytes = maxBatchPayloadBytes

// Producer copies and batches caller records before appending them to a
// CommitLog. A single flush gate preserves admission order across concurrent
// senders.
type Producer struct {
	mu             sync.Mutex
	log            *CommitLog
	config         ProducerConfig
	pending        []Record
	backBuf        []Record
	bufSize        int
	pendingHeaders int

	inflightBytes   int
	inflightRecords int
	inflightHeaders int
	closed          bool
	clock           func() int64
	maxMessageSize  int

	flushGate chan struct{}
	flushCh   chan struct{}
	flushNow  chan struct{}
	stopCh    chan struct{}
	wg        sync.WaitGroup

	closeOnce sync.Once
	closeErr  error
	onClose   func()
	onAppend  func()
}

func defaultProducerConfig() ProducerConfig {
	return ProducerConfig{
		BatchSize:       defaultBatchBytes,
		LingerTime:      defaultLingerTime,
		MaxPendingBytes: DefaultMaxPendingBytes,
	}
}

func (config ProducerConfig) validate() error {
	if config.BatchSize <= 0 ||
		config.BatchSize > maxBatchPayloadBytes ||
		config.LingerTime <= 0 ||
		(config.Compression != CompressionNone && config.Compression != CompressionLZ4) ||
		config.MaxPendingBytes <= 0 ||
		config.MaxPendingBytes > maxBatchPayloadBytes {
		return fmt.Errorf("%w: producer", ErrInvalidConfig)
	}
	return nil
}

// ProducerOption configures a Producer.
type ProducerOption func(*ProducerConfig) error

func WithBatchSize(size int) ProducerOption {
	return func(config *ProducerConfig) error {
		if size <= 0 || size > maxBatchPayloadBytes {
			return invalidOption("producer batch size")
		}
		config.BatchSize = size
		return nil
	}
}

func WithLinger(duration time.Duration) ProducerOption {
	return func(config *ProducerConfig) error {
		if duration <= 0 {
			return invalidOption("producer linger")
		}
		config.LingerTime = duration
		return nil
	}
}

func WithCompression(compression uint8) ProducerOption {
	return func(config *ProducerConfig) error {
		if compression != CompressionNone && compression != CompressionLZ4 {
			return invalidOption("producer compression")
		}
		config.Compression = compression
		return nil
	}
}

func WithClock(clock func() int64) ProducerOption {
	return func(config *ProducerConfig) error {
		if clock == nil {
			return invalidOption("producer clock")
		}
		config.Clock = clock
		return nil
	}
}

func WithMaxPendingBytes(size int) ProducerOption {
	return func(config *ProducerConfig) error {
		if size <= 0 || size > maxBatchPayloadBytes {
			return invalidOption("producer pending bytes")
		}
		config.MaxPendingBytes = size
		return nil
	}
}

func WithOnError(callback func(error)) ProducerOption {
	return func(config *ProducerConfig) error {
		config.OnError = callback
		return nil
	}
}

func WithMetrics(metrics *MetricsHook) ProducerOption {
	return func(config *ProducerConfig) error {
		config.Metrics = metrics
		return nil
	}
}

// NewProducer creates a producer for log. It starts one linger goroutine and
// must be closed. A nil or closed log is rejected without starting a goroutine.
func NewProducer(log *CommitLog, options ...ProducerOption) (*Producer, error) {
	if log == nil {
		return nil, fmt.Errorf("%w: nil commit log", ErrInvalidConfig)
	}
	config, err := resolveProducerConfig(options...)
	if err != nil {
		return nil, err
	}
	return newProducer(log, config)
}

func resolveProducerConfig(options ...ProducerOption) (ProducerConfig, error) {
	config := defaultProducerConfig()
	for _, option := range options {
		if err := applyOption(option, &config); err != nil {
			return ProducerConfig{}, err
		}
	}
	if err := config.validate(); err != nil {
		return ProducerConfig{}, err
	}
	return config, nil
}

func newProducer(log *CommitLog, config ProducerConfig) (*Producer, error) {
	if log == nil {
		return nil, fmt.Errorf("%w: nil commit log", ErrInvalidConfig)
	}
	log.mu.RLock()
	logClosed := log.closed
	logStorageErr := log.storageErr
	maxMessageSize := log.config.MaxMessageSize
	log.mu.RUnlock()
	if logClosed {
		return nil, ErrClosed
	}
	if logStorageErr != nil {
		return nil, logStorageErr
	}

	clock := config.Clock
	if clock == nil {
		clock = func() int64 { return time.Now().UnixNano() }
	}

	producer := &Producer{
		log:            log,
		config:         config,
		pending:        make([]Record, 0, estimatedRecordsPerBatch),
		backBuf:        make([]Record, 0, estimatedRecordsPerBatch),
		clock:          clock,
		maxMessageSize: maxMessageSize,
		flushGate:      make(chan struct{}, 1),
		flushCh:        make(chan struct{}, 1),
		flushNow:       make(chan struct{}, 1),
		stopCh:         make(chan struct{}),
	}
	producer.flushGate <- struct{}{}

	producer.wg.Add(1)
	go producer.lingerLoop()
	return producer, nil
}

// Send admits a deep copy of one record and returns AckMemory semantics.
// Append errors are delivered to WithOnError and retried by the linger loop;
// use SendContext with AckAppend or AckFsync when the caller needs durability.
func (producer *Producer) Send(key, value []byte, headers []Header) error {
	return producer.SendContext(context.Background(), key, value, headers, AckMemory)
}

// SendContext admits one record and waits for the selected acknowledgment.
func (producer *Producer) SendContext(
	ctx context.Context,
	key, value []byte,
	headers []Header,
	ack Acknowledgment,
) error {
	if ctx == nil {
		return fmt.Errorf("%w: nil context", ErrInvalidConfig)
	}
	if !ack.valid() {
		return ErrInvalidAck
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	borrowed := Record{Key: key, Value: value, Headers: headers}
	recordSize, err := encodedRecordSize(borrowed)
	if err != nil || (producer.maxMessageSize > 0 && recordSize > producer.maxMessageSize) {
		return ErrMessageTooLarge
	}
	record := copyRecord(borrowed)

	producer.mu.Lock()
	if producer.closed {
		producer.mu.Unlock()
		return ErrClosed
	}
	totalBytes := producer.bufSize + producer.inflightBytes + recordSize
	totalRecords := len(producer.pending) + producer.inflightRecords + 1
	totalHeaders := producer.pendingHeaders + producer.inflightHeaders + len(record.Headers)
	if totalBytes > producer.config.MaxPendingBytes ||
		totalBytes > maxBatchPayloadBytes ||
		totalRecords > maxRecordsPerBatch ||
		totalHeaders > maxHeadersPerBatch {
		producer.mu.Unlock()
		producer.config.Metrics.backpressureHook(producer.config.Topic)
		return ErrBackpressure
	}
	producer.pending = append(producer.pending, record)
	producer.bufSize += recordSize
	producer.pendingHeaders += len(record.Headers)
	shouldFlush := producer.bufSize >= producer.config.BatchSize ||
		len(producer.pending) >= maxRecordsPerBatch ||
		producer.pendingHeaders >= maxHeadersPerBatch
	producer.mu.Unlock()

	if ack == AckMemory {
		producer.signalFlush(shouldFlush)
		return nil
	}

	flushErr := producer.flushContext(ctx)
	if flushErr != nil {
		var acknowledgmentError *AcknowledgmentError
		if ack == AckAppend &&
			errors.As(flushErr, &acknowledgmentError) &&
			acknowledgmentError.Appended {
			producer.reportError(flushErr)
			return nil
		}
		if errors.As(flushErr, &acknowledgmentError) {
			return &AcknowledgmentError{
				Level:    acknowledgmentError.Level,
				Admitted: true,
				Appended: acknowledgmentError.Appended,
				Cause:    acknowledgmentError.Cause,
			}
		}
		return &AcknowledgmentError{
			Level:    ack,
			Admitted: true,
			Appended: false,
			Cause:    flushErr,
		}
	}
	if ack == AckFsync {
		if err := producer.log.Sync(); err != nil {
			return &AcknowledgmentError{
				Level:    AckFsync,
				Admitted: true,
				Appended: true,
				Cause:    err,
			}
		}
	}
	return nil
}

// Flush forces the records admitted before its flush-gate turn to append.
func (producer *Producer) Flush() error {
	return producer.FlushContext(context.Background())
}

// FlushContext is Flush with bounded waiting for another concurrent flush.
// Once local filesystem I/O starts it is allowed to complete so the result is
// never ambiguous because a helper goroutine outlived the call.
func (producer *Producer) FlushContext(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("%w: nil context", ErrInvalidConfig)
	}
	return producer.flushContext(ctx)
}

func (producer *Producer) flushContext(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-producer.flushGate:
	}
	released := false
	releaseGate := func() {
		if released {
			return
		}
		producer.flushGate <- struct{}{}
		released = true
	}
	defer releaseGate()

	producer.mu.Lock()
	if len(producer.pending) == 0 {
		producer.mu.Unlock()
		return nil
	}

	records := producer.pending
	estimatedBytes := producer.bufSize
	headerCount := producer.pendingHeaders
	producer.pending = producer.backBuf[:0]
	producer.backBuf = nil
	producer.bufSize = 0
	producer.pendingHeaders = 0
	producer.inflightBytes = estimatedBytes
	producer.inflightRecords = len(records)
	producer.inflightHeaders = headerCount
	producer.mu.Unlock()

	now, clockErr := producer.readClock()
	if clockErr != nil {
		producer.restoreFailedBatch(records, estimatedBytes, headerCount)
		return clockErr
	}
	batch := &RecordBatch{
		Compression:  producer.config.Compression,
		Timestamp:    now,
		MaxTimestamp: now,
		Records:      records,
	}
	_, appendErr := producer.log.Append(batch)

	appended := appendErr == nil
	if appendErr != nil {
		var acknowledgmentError *AcknowledgmentError
		appended = errors.As(appendErr, &acknowledgmentError) && acknowledgmentError.Appended
	}
	if !appended {
		producer.restoreFailedBatch(records, estimatedBytes, headerCount)
		return appendErr
	}

	producer.recycleSuccessfulBatch(records)
	releaseGate()
	if appendErr == nil {
		producer.config.Metrics.flushHook(producer.config.Topic, len(records), estimatedBytes)
	}
	if producer.onAppend != nil {
		producer.onAppend()
	}
	return appendErr
}

func (producer *Producer) readClock() (now int64, resultErr error) {
	defer func() {
		if recover() != nil {
			now = 0
			resultErr = fmt.Errorf("%w: producer clock panic", ErrInvalidConfig)
		}
	}()
	return producer.clock(), nil
}

func (producer *Producer) restoreFailedBatch(records []Record, estimatedBytes, restoredHeaders int) {
	producer.mu.Lock()
	current := producer.pending
	currentBytes := producer.bufSize
	currentHeaders := producer.pendingHeaders

	required := len(records) + len(current)
	var restored []Record
	if cap(records) >= required {
		restored = records[:len(records)]
	} else {
		restored = make([]Record, len(records), required)
		copy(restored, records)
	}
	restored = append(restored, current...)
	for index := range current {
		current[index] = Record{}
	}

	producer.pending = restored
	producer.backBuf = current[:0]
	producer.bufSize = estimatedBytes + currentBytes
	producer.pendingHeaders = restoredHeaders + currentHeaders
	producer.inflightBytes = 0
	producer.inflightRecords = 0
	producer.inflightHeaders = 0
	producer.mu.Unlock()
}

func (producer *Producer) recycleSuccessfulBatch(records []Record) {
	for index := range records {
		records[index] = Record{}
	}
	producer.mu.Lock()
	producer.inflightBytes = 0
	producer.inflightRecords = 0
	producer.inflightHeaders = 0
	if producer.backBuf == nil {
		producer.backBuf = records[:0]
	}
	producer.mu.Unlock()
}

// PendingRecords reports records awaiting append, including an in-flight batch.
func (producer *Producer) PendingRecords() int {
	producer.mu.Lock()
	defer producer.mu.Unlock()
	return len(producer.pending) + producer.inflightRecords
}

func (producer *Producer) signalFlush(immediate bool) {
	if immediate {
		select {
		case producer.flushNow <- struct{}{}:
		default:
		}
		return
	}
	select {
	case producer.flushCh <- struct{}{}:
	default:
	}
}

func (producer *Producer) lingerLoop() {
	defer producer.wg.Done()

	timer := time.NewTimer(producer.config.LingerTime)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	retryDelay := producer.config.LingerTime
	timerActive := false

	stopTimer := func() {
		if !timerActive {
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timerActive = false
	}
	schedule := func(delay time.Duration) {
		stopTimer()
		timer.Reset(delay)
		timerActive = true
	}
	flush := func() {
		stopTimer()
		if err := producer.flushContext(context.Background()); err != nil {
			producer.reportError(err)
			if retryDelay >= maxFlushRetryDelay/2 {
				retryDelay = maxFlushRetryDelay
			} else {
				retryDelay *= 2
			}
		} else {
			retryDelay = producer.config.LingerTime
		}
		if producer.PendingRecords() > 0 {
			schedule(retryDelay)
		}
	}

	for {
		select {
		case <-producer.stopCh:
			return
		case <-producer.flushNow:
			flush()
		case <-producer.flushCh:
			if !timerActive {
				schedule(producer.config.LingerTime)
			}
		case <-timer.C:
			timerActive = false
			flush()
		}
	}
}

func (producer *Producer) reportError(err error) {
	if err == nil || producer.config.OnError == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	producer.config.OnError(err)
}

// Close rejects new admissions, stops linger, and performs one final flush.
func (producer *Producer) Close() error {
	producer.closeOnce.Do(func() {
		producer.mu.Lock()
		producer.closed = true
		producer.mu.Unlock()

		close(producer.stopCh)
		producer.wg.Wait()

		producer.closeErr = producer.flushContext(context.Background())
		if producer.onClose != nil {
			producer.onClose()
		}
	})
	return producer.closeErr
}

func encodedRecordSize(record Record) (int, error) {
	if len(record.Headers) > maxHeadersPerRecord {
		return 0, ErrMessageTooLarge
	}
	// Producer-created records have a zero timestamp delta. OffsetDelta is
	// assigned during append and is bounded by the maximum batch record count.
	bodySize := int64(
		varIntSize(0) +
			varIntSize(maxRecordsPerBatch-1) +
			varIntSize(int64(len(record.Headers))),
	)
	add := func(length int) bool {
		bodySize += int64(varIntSize(int64(length))) + int64(length)
		return bodySize <= maxBatchPayloadBytes
	}
	if !add(len(record.Key)) || !add(len(record.Value)) {
		return 0, ErrMessageTooLarge
	}
	for _, header := range record.Headers {
		if !add(len(header.Key)) || !add(len(header.Value)) {
			return 0, ErrMessageTooLarge
		}
	}
	totalSize := int64(varIntSize(bodySize)) + bodySize
	if totalSize > maxBatchPayloadBytes {
		return 0, ErrMessageTooLarge
	}
	return int(totalSize), nil
}
