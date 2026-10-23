package logger

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/huynhanx03/go-common/pkg/settings"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestOutputConfigurationAndRotator(t *testing.T) {
	config := LoggerConfig{
		Service: "api", Env: settings.EnvProd, Version: "v2",
		Filename: "/tmp/service.log", MaxSize: 8, MaxBackups: 3, MaxAge: 4, Compress: true,
	}
	fields := serviceFields(config)
	if len(fields) != 3 {
		t.Fatalf("service fields = %d, want 3", len(fields))
	}
	if got := serviceFields(LoggerConfig{}); len(got) != 0 {
		t.Fatalf("empty service metadata produced fields: %#v", got)
	}
	fileConfig := fileEncoderConfig()
	if fileConfig.TimeKey != fieldTimestamp || fileConfig.MessageKey != fieldMessage || fileConfig.NameKey != fieldComponent {
		t.Fatalf("file encoder keys = %+v", fileConfig)
	}
	consoleConfig := consoleEncoderConfig()
	if consoleConfig.TimeKey != fieldTimestamp || consoleConfig.MessageKey != fieldMessage || consoleConfig.NameKey != fieldComponent {
		t.Fatalf("console encoder keys = %+v", consoleConfig)
	}
	rotator := newRotator(config)
	if rotator.Filename != config.Filename || rotator.MaxSize != config.MaxSize || rotator.MaxBackups != config.MaxBackups ||
		rotator.MaxAge != config.MaxAge || rotator.Compress != config.Compress {
		t.Fatalf("rotator config = %+v", rotator)
	}
}

func TestPrepareLogFileCreatesParentAndReturnsFilesystemErrors(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "nested", "service.log")
	if err := prepareLogFile(filename); err != nil {
		t.Fatalf("prepareLogFile() error = %v", err)
	}
	if info, err := os.Stat(filename); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("created file info=%v err=%v", info, err)
	}
	if err := prepareLogFile(t.TempDir()); err == nil {
		t.Fatal("prepareLogFile accepted directory as file")
	}
	parent := filepath.Join(t.TempDir(), "parent-file")
	if err := os.WriteFile(parent, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareLogFile(filepath.Join(parent, "child.log")); err == nil {
		t.Fatal("prepareLogFile accepted file as parent directory")
	}
}

func TestLoggerSyncReturnsUnrelatedError(t *testing.T) {
	want := errors.New("flush failure")
	core := errorSyncCore{err: want}
	log := Wrap(zap.New(core))
	if got := log.Sync(); !errors.Is(got, want) {
		t.Fatalf("Sync() error = %v, want %v", got, want)
	}
}

type errorSyncCore struct{ err error }

func (core errorSyncCore) Enabled(zapcore.Level) bool        { return true }
func (core errorSyncCore) With([]zapcore.Field) zapcore.Core { return core }
func (core errorSyncCore) Check(entry zapcore.Entry, checked *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	return checked.AddCore(entry, core)
}
func (errorSyncCore) Write(zapcore.Entry, []zapcore.Field) error { return nil }
func (core errorSyncCore) Sync() error                           { return core.err }
