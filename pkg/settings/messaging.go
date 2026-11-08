package settings

// Kafka is the configuration for Kafka.
type Kafka struct {
	Brokers               []string `mapstructure:"brokers"`
	FlushFrequency        int      `mapstructure:"flush_frequency"`         // Milliseconds
	FlushBytes            int      `mapstructure:"flush_bytes"`             // Bytes
	MaxMessageBytes       int      `mapstructure:"max_message_bytes"`       // Bytes
	Timeout               int      `mapstructure:"timeout"`                 // Seconds
	MaxRetries            int      `mapstructure:"max_retries"`             // Number of retries
	RetryBackoff          int      `mapstructure:"retry_backoff"`           // Milliseconds
	MaxProcessingTime     int      `mapstructure:"max_processing_time"`     // Milliseconds
	ConsumerBatchSize     int      `mapstructure:"consumer_batch_size"`     // Number of messages
	ConsumerBatchInterval int      `mapstructure:"consumer_batch_interval"` // Milliseconds
}
