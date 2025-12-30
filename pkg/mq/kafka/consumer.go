package kafka

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/IBM/sarama"
	
	"github.com/huynhanx03/go-common/pkg/utils"
)

// consumerGroup wraps sarama.ConsumerGroup
type consumerGroup struct {
	client      sarama.ConsumerGroup
	topics      []string
	handler     Handler
	errHandler  ErrorHandler
	middlewares []Middleware
	wg          sync.WaitGroup
}

// NewConsumer creates a new Consumer Group
func NewConsumer(cfg *Config, groupID string, mws ...Middleware) (ConsumerGroup, error) {
	config := sarama.NewConfig()
	config.ClientID = cfg.ClientID

	// Rebalance Strategy
	config.Consumer.Group.Rebalance.Strategy = sarama.NewBalanceStrategySticky()
	config.Consumer.Offsets.Initial = sarama.OffsetOldest
	config.Consumer.Group.Session.Timeout = utils.ToDurationMs(cfg.ConsumerInfo.SessionTimeout)
	if cfg.ConsumerInfo.MaxProcessingTime > 0 {
		config.Consumer.MaxProcessingTime = utils.ToDurationMs(cfg.ConsumerInfo.MaxProcessingTime)
	}

	client, err := sarama.NewConsumerGroup(cfg.Brokers, groupID, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create consumer group: %w", err)
	}

	return &consumerGroup{
		client:      client,
		middlewares: mws,
	}, nil
}

// Start consumes messages from the configured topics
func (c *consumerGroup) Start(ctx context.Context, topics []string, handler Handler, errHandler ErrorHandler) error {
	c.topics = topics
	c.handler = handler
	c.errHandler = errHandler

	// Apply Middlewares
	wrappedHandler := Chain(handler, c.middlewares...)

	consumer := &consumerGroupHandler{
		handlerFunc: wrappedHandler,
		errHandler:  errHandler,
	}

	// Loop to handle rebalances
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		attempts := 0
		for {
			err := c.client.Consume(ctx, topics, consumer)
			if err != nil {
				if c.errHandler != nil {
					c.errHandler(err)
				}

				// Backoff before retry
				sleepTime := utils.CalculateBackoffByTime(attempts, 1*time.Second, 30*time.Second)
				time.Sleep(sleepTime)
				attempts++
			} else {
				attempts = 0
			}

			if ctx.Err() != nil {
				return
			}
		}
	}()

	return nil
}

// Close closes the consumer group
func (c *consumerGroup) Close() error {
	err := c.client.Close()
	c.wg.Wait()
	return err
}
