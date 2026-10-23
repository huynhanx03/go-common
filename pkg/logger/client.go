package logger

import (
	"fmt"
	"os"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/huynhanx03/go-common/pkg/settings"
)

// Logger writes structured entries enriched with request and trace identity.
type Logger struct {
	base  *zap.Logger
	level zap.AtomicLevel
}

// LoggerConfig controls output, service metadata, rotation, sampling, and level.
type LoggerConfig struct {
	// Mode selects readable console output in dev and JSON output otherwise.
	Mode settings.Env
	// Level is the minimum level; it defaults to debug in dev and info otherwise.
	Level string
	// Service metadata is attached to every entry when non-empty.
	Service string
	Env     settings.Env
	Version string

	// Filename enables an additional rotating JSON file output.
	Filename   string
	MaxSize    int // megabytes
	MaxBackups int
	MaxAge     int // days
	Compress   bool

	// Sampling limits repeated messages outside dev mode.
	SamplingInitial    int
	SamplingThereafter int
	DisableSampling    bool
}

// NewLogger validates configuration and creates a logger with stdout output
// and optional rotating JSON file output.
func NewLogger(config LoggerConfig) (*Logger, error) {
	config = config.withDefaults()
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	level, err := parseLevel(config.Level)
	if err != nil {
		return nil, fmt.Errorf("%w: level: %v", ErrInvalidConfig, err)
	}
	if config.Filename != "" {
		if err := prepareLogFile(config.Filename); err != nil {
			return nil, err
		}
	}

	atomicLevel := zap.NewAtomicLevelAt(level)
	cores := make([]zapcore.Core, 0, 2)
	if config.Mode.IsDev() {
		cores = append(cores, zapcore.NewCore(
			zapcore.NewConsoleEncoder(consoleEncoderConfig()), zapcore.AddSync(os.Stdout), atomicLevel,
		))
	} else {
		cores = append(cores, zapcore.NewCore(
			zapcore.NewJSONEncoder(fileEncoderConfig()), zapcore.AddSync(os.Stdout), atomicLevel,
		))
	}
	if config.Filename != "" {
		cores = append(cores, zapcore.NewCore(
			zapcore.NewJSONEncoder(fileEncoderConfig()), zapcore.AddSync(newRotator(config)), atomicLevel,
		))
	}

	var core zapcore.Core = zapcore.NewTee(cores...)
	if !config.Mode.IsDev() && !config.DisableSampling {
		core = zapcore.NewSamplerWithOptions(core, time.Second, config.SamplingInitial, config.SamplingThereafter)
	}
	options := []zap.Option{zap.AddCaller(), zap.AddCallerSkip(1), zap.AddStacktrace(zapcore.ErrorLevel)}
	if fields := serviceFields(config); len(fields) > 0 {
		options = append(options, zap.Fields(fields...))
	}
	return &Logger{base: zap.New(core, options...), level: atomicLevel}, nil
}

// MustNewLogger creates a logger or panics when configuration is invalid.
// Deprecated: call NewLogger and handle its error.
func MustNewLogger(config LoggerConfig) *Logger {
	log, err := NewLogger(config)
	if err != nil {
		panic(err)
	}
	return log
}

// Wrap adapts an injected Zap logger to the context-aware logger API.
func Wrap(base *zap.Logger) *Logger {
	return &Logger{base: base.WithOptions(zap.AddCallerSkip(1))}
}
