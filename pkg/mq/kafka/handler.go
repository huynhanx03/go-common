package kafka

import (
	"fmt"

	"github.com/IBM/sarama"

	"github.com/huynhanx03/go-common/pkg/mq/consumer"
)

type consumerGroupHandler struct {
	runtime           *consumer.Runtime
	errHandler        ErrorHandler
	deadLetterHandler consumer.DeadLetterHandler
	retryRequested    chan struct{}
}

func (consumerGroupHandler) Setup(_ sarama.ConsumerGroupSession) error { return nil }

func (consumerGroupHandler) Cleanup(_ sarama.ConsumerGroupSession) error { return nil }

func (h consumerGroupHandler) ConsumeClaim(
	session sarama.ConsumerGroupSession,
	claim sarama.ConsumerGroupClaim,
) error {
	limit, wait := h.runtime.Window()
	for {
		messages, err := consumer.Collect(session.Context(), claim.Messages(), limit, wait)
		if err != nil || len(messages) == 0 {
			return nil
		}
		shared := make([]consumer.Message, len(messages))
		for i, message := range messages {
			shared[i] = toConsumerMessage(message)
		}
		results := h.runtime.Process(session.Context(), shared)
		for i, result := range results {
			if session.Context().Err() != nil {
				return nil
			}
			ctx := consumer.MessageContext(session.Context(), shared[i])
			switch result.Outcome {
			case consumer.Success:
			case consumer.DeadLetter:
				if h.deadLetterHandler == nil {
					h.report(consumer.ErrDeadLetterHandlerRequired)
					return h.stopForRetry()
				}
				if err := h.deadLetterHandler(ctx, shared[i], result.Err); err != nil {
					h.report(fmt.Errorf("dead-letter publish: %w", err))
					return h.stopForRetry()
				}
			default:
				h.report(fmt.Errorf("message processing: %w", result.Err))
				return h.stopForRetry()
			}
			session.MarkMessage(messages[i], "")
		}
	}
}

func (h consumerGroupHandler) stopForRetry() error {
	select {
	case h.retryRequested <- struct{}{}:
	default:
	}
	return nil
}

func (h consumerGroupHandler) report(err error) {
	if h.errHandler != nil {
		h.errHandler(err)
	}
}

func toConsumerMessage(message *sarama.ConsumerMessage) consumer.Message {
	headers := make([]consumer.Header, len(message.Headers))
	for index, header := range message.Headers {
		headers[index] = consumer.Header{Key: string(header.Key), Value: header.Value}
	}
	messageID := consumer.IDFromHeaders(headers)
	if messageID == "" {
		messageID = fmt.Sprintf("kafka:%s:%d:%d", message.Topic, message.Partition, message.Offset)
	}

	return consumer.Message{
		ID:        messageID,
		Topic:     message.Topic,
		Stream:    fmt.Sprintf("kafka:%s:%d", message.Topic, message.Partition),
		Key:       message.Key,
		Value:     message.Value,
		Headers:   headers,
		Timestamp: message.Timestamp,
	}
}
