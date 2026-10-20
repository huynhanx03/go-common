package logger

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/huynhanx03/go-common/pkg/correlation"
	"github.com/huynhanx03/go-common/pkg/settings"
)

func TestFromContextReturnsStoredLogger(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	ctx := WithContext(context.Background(), zap.New(core))

	FromContext(ctx).Info("stored")

	if logs.Len() != 1 || logs.All()[0].Message != "stored" {
		t.Errorf("expected entry via stored logger, got %v", logs.All())
	}
}

func TestFromContextFallbackAttachesCorrelationID(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	restore := zap.ReplaceGlobals(zap.New(core))
	defer restore()

	ctx := correlation.WithContext(context.Background(), "cid-777")
	FromContext(ctx).Info("via fallback")

	fields := logs.All()[0].ContextMap()
	if fields["correlation_id"] != "cid-777" {
		t.Errorf("correlation_id field = %v, want cid-777", fields["correlation_id"])
	}
}

func TestFromContextStoredLoggerAttachesCIDAndImmutableFields(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	ctx := WithContext(context.Background(), zap.New(core))
	ctx = correlation.WithContext(ctx, "cid-immutable")

	fields := []Field{String("operation_id", "before")}
	ctx = WithFields(ctx, fields...)
	fields[0].Value = "after"

	FromContext(ctx).Info("with fields")

	entry := logs.All()[0].ContextMap()
	if entry["correlation_id"] != "cid-immutable" {
		t.Fatalf("correlation_id = %v, want cid-immutable", entry["correlation_id"])
	}
	if entry["operation_id"] != "before" {
		t.Fatalf("operation_id = %v, want immutable value before", entry["operation_id"])
	}
}

func TestWithFieldsRejectsReservedDuplicateAndOversizeFields(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	ctx := WithContext(context.Background(), zap.New(core))

	fields := []Field{
		String("cid", "spoofed"),
		String("service", "spoofed"),
		String("password", "hunter2"),
		String("access_token", "top-secret"),
		String("source_code", "print-secret"),
		String("payload", "opaque-secret"),
		String("password_hash", "credential-equivalent"),
		String("raw_token", "opaque-secret"),
		String("database.password", "nested-secret"),
		String("oauth-access-token", "prefixed-secret"),
		String("http.request_body", "unbounded-secret"),
		String("message.source_code", "source-secret"),
		String("message.source", "source-secret"),
		String("service.private_key", "key-secret"),
		String("refresh_token_id", "safe-record-reference"),
		String("source_event_id", "safe-source-reference"),
		String("valid", "first"),
		String("valid", "second"),
		String(strings.Repeat("k", maxFieldKeyBytes+1), "oversize-key"),
		String("oversize_value", strings.Repeat("v", maxFieldValueBytes+1)),
	}
	for i := 0; i < maxContextFields+10; i++ {
		fields = append(fields, String(fmt.Sprintf("field_%02d", i), "value"))
	}

	ctx = WithFields(ctx, fields...)
	FromContext(ctx).Info("bounded")

	entry := logs.All()[0].ContextMap()
	if _, exists := entry["cid"]; exists {
		t.Fatal("reserved cid field was accepted without a canonical correlation ID")
	}
	if _, exists := entry["service"]; exists {
		t.Fatal("reserved service field was accepted")
	}
	for _, key := range []string{
		"password", "access_token", "source_code", "payload", "password_hash", "raw_token",
		"database.password", "oauth-access-token", "http.request_body",
		"message.source_code", "message.source", "service.private_key",
	} {
		if _, exists := entry[key]; exists {
			t.Fatalf("sensitive field %q was accepted", key)
		}
	}
	if entry["refresh_token_id"] != "safe-record-reference" {
		t.Fatalf("safe token record identifier was rejected: %v", entry["refresh_token_id"])
	}
	if entry["source_event_id"] != "safe-source-reference" {
		t.Fatalf("safe source record identifier was rejected: %v", entry["source_event_id"])
	}
	if entry["valid"] != "first" {
		t.Fatalf("duplicate key overwrote first value: %v", entry["valid"])
	}
	if _, exists := entry["oversize_value"]; exists {
		t.Fatal("oversize value was accepted")
	}
	if got := len(entry); got != maxContextFields {
		t.Fatalf("context fields = %d, want bounded at %d", got, maxContextFields)
	}
}

func TestInheritContextCopiesLoggerAndFieldsWithoutDuplicatingCorrelation(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	source := WithContext(context.Background(), zap.New(core))
	source = correlation.WithContext(source, "source-cid")
	source = WithFields(source, String("operation_id", "operation-1"))

	destination := correlation.WithContext(context.Background(), "destination-cid")
	destination = InheritContext(destination, source)
	FromContext(destination).Info("inherited")

	entries := logs.FilterMessage("inherited").All()
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	fields := entries[0].ContextMap()
	if fields["correlation_id"] != "destination-cid" || fields["operation_id"] != "operation-1" {
		t.Fatalf("inherited fields = %+v", fields)
	}
	cidFields := 0
	for _, field := range entries[0].Context {
		if field.Key == "correlation_id" {
			cidFields++
		}
	}
	if cidFields != 1 {
		t.Fatalf("cid fields = %d, want exactly 1", cidFields)
	}
}

func TestContextHelpersAreNilSafe(t *testing.T) {
	if got := FromContext(nil); got == nil {
		t.Fatal("FromContext(nil) returned nil logger")
	}
	if ctx := WithContext(nil, nil); ctx == nil {
		t.Fatal("WithContext(nil, nil) returned nil context")
	}
	ctx := WithFields(nil, String("operation_id", "operation-1"))
	if ctx == nil {
		t.Fatal("WithFields(nil, ...) returned nil context")
	}
}

func TestWithFieldsRejectsMalformedValues(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	ctx := WithContext(context.Background(), zap.New(core))
	ctx = WithFields(ctx,
		String("valid.empty", ""),
		String("invalid:key", "value"),
		String("control", "line\nbreak"),
		String("invalid_utf8", string([]byte{0xff})),
	)

	FromContext(ctx).Info("validated")
	entry := logs.All()[0].ContextMap()
	if _, exists := entry["valid.empty"]; !exists {
		t.Fatal("valid empty string field was rejected")
	}
	for _, key := range []string{"invalid:key", "control", "invalid_utf8"} {
		if _, exists := entry[key]; exists {
			t.Fatalf("malformed field %q was accepted", key)
		}
	}
}

func TestSyncIgnoresStdoutError(t *testing.T) {
	l, err := NewLogger(LoggerConfig{Mode: settings.EnvProd})
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	l.Info("before sync")

	// Under `go test`, stdout is a pipe/char device whose fsync fails —
	// exactly the case Sync must swallow.
	if err := l.Sync(); err != nil {
		t.Errorf("Sync() = %v, want nil", err)
	}
}
