package logger

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/natefinch/lumberjack"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/huynhanx03/go-common/pkg/settings"
)

const (
	maxLoggerPathBytes       = 4096
	maxLoggerMetadataBytes   = 256
	maxLoggerVersionBytes    = 128
	maxRotationSizeMegabytes = 1 << 20
	maxRotationBackups       = 10_000
	maxRotationAgeDays       = 100 * 365
	maxSamplingEntries       = 10_000_000
)

var errInvalidLogger = errors.New("logger: invalid logger")

// LoggerZap wraps zap.Logger for structured logging with a runtime-adjustable level.
type LoggerZap struct {
	*zap.Logger
	level zap.AtomicLevel
}

// LoggerConfig holds configuration for logger initialization.
type LoggerConfig struct {
	// Mode selects the output profile — pass cfg.Server.Mode straight in.
	// Dev (or empty): colored human-readable stdout, default level debug.
	// Anything else (staging, prod): JSON stdout with sampling, default
	// level info — so Debug entries are dropped outside dev.
	Mode settings.Env

	// Level is the minimum level to log (debug|info|warn|error|dpanic|panic|fatal).
	// Entries below it are dropped. Defaults to debug in dev, info otherwise.
	Level string

	// Service metadata stamped on every entry; empty fields are skipped.
	// Set these when multiple services ship logs to the same aggregator.
	Service string
	Env     settings.Env
	Version string

	// File output with rotation, enabled when Filename is set (in any mode).
	Filename   string
	MaxSize    int // megabytes
	MaxBackups int
	MaxAge     int // days
	Compress   bool

	// Sampling caps repeated identical messages per second so a hot error
	// loop cannot saturate I/O. Active outside dev mode. Per second, the
	// first SamplingInitial entries of an identical message are logged, then
	// one of every SamplingThereafter.
	SamplingInitial    int  // default 100
	SamplingThereafter int  // default 100
	DisableSampling    bool // turn sampling off entirely
}

// withDefaults fills zero-valued fields with sensible defaults.
func (c LoggerConfig) withDefaults() LoggerConfig {
	if c.Mode == "" {
		c.Mode = settings.EnvDev
	}
	if c.Level == "" {
		if c.Mode.IsDev() {
			c.Level = "debug"
		} else {
			c.Level = "info"
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
//   - dev (or empty) mode: colored human-readable output on stdout
//   - any other mode (staging, prod): JSON output on stdout, sampled
//   - any mode: additional JSON file output with rotation when Filename is set
//
// The level can be changed at runtime via SetLevel / LevelHandler.
func NewLogger(cfg LoggerConfig) (*LoggerZap, error) {
	cfg = cfg.withDefaults()
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	parsedLevel, err := parseLevel(cfg.Level)
	if err != nil {
		return nil, fmt.Errorf("logger: level: %w", err)
	}
	if cfg.Filename != "" {
		if err := prepareLogFile(cfg.Filename); err != nil {
			return nil, err
		}
	}
	level := zap.NewAtomicLevelAt(parsedLevel)

	var cores []zapcore.Core
	if cfg.Mode.IsDev() {
		cores = append(cores, zapcore.NewCore(
			zapcore.NewConsoleEncoder(consoleEncoderConfig()),
			zapcore.AddSync(os.Stdout),
			level,
		))
	} else {
		cores = append(cores, zapcore.NewCore(
			zapcore.NewJSONEncoder(fileEncoderConfig()),
			zapcore.AddSync(os.Stdout),
			level,
		))
	}

	if cfg.Filename != "" {
		cores = append(cores, zapcore.NewCore(
			zapcore.NewJSONEncoder(fileEncoderConfig()),
			zapcore.AddSync(newRotator(cfg)),
			level,
		))
	}

	core := zapcore.NewTee(cores...)
	if !cfg.Mode.IsDev() && !cfg.DisableSampling {
		core = zapcore.NewSamplerWithOptions(core, time.Second, cfg.SamplingInitial, cfg.SamplingThereafter)
	}

	opts := []zap.Option{zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel)}
	if fields := serviceFields(cfg); len(fields) > 0 {
		opts = append(opts, zap.Fields(fields...))
	}

	return &LoggerZap{
		Logger: zap.New(core, opts...),
		level:  level,
	}, nil
}

// MustNewLogger creates a logger or panics. Executable bootstrap code that
// cannot return an error may use this compatibility helper.
//
// Deprecated: call NewLogger and handle the returned error.
func MustNewLogger(cfg LoggerConfig) *LoggerZap {
	logger, err := NewLogger(cfg)
	if err != nil {
		panic(err)
	}
	return logger
}

// Sync flushes any buffered log entries. Call it on shutdown (e.g. with
// defer) so the final entries are not lost. Errors from syncing stdout are
// ignored — terminals and pipes routinely reject fsync.
func (l *LoggerZap) Sync() error {
	if l == nil || l.Logger == nil {
		return errInvalidLogger
	}
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
func (l *LoggerZap) SetLevel(level string) (resultErr error) {
	if l == nil || l.Logger == nil {
		return errInvalidLogger
	}
	defer func() {
		if recover() != nil {
			resultErr = errInvalidLogger
		}
	}()
	var zl zapcore.Level
	if err := zl.UnmarshalText([]byte(level)); err != nil {
		return err
	}
	l.level.SetLevel(zl)
	return nil
}

// Level returns the current minimum log level.
func (l *LoggerZap) Level() zapcore.Level {
	level, valid := l.currentLevel()
	if !valid {
		return zapcore.InfoLevel
	}
	return level
}

// CurrentLevel returns the canonical text representation used by generic
// time-bounded debug controls without exposing Zap types at that boundary.
func (l *LoggerZap) CurrentLevel() string {
	level, valid := l.currentLevel()
	if !valid {
		return ""
	}
	return level.String()
}

// LevelHandler returns an http.Handler for reading and changing the level:
//
//	GET  -> {"level":"info"}
//	PUT  {"level":"debug"} -> switches the logger to debug
//
// Mount it on an internal/admin route only.
//
// Deprecated: expose SetLevel only through an application-owned authenticated,
// authorized, audited, and time-bounded control surface.
func (l *LoggerZap) LevelHandler() http.Handler {
	if _, valid := l.currentLevel(); !valid {
		return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			http.Error(writer, "logger unavailable", http.StatusServiceUnavailable)
		})
	}
	return l.level
}

func (l *LoggerZap) currentLevel() (level zapcore.Level, valid bool) {
	if l == nil || l.Logger == nil {
		return zapcore.InfoLevel, false
	}
	defer func() {
		if recover() != nil {
			level = zapcore.InfoLevel
			valid = false
		}
	}()
	return l.level.Level(), true
}

// serviceFields converts non-empty service metadata into zap fields.
func serviceFields(cfg LoggerConfig) []zap.Field {
	var fields []zap.Field
	if cfg.Service != "" {
		fields = append(fields, zap.String("service", cfg.Service))
	}
	if cfg.Env != "" {
		fields = append(fields, zap.String("environment", string(cfg.Env)))
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
	cfg.MessageKey = "message"
	cfg.NameKey = "component"
	cfg.EncodeTime = zapcore.ISO8601TimeEncoder
	cfg.EncodeLevel = zapcore.CapitalLevelEncoder
	return cfg
}

// consoleEncoderConfig returns encoder config optimized for dev readability.
func consoleEncoderConfig() zapcore.EncoderConfig {
	cfg := zap.NewDevelopmentEncoderConfig()
	cfg.TimeKey = "timestamp"
	cfg.MessageKey = "message"
	cfg.NameKey = "component"
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

func parseLevel(level string) (zapcore.Level, error) {
	var l zapcore.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		return 0, err
	}
	return l, nil
}

func validateConfig(cfg LoggerConfig) error {
	switch {
	case cfg.MaxSize < 0 || cfg.MaxSize > maxRotationSizeMegabytes:
		return errors.New("logger: max size is outside the supported range")
	case cfg.MaxBackups < 0 || cfg.MaxBackups > maxRotationBackups:
		return errors.New("logger: max backups is outside the supported range")
	case cfg.MaxAge < 0 || cfg.MaxAge > maxRotationAgeDays:
		return errors.New("logger: max age is outside the supported range")
	case cfg.SamplingInitial < 0 || cfg.SamplingInitial > maxSamplingEntries:
		return errors.New("logger: sampling initial is outside the supported range")
	case cfg.SamplingThereafter < 0 || cfg.SamplingThereafter > maxSamplingEntries:
		return errors.New("logger: sampling thereafter is outside the supported range")
	case !validLoggerText(cfg.Level, 32, false):
		return errors.New("logger: invalid level")
	case !validLoggerText(cfg.Service, maxLoggerMetadataBytes, true):
		return errors.New("logger: invalid service metadata")
	case !validLoggerText(string(cfg.Env), maxLoggerMetadataBytes, true):
		return errors.New("logger: invalid environment metadata")
	case !validLoggerText(cfg.Version, maxLoggerVersionBytes, true):
		return errors.New("logger: invalid version metadata")
	case !validLoggerText(cfg.Filename, maxLoggerPathBytes, true):
		return errors.New("logger: invalid filename")
	default:
		return nil
	}
}

func validLoggerText(value string, maximum int, optional bool) bool {
	if value == "" {
		return optional
	}
	if len(value) > maximum || strings.TrimSpace(value) != value || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func prepareLogFile(filename string) error {
	directory := filepath.Dir(filename)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return fmt.Errorf("logger: create log directory: %w", err)
	}
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("logger: open log file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("logger: close log file: %w", err)
	}
	return nil
}
