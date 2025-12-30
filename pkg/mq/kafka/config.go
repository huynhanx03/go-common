package kafka

// Config holds Kafka configuration
type Config struct {
	Brokers      []string       // Brokers list of Kafka brokers
	ClientID     string         // ClientID helpful for tracing
	ProducerInfo ProducerConfig // Producer Config
	ConsumerInfo ConsumerConfig // Consumer Config
}

type ProducerConfig struct {
	FlushFrequency  int  // FlushFrequency puts a cap on how long to wait for validation
	FlushBytes      int  // FlushBytes puts a cap on how many bytes to accumulate
	MaxMessageBytes int  // MaxMessageBytes defaults to 1MB
	MaxRetries      int  // MaxRetries for SyncProducer
	RetryBackoff    int  // RetryBackoff for Sync Producer (milliseconds)
	ReturnSuccesses bool // ReturnSuccesses must be true to handle Successes channel
}

type ConsumerConfig struct {
	InitialOffset     int64 // InitialOffset: OffsetOldest or OffsetNewest
	SessionTimeout    int   // SessionTimeout for rebalancing
	MaxProcessingTime int   // MaxProcessingTime for poll interval
}
