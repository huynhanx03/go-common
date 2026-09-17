package forge

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Broker is the entry point for Forge MQ.
// It manages topics, producers, and consumers.
type Broker struct {
	mu          sync.RWMutex
	groupMu     sync.Mutex
	producerMu  sync.Mutex
	dataDir     string
	topics      map[string]*Topic
	offsetStore *OffsetStore
	config      Config
	closed      bool
	stopCh      chan struct{}
	wg          sync.WaitGroup
	producers   map[*Producer]struct{}
	consumers   map[*Consumer]struct{}
	closeOnce   sync.Once
	closeDone   chan struct{}
	closeErr    error
	lease       *consumerLease
}

// NewBroker creates a broker rooted at the given data directory.
func NewBroker(dataDir string, opts ...Option) (*Broker, error) {
	if dataDir == "" {
		return nil, fmt.Errorf("%w: broker directory", ErrInvalidConfig)
	}
	cfg := defaultConfig()
	for _, o := range opts {
		if err := applyOption(o, &cfg); err != nil {
			return nil, err
		}
	}
	cfg.fileOps = cfg.fileOps.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	if err := ensurePrivateDirectory(dataDir); err != nil {
		return nil, fmt.Errorf("forge: mkdir broker: %w", err)
	}
	if err := cfg.fileOps.syncDir(filepath.Dir(dataDir)); err != nil {
		return nil, fmt.Errorf("forge: sync broker parent: %w", err)
	}
	lease, err := acquireBrokerLease(dataDir)
	if err != nil {
		return nil, err
	}

	topicsDir := filepath.Join(dataDir, "topics")
	offsetsDir := filepath.Join(dataDir, "offsets")

	if err := ensurePrivateDirectory(topicsDir); err != nil {
		_ = lease.Close()
		return nil, fmt.Errorf("forge: mkdir topics: %w", err)
	}

	store, err := NewOffsetStore(offsetsDir)
	if err != nil {
		_ = lease.Close()
		return nil, err
	}
	if err := cfg.fileOps.syncDir(dataDir); err != nil {
		_ = lease.Close()
		return nil, fmt.Errorf("forge: sync broker directory: %w", err)
	}
	store.maxGroups = cfg.MaxConsumerGroups

	b := &Broker{
		dataDir:     dataDir,
		topics:      make(map[string]*Topic),
		offsetStore: store,
		config:      cfg,
		stopCh:      make(chan struct{}),
		producers:   make(map[*Producer]struct{}),
		consumers:   make(map[*Consumer]struct{}),
		closeDone:   make(chan struct{}),
		lease:       lease,
	}

	// Auto-load existing topics from disk.
	if err := b.loadTopics(topicsDir); err != nil {
		for _, topic := range b.topics {
			_ = topic.Close()
		}
		_ = lease.Close()
		return nil, err
	}

	// Start the background whole-segment retention loop.
	if cfg.RetentionInterval > 0 {
		b.startRetentionLoop()
	}

	return b, nil
}

// loadTopics discovers existing topic directories and opens them.
func (b *Broker) loadTopics(topicsDir string) error {
	return forEachDirectoryEntry(topicsDir, func(e os.DirEntry) error {
		if !e.IsDir() {
			return nil
		}
		name := e.Name()
		if !validResourceName(name) {
			return fmt.Errorf("forge: invalid topic directory")
		}
		if len(b.topics) >= b.config.MaxTopics {
			return ErrResourceLimit
		}
		if _, err := b.getOrCreateTopicLocked(name); err != nil {
			return fmt.Errorf("forge: load topic %q: %w", name, err)
		}
		return nil
	})
}

// getTopic returns an existing topic or nil. Caller must hold at least RLock.
func (b *Broker) getTopic(name string) *Topic {
	return b.topics[name]
}

// createTopic creates a new topic. Caller must hold write Lock.
func (b *Broker) createTopic(name string) (*Topic, error) {
	if len(b.topics) >= b.config.MaxTopics {
		return nil, ErrResourceLimit
	}
	dir := filepath.Join(b.dataDir, "topics", name)
	log, err := NewCommitLog(dir, func(config *Config) error {
		*config = b.config
		return nil
	})
	if err != nil {
		return nil, err
	}
	t := &Topic{name: name, log: log, wake: make(chan struct{})}
	b.topics[name] = t
	return t, nil
}

// getOrCreateTopicLocked returns an existing topic or creates a new one. Caller must hold write Lock.
func (b *Broker) getOrCreateTopicLocked(name string) (*Topic, error) {
	if t := b.getTopic(name); t != nil {
		return t, nil
	}
	return b.createTopic(name)
}

// ensureTopic uses double-checked locking: fast RLock path for existing topics,
// upgrades to write Lock only for cold topic creation (disk I/O).
func (b *Broker) ensureTopic(name string) (*Topic, error) {
	// Fast path: topic already exists.
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return nil, ErrClosed
	}
	if t := b.getTopic(name); t != nil {
		b.mu.RUnlock()
		return t, nil
	}
	b.mu.RUnlock()

	// Slow path: acquire write lock and create topic.
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, ErrClosed
	}
	return b.getOrCreateTopicLocked(name)
}

// NewProducer creates a producer for the given topic.
func (b *Broker) NewProducer(topic string, opts ...ProducerOption) (*Producer, error) {
	if !validResourceName(topic) {
		return nil, fmt.Errorf("%w: topic", ErrInvalidConfig)
	}
	config, err := resolveProducerConfig(opts...)
	if err != nil {
		return nil, err
	}
	config.Topic = topic
	b.producerMu.Lock()
	defer b.producerMu.Unlock()
	if err := b.checkProducerCapacity(); err != nil {
		return nil, err
	}
	t, err := b.ensureTopic(topic)
	if err != nil {
		return nil, err
	}

	// The broker owns the metric identity; a custom ProducerOption cannot
	// relabel queue activity as another topic.
	producer, err := newProducer(t.log, config)
	if err != nil {
		return nil, err
	}
	producer.onAppend = t.signal
	if err := b.registerProducer(producer); err != nil {
		_ = producer.Close()
		return nil, err
	}
	return producer, nil
}

// NewConsumer creates a consumer for the given group and topic.
func (b *Broker) NewConsumer(group, topic string, opts ...ConsumerOption) (*Consumer, error) {
	if !validResourceName(group) || !validResourceName(topic) {
		return nil, fmt.Errorf("%w: consumer group or topic", ErrInvalidConfig)
	}
	config, err := resolveConsumerConfig(opts...)
	if err != nil {
		return nil, err
	}
	b.groupMu.Lock()
	defer b.groupMu.Unlock()
	if err := b.checkConsumerCapacity(); err != nil {
		return nil, err
	}
	t, err := b.ensureTopic(topic)
	if err != nil {
		return nil, err
	}

	consumer, err := newConsumer(t.log, group, topic, b.offsetStore, config)
	if err != nil {
		return nil, err
	}
	consumer.waitSignal = t.waitChannel
	if err := b.registerConsumer(consumer); err != nil {
		_ = consumer.Close()
		return nil, err
	}
	return consumer, nil
}

// DeleteConsumerGroup removes a closed group's durable progress so it no
// longer pins queue retention. Active groups return ErrConsumerBusy.
func (b *Broker) DeleteConsumerGroup(group, topic string) error {
	if !validResourceName(group) || !validResourceName(topic) {
		return fmt.Errorf("%w: consumer group or topic", ErrInvalidConfig)
	}
	b.groupMu.Lock()
	defer b.groupMu.Unlock()

	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return ErrClosed
	}
	for consumer := range b.consumers {
		consumer.mu.Lock()
		active := !consumer.closed && consumer.group == group && consumer.topic == topic
		consumer.mu.Unlock()
		if active {
			b.mu.RUnlock()
			return ErrConsumerBusy
		}
	}
	b.mu.RUnlock()
	return b.offsetStore.deleteGroup(group, topic)
}

// NewDLQConsumer creates a consumer with a dead-letter queue for the given group/topic.
// Failed messages (via Nack) are routed to "{topic}.dlq".
func (b *Broker) NewDLQConsumer(group, topic string, opts ...ConsumerOption) (*Consumer, *Producer, error) {
	if !validResourceName(group) || !validResourceName(topic) {
		return nil, nil, fmt.Errorf("%w: consumer group or topic", ErrInvalidConfig)
	}
	config, err := resolveConsumerConfig(opts...)
	if err != nil {
		return nil, nil, err
	}
	b.groupMu.Lock()
	defer b.groupMu.Unlock()
	if err := b.checkConsumerCapacity(); err != nil {
		return nil, nil, err
	}
	dlqTopic := topic + dlqSuffix
	dlqProducer, err := b.NewProducer(dlqTopic)
	if err != nil {
		return nil, nil, fmt.Errorf("forge: create DLQ producer: %w", err)
	}

	// The generated route is part of this constructor's contract and cannot be
	// replaced by a caller-supplied option.
	config.dlq = dlqProducer
	t, topicErr := b.ensureTopic(topic)
	if topicErr != nil {
		_ = dlqProducer.Close()
		return nil, nil, topicErr
	}
	consumer, err := newConsumer(t.log, group, topic, b.offsetStore, config)
	if err == nil {
		consumer.waitSignal = t.waitChannel
		err = b.registerConsumer(consumer)
	}
	if err != nil {
		if consumer != nil {
			_ = consumer.Close()
		}
		_ = dlqProducer.Close()
		return nil, nil, err
	}

	return consumer, dlqProducer, nil
}

func (b *Broker) checkConsumerCapacity() error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return ErrClosed
	}
	if len(b.consumers) >= b.config.MaxConsumers {
		return ErrResourceLimit
	}
	return nil
}

func (b *Broker) checkProducerCapacity() error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return ErrClosed
	}
	if len(b.producers) >= b.config.MaxProducers {
		return ErrResourceLimit
	}
	return nil
}

func (b *Broker) registerProducer(producer *Producer) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return ErrClosed
	}
	if len(b.producers) >= b.config.MaxProducers {
		return ErrResourceLimit
	}
	b.producers[producer] = struct{}{}
	producer.onClose = func() {
		b.mu.Lock()
		delete(b.producers, producer)
		b.mu.Unlock()
	}
	return nil
}

func (b *Broker) registerConsumer(consumer *Consumer) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return ErrClosed
	}
	if len(b.consumers) >= b.config.MaxConsumers {
		return ErrResourceLimit
	}
	b.consumers[consumer] = struct{}{}
	consumer.onClose = func() {
		b.mu.Lock()
		delete(b.consumers, consumer)
		b.mu.Unlock()
	}
	return nil
}

func validResourceName(name string) bool {
	if len(name) == 0 || len(name) > 249 || name == "." || name == ".." {
		return false
	}
	for index := 0; index < len(name); index++ {
		character := name[index]
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '.' ||
			character == '-' ||
			character == '_' {
			continue
		}
		return false
	}
	return true
}

// Topics returns the names of all known topics.
func (b *Broker) Topics() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	names := make([]string, 0, len(b.topics))
	for name := range b.topics {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Close stops the retention goroutine, then shuts down all topics.
func (b *Broker) Close() error {
	b.closeOnce.Do(func() {
		b.closeErr = b.close()
		close(b.closeDone)
	})
	<-b.closeDone
	return b.closeErr
}

func (b *Broker) close() error {
	b.groupMu.Lock()
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	b.groupMu.Unlock()

	// Signal retention loop to stop and wait for it.
	close(b.stopCh)
	b.wg.Wait()

	b.mu.RLock()
	producers := make([]*Producer, 0, len(b.producers))
	for producer := range b.producers {
		producers = append(producers, producer)
	}
	consumers := make([]*Consumer, 0, len(b.consumers))
	for consumer := range b.consumers {
		consumers = append(consumers, consumer)
	}
	topics := make([]*Topic, 0, len(b.topics))
	for _, topic := range b.topics {
		topics = append(topics, topic)
	}
	b.mu.RUnlock()

	var closeErrors []error
	for _, producer := range producers {
		closeErrors = append(closeErrors, producer.Close())
	}
	for _, consumer := range consumers {
		closeErrors = append(closeErrors, consumer.Close())
	}
	for _, topic := range topics {
		closeErrors = append(closeErrors, topic.Close())
	}
	closeErrors = append(closeErrors, b.lease.Close())
	return errors.Join(closeErrors...)
}

func acquireBrokerLease(dataDir string) (*consumerLease, error) {
	file, err := os.OpenFile(
		filepath.Join(dataDir, ".broker.lock"),
		os.O_CREATE|os.O_RDWR,
		filePerm,
	)
	if err != nil {
		return nil, err
	}
	if err := ensurePrivateFile(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := tryLockConsumerFile(file); err != nil {
		_ = file.Close()
		if errors.Is(err, ErrConsumerBusy) {
			return nil, ErrBrokerBusy
		}
		return nil, err
	}
	return &consumerLease{file: file}, nil
}
