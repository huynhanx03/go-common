package kafka

import (
	"fmt"

	"github.com/IBM/sarama"
)

// consumerGroupHandler implements sarama.ConsumerGroupHandler
type consumerGroupHandler struct {
	handlerFunc Handler
	errHandler  ErrorHandler
}

// Setup is called before the consumer group session starts
func (consumerGroupHandler) Setup(_ sarama.ConsumerGroupSession) error { return nil }

// Cleanup is called after the consumer group session ends
func (consumerGroupHandler) Cleanup(_ sarama.ConsumerGroupSession) error { return nil }

// ConsumeClaim processes messages from the consumer group
func (h consumerGroupHandler) ConsumeClaim(sess sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for msg := range claim.Messages() {
		ctx := buildContext(msg.Headers)
		if err := h.handlerFunc(ctx, msg.Key, msg.Value); err != nil {
			if h.errHandler != nil {
				h.errHandler(fmt.Errorf("process message error: %w", err))
			}

			// Mark message to avoid deadlock, depends on policy of project to change
		}
		sess.MarkMessage(msg, "")
	}
	return nil
}
