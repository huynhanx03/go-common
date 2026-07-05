package logger

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap/zapcore"
)

func TestWithDefaultsDev(t *testing.T) {
	cfg := LoggerConfig{}.withDefaults()

	if cfg.Mode != ModeDev {
		t.Errorf("Mode = %q, want %q", cfg.Mode, ModeDev)
	}
	if cfg.Level != "debug" {
		t.Errorf("Level = %q, want debug", cfg.Level)
	}
}

func TestWithDefaultsProd(t *testing.T) {
	cfg := LoggerConfig{Mode: ModeProd}.withDefaults()

	if cfg.Level != "info" {
		t.Errorf("Level = %q, want info", cfg.Level)
	}
	if cfg.SamplingInitial != 100 || cfg.SamplingThereafter != 100 {
		t.Errorf("sampling defaults = %d/%d, want 100/100", cfg.SamplingInitial, cfg.SamplingThereafter)
	}
}

func TestDevLogsDebugProdDoesNot(t *testing.T) {
	dev := NewLogger(LoggerConfig{Mode: ModeDev})
	if !dev.Core().Enabled(zapcore.DebugLevel) {
		t.Error("dev logger should enable debug level")
	}

	prod := NewLogger(LoggerConfig{Mode: ModeProd})
	if prod.Core().Enabled(zapcore.DebugLevel) {
		t.Error("prod logger should not enable debug level")
	}
	if !prod.Core().Enabled(zapcore.InfoLevel) {
		t.Error("prod logger should enable info level")
	}
}

func TestSetLevelAtRuntime(t *testing.T) {
	l := NewLogger(LoggerConfig{Mode: ModeDev})

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
	l := NewLogger(LoggerConfig{Mode: ModeProd})

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
	l := NewLogger(LoggerConfig{
		Mode:     ModeProd,
		Service:  "judgify-api",
		Env:      "test",
		Version:  "1.2.3",
		Filename: logFile,
	})

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
	for k, want := range map[string]string{"service": "judgify-api", "env": "test", "version": "1.2.3", "msg": "hello"} {
		if entry[k] != want {
			t.Errorf("entry[%q] = %v, want %q", k, entry[k], want)
		}
	}
}
