package forge

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/huynhanx03/go-common/pkg/mq/consumer"
)

// Handle runs a shared consumer handler and applies its outcome to this
// delivery. A retry leaves the source offset uncommitted.
func (c *Consumer) Handle(
	ctx context.Context,
	delivery Delivery,
	handler consumer.Handler,
) (consumer.Outcome, error) {
	c.handleMu.Lock()
	defer c.handleMu.Unlock()

	c.mu.Lock()
	expectedOffset := c.committedOffset
	c.mu.Unlock()
	if delivery.Offset != expectedOffset {
		return c.retry(ErrInvalidOffsetCommit)
	}

	outcome, handlerErr := handler(ctx, toConsumerMessage(c.topic, delivery))
	return c.settle(ctx, delivery, consumer.Result{Outcome: outcome, Err: handlerErr})
}

// Run fetches bounded windows and settles only their completed prefix. Retry
// returns control to the caller for backoff/redelivery. The caller owns runtime.
// Run exclusively owns fetching and offset changes until it returns.
func (c *Consumer) Run(ctx context.Context, runtime *consumer.Runtime) error {
	c.handleMu.Lock()
	defer c.handleMu.Unlock()
	if err := c.Rollback(); err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-c.done:
			cancel()
		case <-runCtx.Done():
		}
	}()
	limit, _ := runtime.Window()
	for {
		deliveries, err := c.FetchWait(runCtx, limit, DefaultFetchWaitInterval)
		if err != nil {
			return errors.Join(err, c.Rollback())
		}
		messages := make([]consumer.Message, len(deliveries))
		for i, delivery := range deliveries {
			messages[i] = toConsumerMessage(c.topic, delivery)
		}
		results := runtime.Process(runCtx, messages)
		nextOffset := c.CommittedOffset()
		var failure error
		for i, result := range results {
			outcome, err := c.prepare(runCtx, deliveries[i], result)
			if outcome == consumer.Retry {
				failure = err
				break
			}
			nextOffset = deliveries[i].Offset + 1
		}
		if err := c.CommitOffset(runCtx, nextOffset); err != nil {
			failure = errors.Join(failure, err)
		}
		if failure != nil {
			return errors.Join(failure, c.Rollback())
		}
	}
}

func (c *Consumer) settle(ctx context.Context, delivery Delivery, result consumer.Result) (consumer.Outcome, error) {
	outcome, err := c.prepare(ctx, delivery, result)
	if outcome == consumer.Retry {
		return c.retry(err)
	}
	if err := c.CommitOffset(ctx, delivery.Offset+1); err != nil {
		return c.retry(err)
	}
	return outcome, nil
}

func (c *Consumer) prepare(ctx context.Context, delivery Delivery, result consumer.Result) (consumer.Outcome, error) {
	switch result.Outcome {
	case consumer.Success:
		if result.Err != nil {
			return consumer.Retry, result.Err
		}
	case consumer.Retry:
		if result.Err == nil {
			return consumer.Retry, consumer.ErrRetryRequested
		}
		return consumer.Retry, result.Err
	case consumer.DeadLetter:
		if err := c.NackDeliveryContext(ctx, delivery); err != nil {
			return consumer.Retry, errors.Join(result.Err, err)
		}
	default:
		return consumer.Retry, errors.Join(consumer.ErrInvalidOutcome, result.Err)
	}
	return result.Outcome, nil
}

func (c *Consumer) retry(err error) (consumer.Outcome, error) {
	return consumer.Retry, errors.Join(err, c.Rollback())
}

func toConsumerMessage(topic string, delivery Delivery) consumer.Message {
	headers := make([]consumer.Header, len(delivery.Headers))
	for index, header := range delivery.Headers {
		headers[index] = consumer.Header{Key: string(header.Key), Value: header.Value}
	}
	messageID := consumer.IDFromHeaders(headers)
	if messageID == "" {
		messageID = fmt.Sprintf("forge:%s:%d", topic, delivery.Offset)
	}

	return consumer.Message{
		ID:        messageID,
		Topic:     topic,
		Stream:    "forge:" + topic,
		Key:       delivery.Key,
		Value:     delivery.Value,
		Headers:   headers,
		Timestamp: time.Unix(0, delivery.Timestamp),
	}
}
