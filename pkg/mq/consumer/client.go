package consumer

import (
	"context"
	"sync"
	"time"
)

// FullQueue determines admission when the pending queue has no capacity.
type FullQueue uint8

const (
	Block FullQueue = iota
	Reject
)

type config struct {
	workers, queueCapacity int
	batchSize              int
	batchWait              time.Duration
	fullQueue              FullQueue
	ordering               func(Message) string
	priority               func(Message) int
	priorityBurst          int
}

type Option func(*config)

func WithWorkers(count int) Option {
	return func(c *config) { c.workers = count }
}

func WithQueueCapacity(capacity int) Option {
	return func(c *config) { c.queueCapacity = capacity }
}

func WithFullQueue(policy FullQueue) Option {
	return func(c *config) { c.fullQueue = policy }
}

// WithOrdering serializes messages with the same nonempty key. The function
// must be safe for concurrent calls. An empty key leaves a message unordered.
func WithOrdering(key func(Message) string) Option {
	return func(c *config) { c.ordering = key }
}

func WithUnordered() Option { return WithOrdering(nil) }

// WithPriority prefers higher numbers among ready messages. Ordering within
// each stream is preserved. The function must be safe for concurrent calls.
func WithPriority(priority func(Message) int) Option {
	return func(c *config) { c.priority = priority }
}

// WithPriorityBurst limits how often priority may bypass the oldest ready item.
func WithPriorityBurst(limit int) Option {
	return func(c *config) { c.priorityBurst = limit }
}

// WithBatch sets the maximum batch size and gathering delay for NewBatch.
func WithBatch(size int, wait time.Duration) Option {
	return func(c *config) { c.batchSize, c.batchWait = size, wait }
}

// Runtime owns a bounded queue and fixed workers. A separate instance provides
// isolation for a service, provider, tenant, or workload.
type Runtime struct {
	config       config
	handler      Handler
	batchHandler BatchHandler
	ctx          context.Context
	cancel       context.CancelFunc
	done         chan struct{}
	mu           sync.Mutex
	changed      *sync.Cond
	queue        []*task
	tails        map[string]*task
	closed       bool
	running      int
	burst        int
}

func New(handler Handler, options ...Option) (*Runtime, error) {
	if handler == nil {
		return nil, ErrInvalidConfig
	}
	return newRuntime(handler, nil, options...)
}

func NewBatch(handler BatchHandler, options ...Option) (*Runtime, error) {
	if handler == nil {
		return nil, ErrInvalidConfig
	}
	return newRuntime(nil, handler, options...)
}

func newRuntime(handler Handler, batchHandler BatchHandler, options ...Option) (*Runtime, error) {
	c := config{
		workers: defaultWorkers, queueCapacity: defaultQueueCapacity,
		batchSize: 1, ordering: streamKey, priorityBurst: defaultPriorityBurst,
	}
	if batchHandler != nil {
		c.batchSize, c.batchWait = defaultBatchSize, defaultBatchWait
	}
	for _, option := range options {
		if option == nil {
			return nil, ErrInvalidConfig
		}
		option(&c)
	}
	if c.workers <= 0 || c.workers > maxWorkers ||
		c.queueCapacity <= 0 || c.queueCapacity > maxQueueCapacity ||
		c.batchSize <= 0 || c.batchSize > maxBatchSize || c.batchWait < 0 ||
		c.priorityBurst <= 0 || c.fullQueue > Reject || (handler != nil && (c.batchSize != 1 || c.batchWait != 0)) {
		return nil, ErrInvalidConfig
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &Runtime{
		config: c, handler: handler, batchHandler: batchHandler, ctx: ctx, cancel: cancel, done: make(chan struct{}),
		queue: make([]*task, 0, c.queueCapacity), tails: make(map[string]*task),
	}
	r.changed = sync.NewCond(&r.mu)
	var workers sync.WaitGroup
	workers.Add(c.workers)
	for range c.workers {
		go func() { defer workers.Done(); r.work() }()
	}
	go func() { workers.Wait(); close(r.done) }()
	return r, nil
}

func streamKey(message Message) string {
	if message.Stream != "" {
		return message.Stream
	}
	return defaultStream
}

// Window bounds a source read and its optional batch-gathering delay.
func (r *Runtime) Window() (int, time.Duration) {
	size := r.config.queueCapacity
	if r.config.fullQueue == Block {
		size += r.config.workers * r.config.batchSize
	}
	return min(size, maxBatchSize), r.config.batchWait
}

func (r *Runtime) Stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Stats{Queued: len(r.queue), Running: r.running}
}

// Close stops admission and drains accepted messages. If ctx expires, handler
// contexts are canceled; handlers must cooperate for workers to exit. Repeated
// calls may wait for that remaining work.
func (r *Runtime) Close(ctx context.Context) error {
	r.mu.Lock()
	r.closed = true
	r.changed.Broadcast()
	r.mu.Unlock()
	select {
	case <-r.done:
		r.cancel()
		return nil
	case <-ctx.Done():
		r.cancel()
		r.wake()
		return ctx.Err()
	}
}

func (r *Runtime) wake() {
	r.mu.Lock()
	r.changed.Broadcast()
	r.mu.Unlock()
}
