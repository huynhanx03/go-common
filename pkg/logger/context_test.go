package logger

import (
	"context"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestLoggerAddsRequestTraceAndContextFields(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	log := Wrap(zap.New(core))
	ctx := WithRequestID(context.Background(), "request-42")
	ctx = WithFields(ctx, String("operation_id", "op-7"))
	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	ctx = trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled,
	}))

	log.Info(ctx, "request handled", zap.String("route", "/items"))

	entries := observed.All()
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	fields := entries[0].ContextMap()
	want := map[string]string{
		fieldRequestID: "request-42", fieldCorrelation: "request-42",
		fieldTraceID: traceID.String(), fieldSpanID: spanID.String(),
		"operation_id": "op-7", "route": "/items",
	}
	for key, value := range want {
		if fields[key] != value {
			t.Errorf("field %q = %v, want %q", key, fields[key], value)
		}
	}
}

func TestWithFieldsFiltersAndBoundsAmbientFields(t *testing.T) {
	ctx := WithFields(nil,
		String("valid", "first"),
		String("VALID", "duplicate"),
		String(fieldRequestID, "spoofed"),
		String("password", "secret"),
		String("invalid:key", "value"),
		String("line_break", "one\ntwo"),
		String("invalid_utf8", string([]byte{0xff})),
		String("oversized_value", strings.Repeat("x", maxFieldValueBytes+1)),
	)
	for index := 0; index < maxContextFields+1; index++ {
		ctx = WithFields(ctx, String("field_"+string(rune('a'+index)), "ok"))
	}
	core, observed := observer.New(zapcore.DebugLevel)
	Wrap(zap.New(core)).Info(ctx, "filtered")
	fields := observed.All()[0].ContextMap()
	if fields["valid"] != "first" {
		t.Fatalf("valid field = %v, want first", fields["valid"])
	}
	if _, exists := fields["VALID"]; exists {
		t.Fatal("case-insensitive duplicate field was accepted")
	}
	for _, key := range []string{fieldRequestID, "password", "invalid:key", "line_break", "invalid_utf8", "oversized_value"} {
		if _, exists := fields[key]; exists {
			t.Errorf("invalid field %q was accepted", key)
		}
	}
}

func TestWithFieldsInheritAndNilContexts(t *testing.T) {
	destination := WithFields(nil, String("destination", "kept"))
	if got := InheritContext(destination, nil); got != destination {
		t.Fatal("InheritContext with nil source changed destination")
	}
	if got := InheritContext(destination, context.Background()); got != destination {
		t.Fatal("InheritContext without source fields changed destination")
	}
	if got := WithFields(context.Background(), String("password", "blocked")); got != context.Background() {
		t.Fatal("WithFields with no accepted values changed the context")
	}
	source := WithFields(context.Background(), String("operation_id", "copied"))
	inherited := InheritContext(destination, source)
	core, observed := observer.New(zapcore.DebugLevel)
	Wrap(zap.New(core)).Info(inherited, "inherited")
	fields := observed.All()[0].ContextMap()
	if fields["destination"] != "kept" || fields["operation_id"] != "copied" {
		t.Fatalf("inherited fields = %#v", fields)
	}
}

func TestSensitiveFieldRules(t *testing.T) {
	cases := map[string]bool{
		"access_token": true, "api_key": true, "token": true,
		"refresh_token_id": false, "credential_id": false,
		"oauth.token.value": true, "request_body": true, "response.body": true,
		"raw_body": true, "authorization_header": true,
		"nested.password.value": true, "nested.credential.value": true,
		"nested.authorization.header": true, "nested.authorization.value": false,
		"nested.request.body": true, "nested.response.body": true,
		"nested.raw.body": true, "nested.source": true,
		"source": true, "source.id": false, "source.kind": false,
		"source.type": false, "source.checksum": false, "source.version": false,
		"source.payload": true, "private_key": true, "api.key": true,
		"ordinary_field": false,
	}
	for key, want := range cases {
		if got := isSensitiveFieldKey(key); got != want {
			t.Errorf("isSensitiveFieldKey(%q) = %t, want %t", key, got, want)
		}
	}
}

func TestContextFieldValidation(t *testing.T) {
	cases := []struct {
		name  string
		field Field
		valid bool
	}{
		{"empty key", Field{Value: "v"}, false},
		{"long key", Field{Key: strings.Repeat("k", maxFieldKeyBytes+1), Value: "v"}, false},
		{"reserved case insensitive", Field{Key: "REQUEST_ID", Value: "v"}, false},
		{"sensitive", Field{Key: "secret", Value: "v"}, false},
		{"invalid key rune", Field{Key: "bad:key", Value: "v"}, false},
		{"empty value", Field{Key: "valid.empty", Value: ""}, true},
		{"invalid value utf8", Field{Key: "valid", Value: string([]byte{0xff})}, false},
		{"control value", Field{Key: "valid", Value: "line\tbreak"}, false},
		{"valid field", Field{Key: "operation-id_1.test", Value: "ok"}, true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := validField(test.field); got != test.valid {
				t.Errorf("validField(%+v) = %t, want %t", test.field, got, test.valid)
			}
		})
	}
}
