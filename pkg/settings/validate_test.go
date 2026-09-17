package settings

import (
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"strings"
	"testing"
)

func validServer() Server {
	return Server{
		Mode: EnvProd,
		Host: "0.0.0.0",
		Port: 8080,
		RateLimit: RateLimitConfig{
			Limit:  100,
			Burst:  200,
			Window: 60,
		},
		CircuitBreaker: CircuitBreakerConfig{
			FailureThreshold: 5,
			SuccessThreshold: 2,
			OpenTimeout:      30,
		},
		MaxRequestBodyBytes:   4 << 20,
		RequestTimeoutSeconds: 30,
	}
}

func validLogger() Logger {
	return Logger{
		LogLevel:    "info",
		FileLogName: "service.log",
		MaxBackups:  10,
		MaxAge:      7,
		MaxSize:     100,
	}
}

func validDatabase() Database {
	return Database{
		Driver:          "postgres",
		Host:            "database",
		Port:            5432,
		Username:        "service",
		Password:        "not-logged",
		Database:        "service",
		MaxOpenConns:    20,
		MaxIdleConns:    10,
		ConnMaxLifetime: 1800,
		ConnMaxIdleTime: 300,
	}
}

func validJWT() JWT {
	return JWT{
		PrivateKeyPath: "/run/secrets/jwt-private.pem",
		PublicKeyPath:  "/run/secrets/jwt-public.pem",
	}
}

func TestEnvValidate(t *testing.T) {
	t.Parallel()

	for _, env := range []Env{EnvDev, EnvStaging, EnvProd} {
		if err := env.Validate(); err != nil {
			t.Errorf("expected %q to be valid, got: %v", env, err)
		}
	}

	for _, invalidEnv := range []Env{"", "test", "production", "local"} {
		if err := invalidEnv.Validate(); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("expected %q to be invalid, got: %v", invalidEnv, err)
		}
	}
}

func TestServerValidate(t *testing.T) {
	t.Parallel()

	if err := validServer().Validate(); err != nil {
		t.Fatalf("validServer().Validate(): %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Server)
	}{
		{name: "invalid mode", mutate: func(s *Server) { s.Mode = "invalid" }},
		{name: "empty host", mutate: func(s *Server) { s.Host = "   " }},
		{name: "zero port", mutate: func(s *Server) { s.Port = 0 }},
		{name: "port too large", mutate: func(s *Server) { s.Port = 70_000 }},
		{name: "invalid grpc port", mutate: func(s *Server) { s.GRPCPort = 70_000 }},
		{name: "rate limit zero", mutate: func(s *Server) { s.RateLimit.Limit = 0 }},
		{name: "rate burst zero", mutate: func(s *Server) { s.RateLimit.Burst = 0 }},
		{name: "rate window zero", mutate: func(s *Server) { s.RateLimit.Window = 0 }},
		{name: "rate window too large", mutate: func(s *Server) { s.RateLimit.Window = maxTimeoutSeconds + 1 }},
		{name: "cb failure threshold zero", mutate: func(s *Server) { s.CircuitBreaker.FailureThreshold = 0 }},
		{name: "cb success threshold zero", mutate: func(s *Server) { s.CircuitBreaker.SuccessThreshold = 0 }},
		{name: "cb open timeout zero", mutate: func(s *Server) { s.CircuitBreaker.OpenTimeout = 0 }},
		{name: "cb open timeout too large", mutate: func(s *Server) { s.CircuitBreaker.OpenTimeout = maxTimeoutSeconds + 1 }},
		{name: "max request body zero", mutate: func(s *Server) { s.MaxRequestBodyBytes = 0 }},
		{name: "max request body too large", mutate: func(s *Server) { s.MaxRequestBodyBytes = (64 << 20) + 1 }},
		{name: "request timeout zero", mutate: func(s *Server) { s.RequestTimeoutSeconds = 0 }},
		{name: "request timeout too large", mutate: func(s *Server) { s.RequestTimeoutSeconds = maxHTTPRequestTimeoutSeconds + 1 }},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := validServer()
			tt.mutate(&server)
			err := server.Validate()
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("expected ErrInvalidConfig, got: %v", err)
			}
		})
	}
}

func TestLoggerValidate(t *testing.T) {
	t.Parallel()

	if err := validLogger().Validate(); err != nil {
		t.Fatalf("validLogger().Validate(): %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Logger)
	}{
		{name: "invalid level", mutate: func(l *Logger) { l.LogLevel = "trace-all-secrets" }},
		{name: "empty file log name", mutate: func(l *Logger) { l.FileLogName = "  " }},
		{name: "max backups zero", mutate: func(l *Logger) { l.MaxBackups = 0 }},
		{name: "max age zero", mutate: func(l *Logger) { l.MaxAge = 0 }},
		{name: "max size zero", mutate: func(l *Logger) { l.MaxSize = 0 }},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			logger := validLogger()
			tt.mutate(&logger)
			err := logger.Validate()
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("expected ErrInvalidConfig, got: %v", err)
			}
		})
	}
}

func TestDatabaseValidate(t *testing.T) {
	t.Parallel()

	if err := validDatabase().Validate(); err != nil {
		t.Fatalf("validDatabase().Validate(): %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Database)
	}{
		{name: "unsupported driver", mutate: func(d *Database) { d.Driver = "sqlite" }},
		{name: "empty host", mutate: func(d *Database) { d.Host = "  " }},
		{name: "empty username", mutate: func(d *Database) { d.Username = "" }},
		{name: "empty password", mutate: func(d *Database) { d.Password = "" }},
		{name: "empty database", mutate: func(d *Database) { d.Database = "" }},
		{name: "invalid port", mutate: func(d *Database) { d.Port = 70_000 }},
		{name: "max open conns zero", mutate: func(d *Database) { d.MaxOpenConns = 0 }},
		{name: "max open conns too large", mutate: func(d *Database) { d.MaxOpenConns = maxConnectionPool + 1 }},
		{name: "negative idle conns", mutate: func(d *Database) { d.MaxIdleConns = -1 }},
		{name: "idle conns larger than open", mutate: func(d *Database) { d.MaxIdleConns = d.MaxOpenConns + 1 }},
		{name: "conn max lifetime zero", mutate: func(d *Database) { d.ConnMaxLifetime = 0 }},
		{name: "conn max lifetime too large", mutate: func(d *Database) { d.ConnMaxLifetime = maxTimeoutSeconds + 1 }},
		{name: "conn max idle time zero", mutate: func(d *Database) { d.ConnMaxIdleTime = 0 }},
		{name: "conn max idle time larger than lifetime", mutate: func(d *Database) { d.ConnMaxIdleTime = d.ConnMaxLifetime + 1 }},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db := validDatabase()
			tt.mutate(&db)
			err := db.Validate()
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("expected ErrInvalidConfig, got: %v", err)
			}
		})
	}
}

func TestJWTValidate(t *testing.T) {
	t.Parallel()

	// 1. Valid with key paths
	jwtPaths := validJWT()
	if err := jwtPaths.Validate(); err != nil {
		t.Fatalf("validJWT with paths failed: %v", err)
	}

	// 2. Valid with secret >= 32 chars
	jwtSecret := JWT{Secret: "a-very-long-secret-key-that-is-at-least-32-chars"}
	if err := jwtSecret.Validate(); err != nil {
		t.Fatalf("validJWT with secret failed: %v", err)
	}

	// 3. Valid with parsed keys
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	jwtParsedKeys := JWT{
		PrivateKey: privKey,
		PublicKey:  &privKey.PublicKey,
	}
	if err := jwtParsedKeys.Validate(); err != nil {
		t.Fatalf("validJWT with parsed keys failed: %v", err)
	}

	// 4. Invalid cases without leaking secrets
	tests := []struct {
		name   string
		jwt    JWT
		secret string
	}{
		{
			name:   "short secret",
			jwt:    JWT{Secret: "short"},
			secret: "short",
		},
		{
			name:   "template placeholder prefix",
			jwt:    JWT{Secret: "${JWT_SECRET_THAT_MUST_NOT_LEAK_1234567890}"},
			secret: "JWT_SECRET_THAT_MUST_NOT_LEAK",
		},
		{
			name:   "template placeholder suffix",
			jwt:    JWT{Secret: "some_secret_with_trailing_bracket_1234567890}"},
			secret: "some_secret_with_trailing_bracket",
		},
		{
			name: "empty jwt config",
			jwt:  JWT{},
		},
		{
			name: "only private key path without public key path",
			jwt:  JWT{PrivateKeyPath: "/run/secrets/key.pem"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.jwt.Validate()
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("expected ErrInvalidConfig, got: %v", err)
			}
			if tt.secret != "" && strings.Contains(err.Error(), tt.secret) {
				t.Fatalf("validation leaked secret: %v", err)
			}
		})
	}
}

func TestGRPCServiceValidate(t *testing.T) {
	t.Parallel()

	valid := GRPCService{Host: "127.0.0.1", Port: 9090}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid GRPCService failed: %v", err)
	}

	tests := []struct {
		name string
		svc  GRPCService
	}{
		{name: "empty host", svc: GRPCService{Host: "   ", Port: 9090}},
		{name: "zero port", svc: GRPCService{Host: "127.0.0.1", Port: 0}},
		{name: "port too large", svc: GRPCService{Host: "127.0.0.1", Port: 70_000}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.svc.Validate()
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("expected ErrInvalidConfig, got: %v", err)
			}
		})
	}
}

func TestRedisAndKafkaValidationRejectInvalidBounds(t *testing.T) {
	t.Parallel()

	redisConfig := Redis{
		Addrs:           []string{"redis:6379"},
		PoolSize:        10,
		MinIdleConns:    2,
		PoolTimeout:     5,
		DialTimeout:     5,
		ReadTimeout:     3,
		WriteTimeout:    3,
		MaxRetries:      3,
		MinRetryBackoff: 100,
		MaxRetryBackoff: 500,
	}
	if err := redisConfig.Validate(); err != nil {
		t.Fatalf("Redis.Validate: %v", err)
	}
	redisConfig.MinIdleConns = 11
	if !errors.Is(redisConfig.Validate(), ErrInvalidConfig) {
		t.Fatal("Redis.Validate accepted min idle larger than pool")
	}

	kafkaConfig := Kafka{
		Brokers:               []string{"kafka:9092"},
		FlushFrequency:        10,
		FlushBytes:            1024,
		MaxMessageBytes:       1024,
		Timeout:               5,
		MaxRetries:            3,
		RetryBackoff:          100,
		MaxProcessingTime:     1000,
		ConsumerBatchSize:     100,
		ConsumerBatchInterval: 100,
	}
	if err := kafkaConfig.Validate(); err != nil {
		t.Fatalf("Kafka.Validate: %v", err)
	}
	kafkaConfig.ConsumerBatchSize = 0
	if !errors.Is(kafkaConfig.Validate(), ErrInvalidConfig) {
		t.Fatal("Kafka.Validate accepted zero batch size")
	}
}
