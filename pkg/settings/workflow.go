package settings

// Temporal configures a connection to a Temporal Service.
type Temporal struct {
	Address            string `mapstructure:"address"`
	Namespace          string `mapstructure:"namespace"`
	APIKey             string `mapstructure:"api_key"`
	ServerName         string `mapstructure:"server_name"`
	CAFile             string `mapstructure:"ca_file"`
	CertFile           string `mapstructure:"cert_file"`
	KeyFile            string `mapstructure:"key_file"`
	TLSEnabled         bool   `mapstructure:"tls_enabled"`
	DialTimeoutSeconds int    `mapstructure:"dial_timeout_seconds"` // Zero inherits the caller's context.
}

// TemporalWorker configures one worker's identity and graceful shutdown budget.
type TemporalWorker struct {
	Name                   string `mapstructure:"name"`
	TaskQueue              string `mapstructure:"task_queue"`
	ShutdownTimeoutSeconds int    `mapstructure:"shutdown_timeout_seconds"` // Zero uses the package default.
}
