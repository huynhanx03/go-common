package forge

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/huynhanx03/go-common/pkg/common/locks"
)

// Broker is the entry point for Forge MQ.
// It manages topics, producers, and consumers.
type Broker struct {
	mu          locks.RWSpinLocker
	dataDir     string
	topics      map[string]*Topic
	offsetStore *OffsetStore
	config      Config
	closed      bool
	stopCh      chan struct{}
	wg          sync.WaitGroup
}

// NewBroker creates a broker rooted at the given data directory.
func NewBroker(dataDir string, opts ...Option) (*Broker, error) {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}

	topicsDir := filepath.Join(dataDir, "topics")
	offsetsDir := filepath.Join(dataDir, "offsets")

	if err := os.MkdirAll(topicsDir, dirPerm); err != nil {
		return nil, fmt.Errorf("forge: mkdir topics: %w", err)
	}

	store, err := NewOffsetStore(offsetsDir)
	if err != nil {
		return nil, err
	}

	b := &Broker{
		mu:          locks.NewRWSpinLock(),
		dataDir:     dataDir,
		topics:      make(map[string]*Topic),
		offsetStore: store,
		config:      cfg,
		stopCh:      make(chan struct{}),
	}

	// Auto-load existing topics from disk.
	if err := b.loadTopics(topicsDir); err != nil {
		return nil, err
	}

	// Start background retention & merge loop.
	if cfg.RetentionInterval > 0 {
		b.startRetentionLoop()
	}

	return b, nil
}

// loadTopics discovers existing topic directories and opens them.
func (b *Broker) loadTopics(topicsDir string) error {
	entries, err := os.ReadDir(topicsDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if _, err := b.getOrCreateTopicLocked(name); err != nil {
			return fmt.Errorf("forge: load topic %q: %w", name, err)
		}
	}
	return nil
}

// getTopic returns an existing topic or nil. Caller must hold at least RLock.
func (b *Broker) getTopic(name string) *Topic {
	return b.topics[name]
}

// createTopic creates a new topic. Caller must hold write Lock.
func (b *Broker) createTopic(name string) (*Topic, error) {
	dir := filepath.Join(b.dataDir, "topics", name)
	log, err := NewCommitLog(dir, func(c *Config) { *c = b.config })
	if err != nil {
		return nil, err
	}
	t := &Topic{name: name, log: log}
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
	t, err := b.ensureTopic(topic)
	if err != nil {
		return nil, err
	}

	// Inject topic name for metrics reporting.
	opts = append([]ProducerOption{func(c *ProducerConfig) { c.Topic = topic }}, opts...)

	return NewProducer(t.log, opts...), nil
}

// NewConsumer creates a consumer for the given group and topic.
func (b *Broker) NewConsumer(group, topic string, opts ...ConsumerOption) (*Consumer, error) {
	t, err := b.ensureTopic(topic)
	if err != nil {
		return nil, err
	}

	return NewConsumer(t.log, group, topic, b.offsetStore, opts...)
}

// NewDLQConsumer creates a consumer with a dead-letter queue for the given group/topic.
// Failed messages (via Nack) are routed to "{topic}.dlq".
func (b *Broker) NewDLQConsumer(group, topic string, opts ...ConsumerOption) (*Consumer, *Producer, error) {
	t, err := b.ensureTopic(topic)
	if err != nil {
		return nil, nil, err
	}

	dlqTopic := topic + dlqSuffix
	dlqT, err := b.ensureTopic(dlqTopic)
	if err != nil {
		return nil, nil, fmt.Errorf("forge: create DLQ topic: %w", err)
	}
	dlqProducer := NewProducer(dlqT.log, func(c *ProducerConfig) { c.Topic = dlqTopic })

	// Prepend DLQ option.
	opts = append([]ConsumerOption{WithDLQ(dlqProducer)}, opts...)

	consumer, err := NewConsumer(t.log, group, topic, b.offsetStore, opts...)
	if err != nil {
		dlqProducer.Close()
		return nil, nil, err
	}

	return consumer, dlqProducer, nil
}

// Topics returns the names of all known topics.
func (b *Broker) Topics() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	names := make([]string, 0, len(b.topics))
	for name := range b.topics {
		names = append(names, name)
	}
	return names
}

// Close stops the retention goroutine, then shuts down all topics.
func (b *Broker) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	b.mu.Unlock()

	// Signal retention loop to stop and wait for it.
	close(b.stopCh)
	b.wg.Wait()

	b.mu.Lock()
	defer b.mu.Unlock()

	var firstErr error
	for _, t := range b.topics {
		if err := t.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
