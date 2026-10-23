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
		mode settings.Env
		want zapcore.Level
	}{
		{settings.EnvDev, zapcore.DebugLevel},
		{settings.EnvStaging, zapcore.InfoLevel},
	} {
		log, err := NewLogger(LoggerConfig{Mode: test.mode})
		if err != nil {
			t.Fatalf("NewLogger(%q): %v", test.mode, err)
		}
		if got := log.Level(); got != test.want {
			t.Errorf("Level() for %q = %v, want %v", test.mode, got, test.want)
		}
	}
}

func TestNewLoggerRejectsInvalidConfigAndFilePaths(t *testing.T) {
	if _, err := NewLogger(LoggerConfig{MaxSize: -1}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("invalid config error = %v, want ErrInvalidConfig", err)
	}
	if _, err := NewLogger(LoggerConfig{Level: "not-a-zap-level"}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("invalid level error = %v, want ErrInvalidConfig", err)
	}
	if _, err := NewLogger(LoggerConfig{Filename: t.TempDir()}); err == nil {
		t.Fatal("NewLogger accepted a directory as the log file")
	}
	parentFile := filepath.Join(t.TempDir(), "parent")
	if err := os.WriteFile(parentFile, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewLogger(LoggerConfig{Filename: filepath.Join(parentFile, "service.log")}); err == nil {
		t.Fatal("NewLogger accepted a regular file as a log directory")
	}
}

func TestMustNewLoggerPanicsOnInvalidConfig(t *testing.T) {
	if log := MustNewLogger(LoggerConfig{Mode: settings.EnvProd}); log == nil {
		t.Fatal("MustNewLogger returned nil for valid config")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("MustNewLogger did not panic")
		}
	}()
	MustNewLogger(LoggerConfig{Level: "invalid"})
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

func TestLoggerNilAndSyncErrors(t *testing.T) {
	var nilLogger *Logger
	if err := nilLogger.Sync(); !errors.Is(err, ErrInvalidLogger) {
		t.Fatalf("nil Sync() error = %v", err)
	}
	if err := (*Logger)(nil).Sync(); !errors.Is(err, ErrInvalidLogger) {
		t.Fatalf("invalid Sync() error = %v", err)
	}
	for _, err := range []error{syscall.ENOTTY, syscall.EINVAL, syscall.EBADF, syscall.ENOTSUP} {
		if !isStdoutSyncErr(err) {
			t.Errorf("isStdoutSyncErr(%v) = false", err)
		}
	}
	if isStdoutSyncErr(errors.New("real sync failure")) {
		t.Fatal("ordinary sync error was treated as stdout error")
	}
	base := zap.New(zapcore.NewCore(zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig()), failingSyncer{}, zapcore.DebugLevel))
	if err := Wrap(base).Sync(); err == nil || err.Error() != "sync failed" {
		t.Fatalf("Sync() error = %v, want sync failure", err)
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

type failingSyncer struct{}

func (failingSyncer) Write(data []byte) (int, error) { return len(data), nil }
func (failingSyncer) Sync() error                    { return errors.New("sync failed") }
