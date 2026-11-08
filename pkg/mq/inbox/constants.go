package inbox

const (
	// TableName is the canonical inbox table name.
	TableName = "mq_inbox"

	// ConsumerColumn identifies the logical handler that owns the deduplication key.
	ConsumerColumn = "consumer"
	// MessageIDColumn stores the stable source message identity.
	MessageIDColumn = "message_id"
	// CreatedAtColumn supports inbox retention cleanup.
	CreatedAtColumn = "created_at"
)
