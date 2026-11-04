package logger

import (
	"context"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestEventWritesDurationMetadataAndContext(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	log := Wrap(zap.New(core))
	ctx := WithRequestID(context.Background(), "request-event")
	duration := int64(42)
	log.Event(ctx, zapcore.InfoLevel, "completed", &duration,
		zap.String("operation", "save"), zap.Bool("success", true))
	entry := observed.All()[0]
	fields := entry.ContextMap()
	if fields[fieldRequestID] != "request-event" || fields[fieldCorrelation] != "request-event" {
		t.Fatalf("context IDs = %#v", fields)
	}
	if fields[fieldDurationMS] != int64(42) {
		t.Fatalf("duration_ms = %v", fields[fieldDurationMS])
	}
	metadata, ok := fields[fieldMetadata].(map[string]interface{})
	if !ok || metadata["operation"] != "save" || metadata["success"] != true {
		t.Fatalf("metadata = %#v", fields[fieldMetadata])
	}
}

func TestEventOptionalFieldsAndDisabledLevel(t *testing.T) {
	core, observed := observer.New(zapcore.InfoLevel)
	log := Wrap(zap.New(core))
	log.Event(context.Background(), zapcore.InfoLevel, "without optional fields", nil)
	log.Event(context.Background(), zapcore.DebugLevel, "disabled", nil, zap.String("ignored", "yes"))
	entries := observed.All()
	if len(entries) != 1 || entries[0].Message != "without optional fields" {
		t.Fatalf("entries = %#v", entries)
	}
	if len(entries[0].Context) != 0 {
		t.Fatalf("optional fields unexpectedly present: %#v", entries[0].Context)
	}
}
