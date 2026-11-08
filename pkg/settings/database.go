package settings

// Database is the configuration for the database.
type Database struct {
	Driver          string `mapstructure:"driver"`
	Host            string `mapstructure:"host"`
	Port            int    `mapstructure:"port"`
	Username        string `mapstructure:"username"`
	Password        string `mapstructure:"password"`
	Database        string `mapstructure:"database"`
	MaxOpenConns    int    `mapstructure:"max_open_conns"`
	MaxIdleConns    int    `mapstructure:"max_idle_conns"`
	ConnMaxLifetime int    `mapstructure:"conn_max_lifetime"`  // seconds; keep below the server/proxy idle timeout
	ConnMaxIdleTime int    `mapstructure:"conn_max_idle_time"` // seconds; shrinks the pool when traffic is quiet
}

// MongoDB is the configuration for MongoDB.
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

// Redis is the configuration for Redis.
type Redis struct {
	Namespace       string   `mapstructure:"namespace"`
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
	MaxRetries      int      `mapstructure:"max_retries"` // -1 disables per-node retries; 0 uses the package default.
	MaxRetryBackoff int      `mapstructure:"max_retry_backoff"`
	MinRetryBackoff int      `mapstructure:"min_retry_backoff"`
}
