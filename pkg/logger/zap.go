package logger

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/natefinch/lumberjack"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Modes select the output/encoding profile of the logger.
const (
	// ModeDev writes colored, human-readable logs to stdout. Default level: debug.
	ModeDev = "dev"

	// ModeProd writes JSON logs to stdout (for collectors like Loki/ELK) with
	// sampling enabled. Default level: info.
	ModeProd = "prod"
)

// LoggerZap wraps zap.Logger for structured logging with a runtime-adjustable level.
type LoggerZap struct {
	*zap.Logger
	level zap.AtomicLevel
}

// LoggerConfig holds configuration for logger initialization.
type LoggerConfig struct {
	// Mode is ModeDev or ModeProd. Defaults to ModeDev.
	Mode string

	// Level is the minimum level to log (debug|info|warn|error|dpanic|panic|fatal).
	// Entries below it are dropped. Defaults to debug in dev, info in prod.
	Level string

	// Service metadata stamped on every entry; empty fields are skipped.
	// Set these when multiple services ship logs to the same aggregator.
	Service string
	Env     string
	Version string

	// File output with rotation, enabled when Filename is set (in any mode).
	Filename   string
	MaxSize    int // megabytes
	MaxBackups int
	MaxAge     int // days
	Compress   bool

	// Sampling caps repeated identical messages per second so a hot error
	// loop cannot saturate I/O. Active in prod mode only. Per second, the
	// first SamplingInitial entries of an identical message are logged, then
	// one of every SamplingThereafter.
	SamplingInitial    int  // default 100
	SamplingThereafter int  // default 100
	DisableSampling    bool // turn sampling off entirely
}

// withDefaults fills zero-valued fields with sensible defaults.
func (c LoggerConfig) withDefaults() LoggerConfig {
	if c.Mode == "" {
		c.Mode = ModeDev
	}
	if c.Level == "" {
		if c.Mode == ModeProd {
			c.Level = "info"
		} else {
			c.Level = "debug"
		}
	}
	if c.MaxSize == 0 {
		c.MaxSize = 100
	}
	if c.MaxBackups == 0 {
		c.MaxBackups = 5
	}
	if c.MaxAge == 0 {
		c.MaxAge = 30
	}
	if c.SamplingInitial == 0 {
		c.SamplingInitial = 100
	}
	if c.SamplingThereafter == 0 {
		c.SamplingThereafter = 100
	}
	return c
}

// NewLogger creates a logger according to cfg:
//   - dev mode: colored human-readable output on stdout
//   - prod mode: JSON output on stdout, sampled
//   - any mode: additional JSON file output with rotation when Filename is set
//
// The level can be changed at runtime via SetLevel / LevelHandler.
func NewLogger(cfg LoggerConfig) *LoggerZap {
	cfg = cfg.withDefaults()
	level := zap.NewAtomicLevelAt(parseLevel(cfg.Level))

	var cores []zapcore.Core
	if cfg.Mode == ModeProd {
		cores = append(cores, zapcore.NewCore(
			zapcore.NewJSONEncoder(fileEncoderConfig()),
			zapcore.AddSync(os.Stdout),
			level,
		))
	} else {
		cores = append(cores, zapcore.NewCore(
			zapcore.NewConsoleEncoder(consoleEncoderConfig()),
			zapcore.AddSync(os.Stdout),
			level,
		))
	}

	if cfg.Filename != "" {
		if err := os.MkdirAll(filepath.Dir(cfg.Filename), 0o755); err != nil {
			panic("logger: failed to create log directory: " + err.Error())
		}
		cores = append(cores, zapcore.NewCore(
			zapcore.NewJSONEncoder(fileEncoderConfig()),
			zapcore.AddSync(newRotator(cfg)),
			level,
		))
	}

	core := zapcore.NewTee(cores...)
	if cfg.Mode == ModeProd && !cfg.DisableSampling {
		core = zapcore.NewSamplerWithOptions(core, time.Second, cfg.SamplingInitial, cfg.SamplingThereafter)
	}

	opts := []zap.Option{zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel)}
	if fields := serviceFields(cfg); len(fields) > 0 {
		opts = append(opts, zap.Fields(fields...))
	}

	return &LoggerZap{
		Logger: zap.New(core, opts...),
		level:  level,
	}
}

// Sync flushes any buffered log entries. Call it on shutdown (e.g. with
// defer) so the final entries are not lost. Errors from syncing stdout are
// ignored — terminals and pipes routinely reject fsync.
func (l *LoggerZap) Sync() error {
	err := l.Logger.Sync()
	if err != nil && isStdoutSyncErr(err) {
		return nil
	}
	return err
}

// isStdoutSyncErr reports whether err is the expected failure from fsyncing
// a character device or pipe such as stdout.
func isStdoutSyncErr(err error) bool {
	return errors.Is(err, syscall.ENOTTY) ||
		errors.Is(err, syscall.EINVAL) ||
		errors.Is(err, syscall.EBADF) ||
		errors.Is(err, syscall.ENOTSUP)
}

// SetLevel changes the minimum log level at runtime.
func (l *LoggerZap) SetLevel(level string) error {
	var zl zapcore.Level
	if err := zl.UnmarshalText([]byte(level)); err != nil {
		return err
	}
	l.level.SetLevel(zl)
	return nil
}

// Level returns the current minimum log level.
func (l *LoggerZap) Level() zapcore.Level {
	return l.level.Level()
}

// LevelHandler returns an http.Handler for reading and changing the level:
//
//	GET  -> {"level":"info"}
//	PUT  {"level":"debug"} -> switches the logger to debug
//
// Mount it on an internal/admin route only.
func (l *LoggerZap) LevelHandler() http.Handler {
	return l.level
}

// serviceFields converts non-empty service metadata into zap fields.
func serviceFields(cfg LoggerConfig) []zap.Field {
	var fields []zap.Field
	if cfg.Service != "" {
		fields = append(fields, zap.String("service", cfg.Service))
	}
	if cfg.Env != "" {
		fields = append(fields, zap.String("env", cfg.Env))
	}
	if cfg.Version != "" {
		fields = append(fields, zap.String("version", cfg.Version))
	}
	return fields
}

// fileEncoderConfig returns encoder config optimized for machine parsing.
func fileEncoderConfig() zapcore.EncoderConfig {
	cfg := zap.NewProductionEncoderConfig()
	cfg.TimeKey = "timestamp"
	cfg.EncodeTime = zapcore.ISO8601TimeEncoder
	cfg.EncodeLevel = zapcore.CapitalLevelEncoder
	return cfg
}

// consoleEncoderConfig returns encoder config optimized for dev readability.
func consoleEncoderConfig() zapcore.EncoderConfig {
	cfg := zap.NewDevelopmentEncoderConfig()
	cfg.TimeKey = "timestamp"
	cfg.EncodeTime = zapcore.ISO8601TimeEncoder
	cfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
	return cfg
}

// newRotator creates a lumberjack rotator for log file management.
func newRotator(cfg LoggerConfig) *lumberjack.Logger {
	return &lumberjack.Logger{
		Filename:   cfg.Filename,
		MaxSize:    cfg.MaxSize,
		MaxBackups: cfg.MaxBackups,
		MaxAge:     cfg.MaxAge,
		Compress:   cfg.Compress,
	}
}

// parseLevel converts a string log level to zapcore.Level.
// Falls back to InfoLevel on unrecognized input.
func parseLevel(level string) zapcore.Level {
	var l zapcore.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		return zapcore.InfoLevel
	}
	return l
}
