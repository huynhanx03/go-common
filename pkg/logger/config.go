package logger

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/huynhanx03/go-common/pkg/settings"
	"go.uber.org/zap/zapcore"
)

func (config LoggerConfig) withDefaults() LoggerConfig {
	if config.Mode == "" {
		config.Mode = settings.EnvDev
	}
	if config.Level == "" {
		if config.Mode.IsDev() {
			config.Level = defaultDevelopmentLevel
		} else {
			config.Level = defaultProductionLevel
		}
	}
	if config.MaxSize == 0 {
		config.MaxSize = defaultMaxSize
	}
	if config.MaxBackups == 0 {
		config.MaxBackups = defaultMaxBackups
	}
	if config.MaxAge == 0 {
		config.MaxAge = defaultMaxAge
	}
	if config.SamplingInitial == 0 {
		config.SamplingInitial = defaultSamplingInitial
	}
	if config.SamplingThereafter == 0 {
		config.SamplingThereafter = defaultSamplingThereafter
	}
	return config
}

func validateConfig(config LoggerConfig) error {
	switch {
	case config.MaxSize < 0 || config.MaxSize > maxRotationSizeMegabytes:
		return invalidConfig("max size is outside the supported range")
	case config.MaxBackups < 0 || config.MaxBackups > maxRotationBackups:
		return invalidConfig("max backups is outside the supported range")
	case config.MaxAge < 0 || config.MaxAge > maxRotationAgeDays:
		return invalidConfig("max age is outside the supported range")
	case config.SamplingInitial < 0 || config.SamplingInitial > maxSamplingEntries:
		return invalidConfig("sampling initial is outside the supported range")
	case config.SamplingThereafter < 0 || config.SamplingThereafter > maxSamplingEntries:
		return invalidConfig("sampling thereafter is outside the supported range")
	case !validLoggerText(config.Level, 32, false):
		return invalidConfig("invalid level")
	case !validLoggerText(config.Service, maxLoggerMetadataBytes, true):
		return invalidConfig("invalid service metadata")
	case !validLoggerText(string(config.Env), maxLoggerMetadataBytes, true):
		return invalidConfig("invalid environment metadata")
	case !validLoggerText(config.Version, maxLoggerVersionBytes, true):
		return invalidConfig("invalid version metadata")
	case !validLoggerText(config.Filename, maxLoggerPathBytes, true):
		return invalidConfig("invalid filename")
	default:
		return nil
	}
}

func invalidConfig(reason string) error { return fmt.Errorf("%w: %s", ErrInvalidConfig, reason) }

func parseLevel(value string) (zapcore.Level, error) {
	var level zapcore.Level
	if err := level.UnmarshalText([]byte(value)); err != nil {
		return 0, err
	}
	return level, nil
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
