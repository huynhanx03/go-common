package settings

type Config struct {
	Server        Server        `mapstructure:"server"`
	MongoDB       MongoDB       `mapstructure:"mongodb"`
	Logger        Logger        `mapstructure:"logger"`
	Redis         Redis         `mapstructure:"redis"`
	Kafka         Kafka         `mapstructure:"kafka"`
	Elasticsearch Elasticsearch `mapstructure:"elasticsearch"`
}

// Server is the configuration for the server
type Server struct {
	Mode string `mapstructure:"mode"`
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
}

// MongoDB is the configuration for MongoDB
type MongoDB struct {
	Host            string `mapstructure:"host"`
	Username        string `mapstructure:"username"`
	Password        string `mapstructure:"password"`
	Database        string `mapstructure:"database"`
	MaxPoolSize     uint64 `mapstructure:"max_pool_size"`
	MinPoolSize     uint64 `mapstructure:"min_pool_size"`
	MaxConnIdleTime uint64 `mapstructure:"max_conn_idle_time"`
	Port            int    `mapstructure:"port"`
	Timeout         int    `mapstructure:"timeout"`
}

// Logger is the configuration for the logger
type Logger struct {
	LogLevel    string `mapstructure:"log_level"`
	FileLogName string `mapstructure:"file_log_name"`
	MaxBackups  int    `mapstructure:"max_backups"`
	MaxAge      int    `mapstructure:"max_age"`
	MaxSize     int    `mapstructure:"max_size"`
	Compress    bool   `mapstructure:"compress"`
}

// Redis is the configuration for Redis
type Redis struct {
	Host            string `mapstructure:"host"`
	Password        string `mapstructure:"password"`
	Port            int    `mapstructure:"port"`
	Database        int    `mapstructure:"database"`
	PoolSize        int    `mapstructure:"pool_size"`
	MinIdleConns    int    `mapstructure:"min_idle_conns"`
	PoolTimeout     int    `mapstructure:"pool_timeout"`
	DialTimeout     int    `mapstructure:"dial_timeout"`
	ReadTimeout     int    `mapstructure:"read_timeout"`
	WriteTimeout    int    `mapstructure:"write_timeout"`
	MaxRetries      int    `mapstructure:"max_retries"`
	MaxRetryBackoff int    `mapstructure:"max_retry_backoff"`
	MinRetryBackoff int    `mapstructure:"min_retry_backoff"`
}

// Kafka is the configuration for Kafka
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

// Elasticsearch is the configuration for Elasticsearch
type Elasticsearch struct {
	Addresses []string `mapstructure:"addresses"`
	Username  string   `mapstructure:"username"`
	Password  string   `mapstructure:"password"`
}
