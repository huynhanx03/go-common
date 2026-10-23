package logger

import (
	"errors"
	"strings"
	"testing"

	"github.com/huynhanx03/go-common/pkg/settings"
	"go.uber.org/zap/zapcore"
)

func TestLoggerConfigDefaultsAndLevelParsing(t *testing.T) {
	dev := (LoggerConfig{}).withDefaults()
	if dev.Mode != settings.EnvDev || dev.Level != defaultDevelopmentLevel {
		t.Fatalf("development defaults = %+v", dev)
	}
	if dev.MaxSize != defaultMaxSize || dev.MaxBackups != defaultMaxBackups || dev.MaxAge != defaultMaxAge ||
		dev.SamplingInitial != defaultSamplingInitial || dev.SamplingThereafter != defaultSamplingThereafter {
		t.Fatalf("numeric defaults = %+v", dev)
	}
	prod := (LoggerConfig{Mode: settings.EnvProd}).withDefaults()
	if prod.Level != defaultProductionLevel {
		t.Fatalf("production level = %q", prod.Level)
	}
	custom := (LoggerConfig{Mode: settings.EnvProd, Level: "warn", MaxSize: 3, MaxBackups: 4, MaxAge: 5,
		SamplingInitial: 6, SamplingThereafter: 7}).withDefaults()
	if custom.Level != "warn" || custom.MaxSize != 3 || custom.MaxBackups != 4 || custom.MaxAge != 5 ||
		custom.SamplingInitial != 6 || custom.SamplingThereafter != 7 {
		t.Fatalf("explicit values changed by defaults: %+v", custom)
	}
	if got, err := parseLevel("error"); err != nil || got != zapcore.ErrorLevel {
		t.Fatalf("parseLevel(error) = %v, %v", got, err)
	}
	if _, err := parseLevel("not-a-level"); err == nil {
		t.Fatal("parseLevel accepted unknown level")
	}
}

func TestValidateConfigRejectsInvalidFields(t *testing.T) {
	cases := []struct {
		name string
		edit func(*LoggerConfig)
	}{
		{"negative max size", func(c *LoggerConfig) { c.MaxSize = -1 }},
		{"max size limit", func(c *LoggerConfig) { c.MaxSize = maxRotationSizeMegabytes + 1 }},
		{"negative backups", func(c *LoggerConfig) { c.MaxBackups = -1 }},
		{"backups limit", func(c *LoggerConfig) { c.MaxBackups = maxRotationBackups + 1 }},
		{"negative age", func(c *LoggerConfig) { c.MaxAge = -1 }},
		{"age limit", func(c *LoggerConfig) { c.MaxAge = maxRotationAgeDays + 1 }},
		{"negative initial sampling", func(c *LoggerConfig) { c.SamplingInitial = -1 }},
		{"initial sampling limit", func(c *LoggerConfig) { c.SamplingInitial = maxSamplingEntries + 1 }},
		{"negative subsequent sampling", func(c *LoggerConfig) { c.SamplingThereafter = -1 }},
		{"subsequent sampling limit", func(c *LoggerConfig) { c.SamplingThereafter = maxSamplingEntries + 1 }},
		{"required level", func(c *LoggerConfig) { c.Level = "" }},
		{"service length", func(c *LoggerConfig) { c.Service = strings.Repeat("s", maxLoggerMetadataBytes+1) }},
		{"environment control", func(c *LoggerConfig) { c.Env = "prod\n" }},
		{"version length", func(c *LoggerConfig) { c.Version = strings.Repeat("v", maxLoggerVersionBytes+1) }},
		{"filename length", func(c *LoggerConfig) { c.Filename = strings.Repeat("f", maxLoggerPathBytes+1) }},
		{"trim whitespace", func(c *LoggerConfig) { c.Service = " api" }},
		{"invalid utf8", func(c *LoggerConfig) { c.Service = string([]byte{0xff}) }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			config := LoggerConfig{Level: "info"}
			test.edit(&config)
			if err := validateConfig(config); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("validateConfig() error = %v, want ErrInvalidConfig", err)
			}
		})
	}
	if err := validateConfig((LoggerConfig{Mode: settings.EnvDev}).withDefaults()); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestValidLoggerTextBoundaries(t *testing.T) {
	cases := []struct {
		name     string
		value    string
		max      int
		optional bool
		want     bool
	}{
		{"optional empty", "", 3, true, true},
		{"required empty", "", 3, false, false},
		{"within limit", "abc", 3, false, true},
		{"over limit", "abcd", 3, false, false},
		{"leading whitespace", " abc", 8, false, false},
		{"control character", "a\tb", 8, false, false},
		{"invalid utf8", string([]byte{0xff}), 8, false, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := validLoggerText(test.value, test.max, test.optional); got != test.want {
				t.Errorf("validLoggerText() = %t, want %t", got, test.want)
			}
		})
	}
}
