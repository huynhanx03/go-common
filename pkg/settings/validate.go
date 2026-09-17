package settings

import (
	"errors"
	"fmt"
	"strings"
)

const (
	maxPort                      = 65_535
	maxConnectionPool            = 1_000_000
	maxTimeoutSeconds            = 86_400
	maxMessageBytes              = 1 << 30
	maxRequestBodyBytes          = 64 << 20
	maxHTTPRequestTimeoutSeconds = 10 * 60
)

var ErrInvalidConfig = errors.New("settings: invalid configuration")

// ValidationError identifies an invalid field without rendering its value.
// Configuration values frequently contain credentials and must never be
// copied into startup errors or logs.
type ValidationError struct {
	Field string
}

func (err *ValidationError) Error() string {
	if err == nil || err.Field == "" {
		return ErrInvalidConfig.Error()
	}
	return fmt.Sprintf("%s: %s", ErrInvalidConfig, err.Field)
}

func (err *ValidationError) Unwrap() error {
	return ErrInvalidConfig
}

func invalid(field string) error {
	return &ValidationError{Field: field}
}

func (environment Env) Validate() error {
	switch environment {
	case EnvDev, EnvStaging, EnvProd:
		return nil
	default:
		return invalid("server.mode")
	}
}

func (config Server) Validate() error {
	if err := config.Mode.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(config.Host) == "" {
		return invalid("server.host")
	}
	if !validPort(config.Port) {
		return invalid("server.port")
	}
	if config.GRPCPort != 0 && !validPort(config.GRPCPort) {
		return invalid("server.grpc_port")
	}
	if config.RateLimit.Limit <= 0 {
		return invalid("server.rate_limit.limit")
	}
	if config.RateLimit.Burst <= 0 {
		return invalid("server.rate_limit.burst")
	}
	if config.RateLimit.Window <= 0 ||
		config.RateLimit.Window > maxTimeoutSeconds {
		return invalid("server.rate_limit.window")
	}
	if config.CircuitBreaker.FailureThreshold <= 0 {
		return invalid("server.circuit_breaker.failure_threshold")
	}
	if config.CircuitBreaker.SuccessThreshold <= 0 {
		return invalid("server.circuit_breaker.success_threshold")
	}
	if config.CircuitBreaker.OpenTimeout <= 0 ||
		config.CircuitBreaker.OpenTimeout > maxTimeoutSeconds {
		return invalid("server.circuit_breaker.open_timeout")
	}
	if config.MaxRequestBodyBytes <= 0 || config.MaxRequestBodyBytes > maxRequestBodyBytes {
		return invalid("server.max_request_body_bytes")
	}
	if config.RequestTimeoutSeconds <= 0 ||
		config.RequestTimeoutSeconds > maxHTTPRequestTimeoutSeconds {
		return invalid("server.request_timeout_seconds")
	}
	return nil
}

func (config GRPCService) Validate() error {
	if strings.TrimSpace(config.Host) == "" {
		return invalid("grpc_service.host")
	}
	if !validPort(config.Port) {
		return invalid("grpc_service.port")
	}
	return nil
}

func (config Logger) Validate() error {
	switch strings.ToLower(strings.TrimSpace(config.LogLevel)) {
	case "debug", "info", "warn", "error":
	default:
		return invalid("logger.log_level")
	}
	if strings.TrimSpace(config.FileLogName) == "" {
		return invalid("logger.file_log_name")
	}
	if config.MaxBackups <= 0 || config.MaxAge <= 0 || config.MaxSize <= 0 {
		return invalid("logger.rotation")
	}
	return nil
}

func (config Database) Validate() error {
	switch strings.ToLower(strings.TrimSpace(config.Driver)) {
	case "postgres", "postgresql", "mysql":
	default:
		return invalid("database.driver")
	}
	if strings.TrimSpace(config.Host) == "" ||
		strings.TrimSpace(config.Username) == "" ||
		config.Password == "" ||
		strings.TrimSpace(config.Database) == "" {
		return invalid("database.connection")
	}
	if !validPort(config.Port) {
		return invalid("database.port")
	}
	if config.MaxOpenConns <= 0 ||
		config.MaxOpenConns > maxConnectionPool ||
		config.MaxIdleConns < 0 ||
		config.MaxIdleConns > config.MaxOpenConns {
		return invalid("database.pool")
	}
	if config.ConnMaxLifetime <= 0 ||
		config.ConnMaxLifetime > maxTimeoutSeconds ||
		config.ConnMaxIdleTime <= 0 ||
		config.ConnMaxIdleTime > config.ConnMaxLifetime {
		return invalid("database.timeout")
	}
	return nil
}

func (config JWT) Validate() error {
	hasSecret := len(config.Secret) >= 32 &&
		!strings.Contains(config.Secret, "${") &&
		!strings.Contains(config.Secret, "}")
	hasKeyPaths := strings.TrimSpace(config.PrivateKeyPath) != "" &&
		strings.TrimSpace(config.PublicKeyPath) != ""
	hasParsedKeys := config.PrivateKey != nil && config.PublicKey != nil
	if !hasSecret && !hasKeyPaths && !hasParsedKeys {
		return invalid("jwt.signing_material")
	}
	return nil
}

func (config Redis) Validate() error {
	if len(config.Addrs) == 0 {
		return invalid("redis.addrs")
	}
	for _, address := range config.Addrs {
		if strings.TrimSpace(address) == "" {
			return invalid("redis.addrs")
		}
	}
	if config.Database < 0 ||
		config.PoolSize <= 0 ||
		config.PoolSize > maxConnectionPool ||
		config.MinIdleConns < 0 ||
		config.MinIdleConns > config.PoolSize {
		return invalid("redis.pool")
	}
	if config.PoolTimeout <= 0 ||
		config.DialTimeout <= 0 ||
		config.ReadTimeout <= 0 ||
		config.WriteTimeout <= 0 ||
		config.PoolTimeout > maxTimeoutSeconds ||
		config.DialTimeout > maxTimeoutSeconds ||
		config.ReadTimeout > maxTimeoutSeconds ||
		config.WriteTimeout > maxTimeoutSeconds {
		return invalid("redis.timeout")
	}
	if config.MaxRetries < 0 ||
		config.MinRetryBackoff <= 0 ||
		config.MaxRetryBackoff < config.MinRetryBackoff {
		return invalid("redis.retry")
	}
	return nil
}

func (config Kafka) Validate() error {
	if len(config.Brokers) == 0 {
		return invalid("kafka.brokers")
	}
	for _, broker := range config.Brokers {
		if strings.TrimSpace(broker) == "" {
			return invalid("kafka.brokers")
		}
	}
	if config.FlushFrequency <= 0 ||
		config.FlushBytes <= 0 ||
		config.FlushBytes > maxMessageBytes ||
		config.MaxMessageBytes <= 0 ||
		config.MaxMessageBytes > maxMessageBytes ||
		config.Timeout <= 0 ||
		config.Timeout > maxTimeoutSeconds ||
		config.MaxRetries < 0 ||
		config.RetryBackoff <= 0 ||
		config.MaxProcessingTime <= 0 ||
		config.ConsumerBatchSize <= 0 ||
		config.ConsumerBatchSize > 1_000_000 ||
		config.ConsumerBatchInterval <= 0 {
		return invalid("kafka.bounds")
	}
	return nil
}

func validPort(port int) bool {
	return port > 0 && port <= maxPort
}
