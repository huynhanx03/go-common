package logger

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/huynhanx03/go-common/pkg/settings"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestNewLoggerWritesConfiguredStructuredOutput(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "nested", "service.log")
	log, err := NewLogger(LoggerConfig{
		Mode: settings.EnvProd, Service: "api", Env: settings.EnvProd,
		Version: "1.2.3", Filename: filename, DisableSampling: true,
	})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	log.Info(WithRequestID(context.Background(), "req-1"), "started")
	if err := log.Sync(); err != nil && !isStdoutSyncErr(err) {
		t.Fatalf("Sync() error = %v", err)
	}
	raw, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	var entry map[string]any
	if err := json.Unmarshal(raw, &entry); err != nil {
		t.Fatalf("log entry is not JSON: %v", err)
	}
	for key, want := range map[string]string{
		fieldService: "api", fieldEnvironment: string(settings.EnvProd),
		fieldVersion: "1.2.3", fieldMessage: "started", fieldRequestID: "req-1",
	} {
		if entry[key] != want {
			t.Errorf("entry[%q] = %v, want %q", key, entry[key], want)
		}
	}
}

func TestNewLoggerSelectsDevAndProductionLevels(t *testing.T) {
	for _, test := range []struct {
		name   string
		config LoggerConfig
		want   zapcore.Level
	}{
		{"default mode", LoggerConfig{}, zapcore.DebugLevel},
		{"development", LoggerConfig{Mode: settings.EnvDev}, zapcore.DebugLevel},
		{"staging", LoggerConfig{Mode: settings.EnvStaging}, zapcore.InfoLevel},
		{"production", LoggerConfig{Mode: settings.EnvProd}, zapcore.InfoLevel},
	} {
		t.Run(test.name, func(t *testing.T) {
			log, err := NewLogger(test.config)
			if err != nil {
				t.Fatalf("NewLogger(): %v", err)
			}
			if got := log.Level(); got != test.want {
				t.Errorf("Level() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestNewLoggerRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		change func(*LoggerConfig)
	}{
		{"negative rotation size", func(c *LoggerConfig) { c.MaxSize = -1 }},
		{"invalid level", func(c *LoggerConfig) { c.Level = "not-a-level" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := LoggerConfig{Mode: settings.EnvProd, DisableSampling: true}
			test.change(&config)
			if _, err := NewLogger(config); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("NewLogger() error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestNewLoggerRejectsUnusableFilePaths(t *testing.T) {
	parentFile := filepath.Join(t.TempDir(), "parent")
	if err := os.WriteFile(parentFile, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		filename string
	}{
		{"directory as log file", t.TempDir()},
		{"file as parent directory", filepath.Join(parentFile, "service.log")},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewLogger(LoggerConfig{Mode: settings.EnvProd, Filename: test.filename}); err == nil {
				t.Fatal("NewLogger() accepted an unusable file path")
			}
		})
	}
}

func TestMustNewLoggerPanicsOnInvalidConfig(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("MustNewLogger did not panic")
		}
	}()
	MustNewLogger(LoggerConfig{Level: "invalid"})
}

func TestMustNewLoggerReturnsUsableLogger(t *testing.T) {
	log := MustNewLogger(LoggerConfig{Mode: settings.EnvProd, DisableSampling: true})
	if err := log.SetLevel("debug"); err != nil || log.CurrentLevel() != "debug" {
		t.Fatalf("MustNewLogger result level = %q, err = %v", log.CurrentLevel(), err)
	}
}

func TestLoggerLevelControlAndUnavailableHandler(t *testing.T) {
	log, err := NewLogger(LoggerConfig{Mode: settings.EnvProd})
	if err != nil {
		t.Fatal(err)
	}
	if err := log.SetLevel("debug"); err != nil || log.CurrentLevel() != "debug" || log.Level() != zapcore.DebugLevel {
		t.Fatalf("level update: current=%q level=%v err=%v", log.CurrentLevel(), log.Level(), err)
	}
	if err := log.SetLevel("not-a-level"); err == nil {
		t.Fatal("SetLevel accepted invalid level")
	}
	var response struct {
		Level string `json:"level"`
	}
	request := httptest.NewRequest("GET", "/level", nil)
	writer := httptest.NewRecorder()
	log.LevelHandler().ServeHTTP(writer, request)
	if err := json.Unmarshal(writer.Body.Bytes(), &response); err != nil || response.Level != "debug" {
		t.Fatalf("level endpoint response=%q, err=%v", writer.Body.String(), err)
	}
	var zero *Logger
	if err := zero.SetLevel("info"); !errors.Is(err, ErrInvalidLogger) {
		t.Fatalf("nil SetLevel error = %v", err)
	}
	if zero.CurrentLevel() != "" || zero.Level() != zapcore.InfoLevel {
		t.Fatal("nil logger returned a current level")
	}
	writer = httptest.NewRecorder()
	zero.LevelHandler().ServeHTTP(writer, request)
	if writer.Code != 503 || !strings.Contains(writer.Body.String(), responseUnavailable) {
		t.Fatalf("unavailable handler status/body = %d/%q", writer.Code, writer.Body.String())
	}
}

func TestLoggerSyncHandlesInvalidAndUnsupportedErrors(t *testing.T) {
	var nilLogger *Logger
	if err := nilLogger.Sync(); !errors.Is(err, ErrInvalidLogger) {
		t.Fatalf("nil Sync() error = %v", err)
	}
	if err := (&Logger{}).Sync(); !errors.Is(err, ErrInvalidLogger) {
		t.Fatalf("zero logger Sync() error = %v", err)
	}
	for _, syncErr := range []error{syscall.ENOTTY, syscall.EINVAL, syscall.EBADF, syscall.ENOTSUP} {
		log := Wrap(zap.New(zapcore.NewCore(
			zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig()),
			failingSyncer{err: syncErr},
			zapcore.DebugLevel,
		)))
		if err := log.Sync(); err != nil {
			t.Errorf("Sync() for unsupported stream error %v = %v, want nil", syncErr, err)
		}
	}
	want := errors.New("sync failed")
	log := Wrap(zap.New(zapcore.NewCore(
		zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig()),
		failingSyncer{err: want},
		zapcore.DebugLevel,
	)))
	if err := log.Sync(); !errors.Is(err, want) {
		t.Fatalf("Sync() error = %v, want %v", err, want)
	}
}

func TestLoggerFatalExits(t *testing.T) {
	if os.Getenv("GO_COMMON_LOGGER_FATAL") == "1" {
		Wrap(zap.NewNop()).Fatal(context.Background(), "fatal child")
		return
	}
	command := exec.Command(os.Args[0], "-test.run=^TestLoggerFatalExits$")
	command.Env = append(os.Environ(), "GO_COMMON_LOGGER_FATAL=1")
	err := command.Run()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() == 0 {
		t.Fatalf("fatal subprocess error = %v, want nonzero exit", err)
	}
}

type failingSyncer struct{ err error }

func (failingSyncer) Write(data []byte) (int, error) { return len(data), nil }
func (syncer failingSyncer) Sync() error             { return syncer.err }
