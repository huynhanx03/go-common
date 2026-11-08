package kafka

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/IBM/sarama"

	"github.com/huynhanx03/go-common/pkg/algorithm"
	"github.com/huynhanx03/go-common/pkg/mq/consumer"
	"github.com/huynhanx03/go-common/pkg/timer"
)

// consumerGroup wraps sarama.ConsumerGroup
type consumerGroup struct {
	client      sarama.ConsumerGroup
	mu          sync.Mutex
	started     bool
	cancel      context.CancelFunc
	middlewares []Middleware
	wg          sync.WaitGroup
	done        chan struct{}
	closeOnce   sync.Once
	closeErr    error
}

// NewConsumer creates a new Consumer Group
func NewConsumer(cfg *Config, groupID string, mws ...Middleware) (ResultConsumerGroup, error) {
	config := sarama.NewConfig()
	config.ClientID = cfg.ClientID

	// Rebalance Strategy
	config.Consumer.Group.Rebalance.Strategy = sarama.NewBalanceStrategySticky()
	config.Consumer.Offsets.Initial = sarama.OffsetOldest
	config.Consumer.Group.Session.Timeout = timer.ToDurationMs(cfg.ConsumerInfo.SessionTimeout)
	if cfg.ConsumerInfo.MaxProcessingTime > 0 {
		config.Consumer.MaxProcessingTime = timer.ToDurationMs(cfg.ConsumerInfo.MaxProcessingTime)
	}

	client, err := sarama.NewConsumerGroup(cfg.Brokers, groupID, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create consumer group: %w", err)
	}

	return &consumerGroup{
		client:      client,
		middlewares: mws,
		done:        make(chan struct{}),
	}, nil
}

// Start consumes messages from the configured topics
func (c *consumerGroup) Start(ctx context.Context, topics []string, handler Handler, errHandler ErrorHandler) error {
	if handler == nil {
		return consumer.ErrInvalidConfig
	}
	wrappedHandler := Chain(handler, c.middlewares...)
	resultHandler := func(ctx context.Context, message consumer.Message) (consumer.Outcome, error) {
		if err := wrappedHandler(ctx, message.Key, message.Value); err != nil {
			return consumer.Retry, err
		}
		return consumer.Success, nil
	}
	return c.startHandler(ctx, topics, resultHandler, errHandler, nil)
}

func (c *consumerGroup) StartWithResult(
	ctx context.Context,
	topics []string,
	handler consumer.Handler,
	errHandler ErrorHandler,
	deadLetterHandler consumer.DeadLetterHandler,
	middlewares ...consumer.Middleware,
) error {
	if handler == nil {
		return consumer.ErrInvalidConfig
	}
	return c.startHandler(
		ctx,
		topics,
		consumer.Chain(handler, middlewares...),
		errHandler,
		deadLetterHandler,
	)
}

// StartWithRuntime uses a caller-owned processing runtime. Stop the consumer
// group before closing that runtime.
func (c *consumerGroup) StartWithRuntime(
	ctx context.Context,
	topics []string,
	runtime *consumer.Runtime,
	errHandler ErrorHandler,
	deadLetterHandler consumer.DeadLetterHandler,
) error {
	return c.start(ctx, topics, runtime, errHandler, deadLetterHandler, false)
}

func (c *consumerGroup) startHandler(
	ctx context.Context, topics []string, handler consumer.Handler,
	errHandler ErrorHandler, deadLetterHandler consumer.DeadLetterHandler,
) error {
	processor, err := consumer.New(handler, consumer.WithWorkers(runtime.GOMAXPROCS(0)))
	if err != nil {
		return err
	}
	if err := c.start(ctx, topics, processor, errHandler, deadLetterHandler, true); err != nil {
		_ = processor.Close(context.Background())
		return err
	}
	return nil
}

func (c *consumerGroup) start(
	ctx context.Context,
	topics []string,
	runtime *consumer.Runtime,
	errHandler ErrorHandler,
	deadLetterHandler consumer.DeadLetterHandler,
	owned bool,
) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.isClosed() {
		return consumer.ErrClosed
	}
	if c.started {
		return ErrConsumerStarted
	}
	c.started = true
	ctx, c.cancel = context.WithCancel(ctx)
	groupHandler := &consumerGroupHandler{
		runtime:           runtime,
		errHandler:        errHandler,
		deadLetterHandler: deadLetterHandler,
		retryRequested:    make(chan struct{}, 1),
	}

	// Loop to handle rebalances
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer c.cancel()
		if owned {
			defer runtime.Close(context.Background())
		}
		attempts := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-c.done:
				return
			default:
			}

			err := c.client.Consume(ctx, topics, groupHandler)
			retry := false
			select {
			case <-groupHandler.retryRequested:
				retry = true
			default:
			}

			if err != nil && !retry && ctx.Err() == nil && !c.isClosed() {
				if errHandler != nil {
					errHandler(err)
				}
				retry = true
			}

			if retry {
				sleepTime := algorithm.CalculateBackoffByTime(attempts, 1*time.Second, 30*time.Second)
				timer := time.NewTimer(sleepTime)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-c.done:
					timer.Stop()
					return
				case <-timer.C:
				}
				attempts++
			} else {
				attempts = 0
			}
		}
	}()

	return nil
}

// Close closes the consumer group
func (c *consumerGroup) Close() error {
	c.mu.Lock()
	c.closeOnce.Do(func() {
		close(c.done)
		if c.cancel != nil {
			c.cancel()
		}
		c.closeErr = c.client.Close()
	})
	c.mu.Unlock()
	c.wg.Wait()
	return c.closeErr
}

func (c *consumerGroup) isClosed() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}
