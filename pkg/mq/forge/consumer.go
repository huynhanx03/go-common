package forge

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"
)

const MaxPollRecords = 10_000
const DefaultFetchWaitInterval = 100 * time.Millisecond

// Consumer owns one exclusive (group, topic) lease. Fetch advances only its
// in-memory cursor; CommitOffset is the sole durable acknowledgment operation.
type Consumer struct {
	mu              sync.Mutex
	log             *CommitLog
	group           string
	topic           string
	offset          uint64
	committedOffset uint64
	offsetStore     *OffsetStore
	metrics         *MetricsHook
	dlq             *Producer
	lease           *consumerLease
	closed          bool
	closeOnce       sync.Once
	closeErr        error
	onClose         func()
	waitSignal      func() <-chan struct{}
	done            chan struct{}
}

// FetchWait waits for at least one delivery, a topic notification, the
// fallback interval, or context cancellation. The fallback preserves progress
// for consumers constructed directly without a Broker.
func (consumer *Consumer) FetchWait(
	ctx context.Context,
	maxRecords int,
	fallbackInterval time.Duration,
) ([]Delivery, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: nil context", ErrInvalidConfig)
	}
	if fallbackInterval <= 0 || fallbackInterval > time.Minute {
		return nil, fmt.Errorf("%w: fetch wait interval", ErrInvalidConfig)
	}
	for {
		var wake <-chan struct{}
		if consumer.waitSignal != nil {
			wake = consumer.waitSignal()
		}
		deliveries, err := consumer.Fetch(ctx, maxRecords)
		if err != nil || len(deliveries) > 0 {
			return deliveries, err
		}

		timer := time.NewTimer(fallbackInterval)
		select {
		case <-ctx.Done():
			stopFetchTimer(timer)
			return nil, ctx.Err()
		case <-consumer.done:
			stopFetchTimer(timer)
			return nil, ErrClosed
		case <-wake:
			stopFetchTimer(timer)
		case <-timer.C:
		}
	}
}

func stopFetchTimer(timer *time.Timer) {
	if timer.Stop() {
		return
	}
	select {
	case <-timer.C:
	default:
	}
}

type ConsumerOption func(*consumerConfig) error

type consumerConfig struct {
	dlq     *Producer
	metrics *MetricsHook
}

func WithDLQ(producer *Producer) ConsumerOption {
	return func(config *consumerConfig) error {
		if producer == nil {
			return invalidOption("consumer DLQ")
		}
		config.dlq = producer
		return nil
	}
}

func WithConsumerMetrics(metrics *MetricsHook) ConsumerOption {
	return func(config *consumerConfig) error {
		config.metrics = metrics
		return nil
	}
}

// NewConsumer acquires exclusive cross-process ownership of group/topic and
// resumes from the last fsynced committed offset.
func NewConsumer(
	log *CommitLog,
	group, topic string,
	store *OffsetStore,
	options ...ConsumerOption,
) (*Consumer, error) {
	if log == nil || store == nil || !validResourceName(group) || !validResourceName(topic) {
		return nil, fmt.Errorf("%w: consumer", ErrInvalidConfig)
	}
	config, err := resolveConsumerConfig(options...)
	if err != nil {
		return nil, err
	}
	return newConsumer(log, group, topic, store, config)
}

func resolveConsumerConfig(options ...ConsumerOption) (consumerConfig, error) {
	var config consumerConfig
	for _, option := range options {
		if err := applyOption(option, &config); err != nil {
			return consumerConfig{}, err
		}
	}
	return config, nil
}

func newConsumer(
	log *CommitLog,
	group, topic string,
	store *OffsetStore,
	config consumerConfig,
) (*Consumer, error) {
	if log == nil || store == nil || !validResourceName(group) || !validResourceName(topic) {
		return nil, fmt.Errorf("%w: consumer", ErrInvalidConfig)
	}
	log.mu.RLock()
	logClosed := log.closed
	logStorageErr := log.storageErr
	log.mu.RUnlock()
	if logClosed {
		return nil, ErrClosed
	}
	if logStorageErr != nil {
		return nil, logStorageErr
	}
	lease, err := store.acquireConsumer(group, topic)
	if err != nil {
		return nil, err
	}
	offset, err := store.Load(group, topic)
	if err != nil {
		_ = lease.Close()
		return nil, err
	}
	if offset > log.NewestOffset() {
		_ = lease.Close()
		return nil, ErrInvalidOffsetCommit
	}

	return &Consumer{
		log:             log,
		group:           group,
		topic:           topic,
		offset:          offset,
		committedOffset: offset,
		offsetStore:     store,
		metrics:         config.metrics,
		dlq:             config.dlq,
		lease:           lease,
		done:            make(chan struct{}),
	}, nil
}

// Fetch returns up to maxRecords from the in-memory cursor without committing.
func (consumer *Consumer) Fetch(ctx context.Context, maxRecords int) (
	deliveries []Delivery,
	resultErr error,
) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: nil context", ErrInvalidConfig)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if maxRecords <= 0 || maxRecords > MaxPollRecords {
		return nil, fmt.Errorf("%w: poll limit", ErrInvalidConfig)
	}

	consumer.mu.Lock()
	defer func() {
		consumer.mu.Unlock()
		if resultErr == nil && len(deliveries) > 0 {
			consumer.metrics.pollHook(consumer.group, consumer.topic, len(deliveries))
		}
	}()
	if consumer.closed {
		return nil, ErrClosed
	}

	fetchStart := consumer.offset
	deliveries = make([]Delivery, 0, maxRecords)
	for len(deliveries) < maxRecords && consumer.offset < consumer.log.NewestOffset() {
		if err := ctx.Err(); err != nil {
			consumer.offset = fetchStart
			return nil, err
		}
		startOffset := consumer.offset
		batches, err := consumer.log.Read(consumer.offset, maxEncodedBatchBytes)
		if err != nil {
			consumer.offset = fetchStart
			return nil, err
		}
		for _, batch := range batches {
			batchComplete := true
			for _, record := range batch.Records {
				absoluteOffset := batch.BaseOffset + uint64(record.OffsetDelta)
				if absoluteOffset < consumer.offset {
					continue
				}
				delivery, err := deliveryFromRecord(absoluteOffset, batch.Timestamp, record)
				if err != nil {
					consumer.offset = fetchStart
					return nil, err
				}
				deliveries = append(deliveries, delivery)
				consumer.offset = absoluteOffset + 1
				if len(deliveries) >= maxRecords {
					batchComplete = false
					break
				}
			}
			batchEnd := batch.BaseOffset + uint64(batch.RecordCount)
			if batchComplete && batchEnd > consumer.offset {
				consumer.offset = batchEnd
			}
			if len(deliveries) >= maxRecords {
				break
			}
		}
		if consumer.offset == startOffset {
			break
		}
	}
	return deliveries, nil
}

// Poll is the legacy record-only fetch API.
//
// Deprecated: use Fetch and CommitOffset with explicit Delivery offsets.
func (consumer *Consumer) Poll(maxRecords int) ([]Record, error) {
	deliveries, err := consumer.Fetch(context.Background(), maxRecords)
	if err != nil {
		return nil, err
	}
	records := make([]Record, len(deliveries))
	for index, delivery := range deliveries {
		records[index] = delivery.record()
	}
	return records, nil
}

// CommitOffset fsyncs nextOffset as this group's next delivery. Commits are
// monotonic and cannot move beyond records fetched by this consumer.
func (consumer *Consumer) CommitOffset(ctx context.Context, nextOffset uint64) error {
	if ctx == nil {
		return fmt.Errorf("%w: nil context", ErrInvalidConfig)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	consumer.mu.Lock()
	defer consumer.mu.Unlock()
	if consumer.closed {
		return ErrClosed
	}
	if nextOffset == consumer.committedOffset {
		return nil
	}
	if nextOffset < consumer.committedOffset || nextOffset > consumer.offset {
		return ErrInvalidOffsetCommit
	}
	if nextOffset > consumer.log.NewestOffset() {
		return ErrInvalidOffsetCommit
	}
	if err := consumer.offsetStore.Commit(consumer.group, consumer.topic, nextOffset); err != nil {
		return err
	}
	consumer.committedOffset = nextOffset
	return nil
}

// Commit persists the current cursor.
//
// Deprecated: use CommitOffset with an explicit processed Delivery offset.
func (consumer *Consumer) Commit() error {
	consumer.mu.Lock()
	nextOffset := consumer.offset
	consumer.mu.Unlock()
	return consumer.CommitOffset(context.Background(), nextOffset)
}

// Rollback restores the fetch cursor to the last committed offset.
func (consumer *Consumer) Rollback() error {
	consumer.mu.Lock()
	defer consumer.mu.Unlock()
	if consumer.closed {
		return ErrClosed
	}
	consumer.offset = consumer.committedOffset
	return nil
}

// Replay moves only the in-memory cursor. It never changes durable progress.
func (consumer *Consumer) Replay(offset uint64) error {
	consumer.mu.Lock()
	defer consumer.mu.Unlock()
	if consumer.closed {
		return ErrClosed
	}
	oldest := consumer.log.OldestOffset()
	newest := consumer.log.NewestOffset()
	if offset < oldest || offset > newest {
		return ErrOffsetNotFound
	}
	consumer.offset = offset
	return nil
}

// Nack writes the failed record to the configured DLQ with fsync durability.
//
// Deprecated: use NackContext so cancellation and tracing remain attached.
func (consumer *Consumer) Nack(record Record) error {
	return consumer.NackContext(context.Background(), record)
}

// NackContext writes the failed record to the configured DLQ with fsync
// durability. It returns ErrDLQNotConfigured instead of silently dropping a
// record when no DLQ is attached.
func (consumer *Consumer) NackContext(ctx context.Context, record Record) error {
	return consumer.nackContext(ctx, record, nil)
}

// NackDeliveryContext durably quarantines one fetched delivery and preserves
// its source offset/timestamp for replay tooling and operator diagnosis.
func (consumer *Consumer) NackDeliveryContext(ctx context.Context, delivery Delivery) error {
	record := Record{Key: delivery.Key, Value: delivery.Value, Headers: delivery.Headers}
	return consumer.nackContext(ctx, record, []Header{
		{Key: []byte(dlqOriginalOffsetKey), Value: []byte(strconv.FormatUint(delivery.Offset, 10))},
		{Key: []byte(dlqOriginalTimestampKey), Value: []byte(strconv.FormatInt(delivery.Timestamp, 10))},
	})
}

func (consumer *Consumer) nackContext(ctx context.Context, record Record, sourceHeaders []Header) error {
	if ctx == nil {
		return fmt.Errorf("%w: nil context", ErrInvalidConfig)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	consumer.mu.Lock()
	if consumer.closed {
		consumer.mu.Unlock()
		return ErrClosed
	}
	dlq := consumer.dlq
	topic := consumer.topic
	consumer.mu.Unlock()

	if dlq == nil {
		consumer.metrics.dropHook(topic, "no DLQ configured")
		return ErrDLQNotConfigured
	}
	headers := make([]Header, len(record.Headers)+1+len(sourceHeaders))
	copy(headers, record.Headers)
	headers[len(record.Headers)] = Header{
		Key:   []byte(dlqOriginalTopicKey),
		Value: []byte(topic),
	}
	copy(headers[len(record.Headers)+1:], sourceHeaders)
	err := dlq.SendContext(ctx, record.Key, record.Value, headers, AckFsync)
	if err != nil {
		consumer.metrics.dropHook(topic, "DLQ send failed")
	}
	return err
}

// Seek is the legacy replay alias. It is range-checked and never changes the
// durable committed offset.
//
// Deprecated: use Replay.
func (consumer *Consumer) Seek(offset uint64) error {
	return consumer.Replay(offset)
}

func (consumer *Consumer) SeekToBeginning() {
	_ = consumer.Replay(consumer.log.OldestOffset())
}

func (consumer *Consumer) SeekToEnd() {
	_ = consumer.Replay(consumer.log.NewestOffset())
}

// Offset returns the current in-memory fetch cursor.
func (consumer *Consumer) Offset() uint64 {
	consumer.mu.Lock()
	defer consumer.mu.Unlock()
	return consumer.offset
}

// CommittedOffset returns the last fsynced group offset.
func (consumer *Consumer) CommittedOffset() uint64 {
	consumer.mu.Lock()
	defer consumer.mu.Unlock()
	return consumer.committedOffset
}

// Close releases exclusive group ownership. It is concurrent-idempotent.
func (consumer *Consumer) Close() error {
	consumer.closeOnce.Do(func() {
		consumer.mu.Lock()
		consumer.closed = true
		lease := consumer.lease
		onClose := consumer.onClose
		done := consumer.done
		consumer.mu.Unlock()

		if done != nil {
			close(done)
		}
		consumer.closeErr = lease.Close()
		if onClose != nil {
			onClose()
		}
	})
	return consumer.closeErr
}
