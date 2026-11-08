package settings

type GRPCService struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
}

// Server is the configuration for the server.
type Server struct {
	Mode                  Env                  `mapstructure:"mode"`
	Host                  string               `mapstructure:"host"`
	Port                  int                  `mapstructure:"port"`
	GRPCPort              int                  `mapstructure:"grpc_port"`
	TrustedProxies        []string             `mapstructure:"trusted_proxies"`
	RateLimit             RateLimitConfig      `mapstructure:"rate_limit"`
	CircuitBreaker        CircuitBreakerConfig `mapstructure:"circuit_breaker"`
	MaxRequestBodyBytes   int64                `mapstructure:"max_request_body_bytes"`
	RequestTimeoutSeconds int                  `mapstructure:"request_timeout_seconds"`
}

// RateLimitConfig is the configuration for HTTP rate limiting.
type RateLimitConfig struct {
	Limit  int `mapstructure:"limit"`
	Burst  int `mapstructure:"burst"`
	Window int `mapstructure:"window"` // in seconds
}

// CircuitBreakerConfig is the configuration for the HTTP circuit breaker.
type CircuitBreakerConfig struct {
	FailureThreshold int `mapstructure:"failure_threshold"`
	SuccessThreshold int `mapstructure:"success_threshold"`
	OpenTimeout      int `mapstructure:"open_timeout"` // in seconds
}
