package consumer

import "time"

const (
	// MessageIDHeader carries a stable producer-assigned message ID across redrive.
	MessageIDHeader = "message-id"
	// OutboxMessageIDHeader preserves compatibility with existing Forge outbox records.
	OutboxMessageIDHeader = "go-common.outbox.message-id"

	defaultWorkers       = 1
	defaultQueueCapacity = 256
	defaultBatchSize     = 128
	defaultBatchWait     = 5 * time.Millisecond
	defaultPriorityBurst = 16
	maxWorkers           = 1024
	maxQueueCapacity     = 1_000_000
	maxBatchSize         = 10_000
	defaultStream        = "\x00"
)
