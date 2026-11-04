package logger

import (
	"errors"
	"strings"
	"testing"

	"github.com/huynhanx03/go-common/pkg/settings"
)

func TestValidateConfigRejectsOutOfRangeValues(t *testing.T) {
	tests := []struct {
		name   string
		change func(*LoggerConfig)
	}{
		{"negative rotation size", func(c *LoggerConfig) { c.MaxSize = -1 }},
		{"rotation size limit", func(c *LoggerConfig) { c.MaxSize = maxRotationSizeMegabytes + 1 }},
		{"negative backup count", func(c *LoggerConfig) { c.MaxBackups = -1 }},
		{"backup count limit", func(c *LoggerConfig) { c.MaxBackups = maxRotationBackups + 1 }},
		{"negative retention age", func(c *LoggerConfig) { c.MaxAge = -1 }},
		{"retention age limit", func(c *LoggerConfig) { c.MaxAge = maxRotationAgeDays + 1 }},
		{"negative initial sampling", func(c *LoggerConfig) { c.SamplingInitial = -1 }},
		{"initial sampling limit", func(c *LoggerConfig) { c.SamplingInitial = maxSamplingEntries + 1 }},
		{"negative subsequent sampling", func(c *LoggerConfig) { c.SamplingThereafter = -1 }},
		{"subsequent sampling limit", func(c *LoggerConfig) { c.SamplingThereafter = maxSamplingEntries + 1 }},
		{"missing required level", func(c *LoggerConfig) { c.Level = "" }},
		{"service too long", func(c *LoggerConfig) { c.Service = strings.Repeat("s", maxLoggerMetadataBytes+1) }},
		{"environment control character", func(c *LoggerConfig) { c.Env = settings.Env("prod\n") }},
		{"version too long", func(c *LoggerConfig) { c.Version = strings.Repeat("v", maxLoggerVersionBytes+1) }},
		{"filename too long", func(c *LoggerConfig) { c.Filename = strings.Repeat("f", maxLoggerPathBytes+1) }},
		{"metadata whitespace", func(c *LoggerConfig) { c.Service = " api" }},
		{"metadata invalid UTF-8", func(c *LoggerConfig) { c.Service = string([]byte{0xff}) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := LoggerConfig{Level: "info"}
			test.change(&config)
			if err := validateConfig(config); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("validateConfig() error = %v, want ErrInvalidConfig", err)
			}
		})
	}

	validUpperBounds := LoggerConfig{
		Level: "info", MaxSize: maxRotationSizeMegabytes, MaxBackups: maxRotationBackups,
		MaxAge: maxRotationAgeDays, SamplingInitial: maxSamplingEntries,
		SamplingThereafter: maxSamplingEntries,
	}
	if err := validateConfig(validUpperBounds); err != nil {
		t.Fatalf("validateConfig() rejected inclusive limits: %v", err)
	}
}

func TestValidLoggerTextBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		maximum  int
		optional bool
		want     bool
	}{
		{"optional empty", "", 3, true, true},
		{"required empty", "", 3, false, false},
		{"at byte limit", "abc", 3, false, true},
		{"over byte limit", "abcd", 3, false, false},
		{"leading whitespace", " abc", 8, false, false},
		{"control character", "a\tb", 8, false, false},
		{"invalid UTF-8", string([]byte{0xff}), 8, false, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := validLoggerText(test.value, test.maximum, test.optional); got != test.want {
				t.Errorf("validLoggerText() = %t, want %t", got, test.want)
			}
		})
	}
}
