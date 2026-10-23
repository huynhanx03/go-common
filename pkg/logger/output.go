package logger

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/natefinch/lumberjack"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func serviceFields(config LoggerConfig) []zap.Field {
	fields := make([]zap.Field, 0, 3)
	if config.Service != "" {
		fields = append(fields, zap.String(fieldService, config.Service))
	}
	if config.Env != "" {
		fields = append(fields, zap.String(fieldEnvironment, string(config.Env)))
	}
	if config.Version != "" {
		fields = append(fields, zap.String(fieldVersion, config.Version))
	}
	return fields
}

func fileEncoderConfig() zapcore.EncoderConfig {
	config := zap.NewProductionEncoderConfig()
	config.TimeKey, config.MessageKey, config.NameKey = fieldTimestamp, fieldMessage, fieldComponent
	config.EncodeTime = zapcore.ISO8601TimeEncoder
	config.EncodeLevel = zapcore.CapitalLevelEncoder
	return config
}

func consoleEncoderConfig() zapcore.EncoderConfig {
	config := zap.NewDevelopmentEncoderConfig()
	config.TimeKey, config.MessageKey, config.NameKey = fieldTimestamp, fieldMessage, fieldComponent
	config.EncodeTime = zapcore.ISO8601TimeEncoder
	config.EncodeLevel = zapcore.CapitalColorLevelEncoder
	return config
}

func newRotator(config LoggerConfig) *lumberjack.Logger {
	return &lumberjack.Logger{
		Filename: config.Filename, MaxSize: config.MaxSize,
		MaxBackups: config.MaxBackups, MaxAge: config.MaxAge, Compress: config.Compress,
	}
}

func prepareLogFile(filename string) error {
	if err := os.MkdirAll(filepath.Dir(filename), 0o750); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close log file: %w", err)
	}
	return nil
}
