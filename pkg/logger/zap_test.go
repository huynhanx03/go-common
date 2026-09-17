package logger

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap/zapcore"

	"github.com/huynhanx03/go-common/pkg/settings"
)

func TestWithDefaultsDev(t *testing.T) {
	// Empty mode counts as dev — a zero-config local run gets debug logs.
	cfg := LoggerConfig{}.withDefaults()

	if !cfg.Mode.IsDev() {
		t.Errorf("Mode = %q, want dev", cfg.Mode)
	}
	if cfg.Level != "debug" {
		t.Errorf("Level = %q, want debug", cfg.Level)
	}
}

func TestStagingBehavesLikeProd(t *testing.T) {
	// Anything that is not dev fails toward the safe profile: info level.
	for _, mode := range []settings.Env{settings.EnvStaging, "typo-env"} {
		l, err := NewLogger(LoggerConfig{Mode: mode})
		if err != nil {
			t.Fatalf("NewLogger(%q): %v", mode, err)
		}
		if l.Core().Enabled(zapcore.DebugLevel) {
			t.Errorf("mode %q must not enable debug level", mode)
		}
	}
}

func TestWithDefaultsProd(t *testing.T) {
	cfg := LoggerConfig{Mode: settings.EnvProd}.withDefaults()

	if cfg.Level != "info" {
		t.Errorf("Level = %q, want info", cfg.Level)
	}
	if cfg.SamplingInitial != 100 || cfg.SamplingThereafter != 100 {
		t.Errorf("sampling defaults = %d/%d, want 100/100", cfg.SamplingInitial, cfg.SamplingThereafter)
	}
}

func TestDevLogsDebugProdDoesNot(t *testing.T) {
	dev, err := NewLogger(LoggerConfig{Mode: settings.EnvDev})
	if err != nil {
		t.Fatalf("NewLogger(dev): %v", err)
	}
	if !dev.Core().Enabled(zapcore.DebugLevel) {
		t.Error("dev logger should enable debug level")
	}

	prod, err := NewLogger(LoggerConfig{Mode: settings.EnvProd})
	if err != nil {
		t.Fatalf("NewLogger(prod): %v", err)
	}
	if prod.Core().Enabled(zapcore.DebugLevel) {
		t.Error("prod logger should not enable debug level")
	}
	if !prod.Core().Enabled(zapcore.InfoLevel) {
		t.Error("prod logger should enable info level")
	}
}

func TestSetLevelAtRuntime(t *testing.T) {
	l, err := NewLogger(LoggerConfig{Mode: settings.EnvDev})
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}

	if err := l.SetLevel("error"); err != nil {
		t.Fatalf("SetLevel: %v", err)
	}
	if l.Core().Enabled(zapcore.InfoLevel) {
		t.Error("info should be disabled after SetLevel(error)")
	}
	if !l.Core().Enabled(zapcore.ErrorLevel) {
		t.Error("error should stay enabled after SetLevel(error)")
	}
	if l.Level() != zapcore.ErrorLevel {
		t.Errorf("Level() = %v, want error", l.Level())
	}

	if err := l.SetLevel("not-a-level"); err == nil {
		t.Error("SetLevel with invalid input should return an error")
	}
}

func TestLevelHandler(t *testing.T) {
	l, err := NewLogger(LoggerConfig{Mode: settings.EnvProd})
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}

	rec := httptest.NewRecorder()
	l.LevelHandler().ServeHTTP(rec, httptest.NewRequest("GET", "/log/level", nil))

	var body struct {
		Level string `json:"level"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if body.Level != "info" {
		t.Errorf("level = %q, want info", body.Level)
	}
}

func TestServiceMetadataAndFileOutput(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "app.log")
	l, err := NewLogger(LoggerConfig{
		Mode:     settings.EnvProd,
		Service:  "example-service",
		Env:      "test",
		Version:  "1.2.3",
		Filename: logFile,
	})
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}

	l.Info("hello")
	if err := l.Sync(); err != nil {
		t.Logf("Sync: %v", err) // stdout sync can fail on some platforms; file matters here
	}

	raw, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}

	var entry map[string]any
	if err := json.Unmarshal(raw, &entry); err != nil {
		t.Fatalf("log entry is not JSON: %v\n%s", err, raw)
	}
	for k, want := range map[string]string{"service": "example-service", "environment": "test", "version": "1.2.3", "message": "hello"} {
		if entry[k] != want {
			t.Errorf("entry[%q] = %v, want %q", k, entry[k], want)
		}
	}
}

func TestNewLoggerRejectsInvalidConfiguration(t *testing.T) {
	t.Run("invalid level", func(t *testing.T) {
		if _, err := NewLogger(LoggerConfig{Level: "verbose"}); err == nil {
			t.Fatal("NewLogger accepted an invalid level")
		}
	})

	t.Run("negative rotation", func(t *testing.T) {
		if _, err := NewLogger(LoggerConfig{MaxSize: -1}); err == nil {
			t.Fatal("NewLogger accepted negative MaxSize")
		}
	})

	for _, tc := range []struct {
		name string
		cfg  LoggerConfig
	}{
		{name: "negative backups", cfg: LoggerConfig{MaxBackups: -1}},
		{name: "negative age", cfg: LoggerConfig{MaxAge: -1}},
		{name: "negative initial sample", cfg: LoggerConfig{SamplingInitial: -1}},
		{name: "negative subsequent sample", cfg: LoggerConfig{SamplingThereafter: -1}},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewLogger(tc.cfg); err == nil {
				t.Fatal("NewLogger accepted invalid negative configuration")
			}
		})
	}

	t.Run("file is directory", func(t *testing.T) {
		if _, err := NewLogger(LoggerConfig{Filename: t.TempDir()}); err == nil {
			t.Fatal("NewLogger accepted a directory as a log file")
		}
	})

	t.Run("parent is file", func(t *testing.T) {
		parent := filepath.Join(t.TempDir(), "parent")
		if err := os.WriteFile(parent, []byte("not a directory"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if _, err := NewLogger(LoggerConfig{Filename: filepath.Join(parent, "app.log")}); err == nil {
			t.Fatal("NewLogger accepted a file as the log directory")
		}
	})
}

func TestMustNewLoggerCompatibility(t *testing.T) {
	if logger := MustNewLogger(LoggerConfig{Mode: settings.EnvProd}); logger == nil {
		t.Fatal("MustNewLogger returned nil")
	}

	defer func() {
		if recover() == nil {
			t.Fatal("MustNewLogger did not panic for invalid configuration")
		}
	}()
	_ = MustNewLogger(LoggerConfig{Level: "invalid"})
}
