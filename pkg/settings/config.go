package settings

import "crypto/rsa"

type Config struct {
	Server        Server        `mapstructure:"server"`
	MongoDB       MongoDB       `mapstructure:"mongodb"`
	Logger        Logger        `mapstructure:"logger"`
	Redis         Redis         `mapstructure:"redis"`
	Kafka         Kafka         `mapstructure:"kafka"`
	Elasticsearch Elasticsearch `mapstructure:"elasticsearch"`
	WideColumn    WideColumn    `mapstructure:"wide_column"`
	Database      Database      `mapstructure:"database"`
	SnowflakeNode SnowflakeNode `mapstructure:"snowflake_node"`
	JWT           JWT           `mapstructure:"jwt"`
	Services      Services      `mapstructure:"services"`
}

type Services struct {
	IdentityService GRPCService `mapstructure:"identity_service"`
	BillingService  GRPCService `mapstructure:"billing_service"`
}

type GRPCService struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
}

type JWT struct {
	Secret         string          `mapstructure:"secret"`
	PrivateKeyPath string          `mapstructure:"private_key_path"`
	PublicKeyPath  string          `mapstructure:"public_key_path"`
	PrivateKey     *rsa.PrivateKey `mapstructure:"-"`
	PublicKey      *rsa.PublicKey  `mapstructure:"-"`
}

// WideColumn is the configuration for Wide Column databases (Cassandra/ScyllaDB)
type WideColumn struct {
	Hosts    []string `mapstructure:"hosts"`
	Keyspace string   `mapstructure:"keyspace"`
	Username string   `mapstructure:"username"`
	Password string   `mapstructure:"password"`
	Port     int      `mapstructure:"port"`
	Timeout  int      `mapstructure:"timeout"`
	Retries  int      `mapstructure:"retries"`
}

// Database is the configuration for the database
type Database struct {
	Driver          string `mapstructure:"driver"`
	Host            string `mapstructure:"host"`
	Port            int    `mapstructure:"port"`
	Username        string `mapstructure:"username"`
	Password        string `mapstructure:"password"`
	Database        string `mapstructure:"database"`
	MaxOpenConns    int    `mapstructure:"max_open_conns"`
	MaxIdleConns    int    `mapstructure:"max_idle_conns"`
	ConnMaxLifetime int    `mapstructure:"conn_max_lifetime"`
}

// Server is the configuration for the server
type Server struct {
	Mode     string `mapstructure:"mode"`
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	GRPCPort int    `mapstructure:"grpc_port"`
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
	Addrs           []string `mapstructure:"addrs"`
	MasterName      string   `mapstructure:"master_name"`
	Password        string   `mapstructure:"password"`
	Database        int      `mapstructure:"database"`
	PoolSize        int      `mapstructure:"pool_size"`
	MinIdleConns    int      `mapstructure:"min_idle_conns"`
	PoolTimeout     int      `mapstructure:"pool_timeout"`
	DialTimeout     int      `mapstructure:"dial_timeout"`
	ReadTimeout     int      `mapstructure:"read_timeout"`
	WriteTimeout    int      `mapstructure:"write_timeout"`
	MaxRetries      int      `mapstructure:"max_retries"`
	MaxRetryBackoff int      `mapstructure:"max_retry_backoff"`
	MinRetryBackoff int      `mapstructure:"min_retry_backoff"`
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

type Snowflake struct {
	Epoch     int64 `mapstructure:"epoch"`
	Node      uint8 `mapstructure:"node"`
	Step      uint8 `mapstructure:"step"`
	TotalBits uint8 `mapstructure:"total_bits"`
}

type SnowflakeNode struct {
	Config   Snowflake
	WorkerID int64 `mapstructure:"worker_id"`
}
