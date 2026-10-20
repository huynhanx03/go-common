package forge

import (
	"context"
	"fmt"
	"sort"
)

// TopicStats is a consistent snapshot of one topic.
type TopicStats struct {
	Topic        string
	OldestOffset uint64
	NewestOffset uint64
	Segments     int
	StorageBytes int64
	StorageLimit int64
}

// Check implements the common health-checker shape without importing a health
// framework. It verifies ownership and open topic logs.
func (broker *Broker) Check(ctx context.Context) error {
	if broker == nil {
		return ErrClosed
	}
	if ctx == nil {
		return fmt.Errorf("%w: nil context", ErrInvalidConfig)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	broker.mu.RLock()
	defer broker.mu.RUnlock()
	if broker.closed {
		return ErrClosed
	}
	for _, topic := range broker.topics {
		topic.log.mu.RLock()
		closed := topic.log.closed
		storageErr := topic.log.storageErr
		topic.log.mu.RUnlock()
		if closed {
			return ErrClosed
		}
		if storageErr != nil {
			return storageErr
		}
	}
	return nil
}

// Stats returns deterministic topic snapshots.
func (broker *Broker) Stats(ctx context.Context) ([]TopicStats, error) {
	if err := broker.Check(ctx); err != nil {
		return nil, err
	}
	broker.mu.RLock()
	topics := make([]*Topic, 0, len(broker.topics))
	for _, topic := range broker.topics {
		topics = append(topics, topic)
	}
	broker.mu.RUnlock()
	sort.Slice(topics, func(left, right int) bool {
		return topics[left].name < topics[right].name
	})

	stats := make([]TopicStats, 0, len(topics))
	for _, topic := range topics {
		topic.log.mu.RLock()
		stats = append(stats, TopicStats{
			Topic:        topic.name,
			OldestOffset: topic.log.oldestOffsetLocked(),
			NewestOffset: topic.log.nextOffset.Load(),
			Segments:     len(topic.log.segments),
			StorageBytes: topic.log.totalLogBytesLocked(),
			StorageLimit: topic.log.config.MaxStorageBytes,
		})
		topic.log.mu.RUnlock()
	}
	return stats, nil
}

// ConsumerLag returns the number of records after a group's durable offset.
func (broker *Broker) ConsumerLag(ctx context.Context, group, topic string) (uint64, error) {
	if err := broker.Check(ctx); err != nil {
		return 0, err
	}
	if !validResourceName(group) || !validResourceName(topic) {
		return 0, fmt.Errorf("%w: consumer identity", ErrInvalidConfig)
	}
	broker.mu.RLock()
	topicState := broker.topics[topic]
	broker.mu.RUnlock()
	if topicState == nil {
		return 0, ErrOffsetNotFound
	}
	committed, err := broker.offsetStore.Load(group, topic)
	if err != nil {
		return 0, err
	}
	newest := topicState.log.NewestOffset()
	if committed > newest {
		return 0, ErrInvalidOffsetCommit
	}
	return newest - committed, nil
}
