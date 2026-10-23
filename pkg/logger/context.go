package logger

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

var reservedFieldKeys = map[string]struct{}{
	fieldTimestamp:   {},
	fieldLevel:       {},
	fieldService:     {},
	fieldVersion:     {},
	fieldEnvironment: {},
	fieldComponent:   {},
	fieldMessage:     {},
	fieldRequestID:   {},
	fieldCorrelation: {},
	fieldTraceID:     {},
	fieldSpanID:      {},
}

// sensitiveFieldKeys are values that must never become ambient context. A
// context field is repeated by every downstream log entry, so an accidental
// credential or opaque payload here has a much larger blast radius than a
// single call-site mistake. Identifiers such as token_id remain allowed; only
// fields whose value is itself secret or unbounded content are rejected.
var sensitiveFieldKeys = map[string]struct{}{
	"access_token":         {},
	"api_key":              {},
	"apikey":               {},
	"authorization":        {},
	"authorization_header": {},
	"bearer_token":         {},
	"body":                 {},
	"ciphertext":           {},
	"client_secret":        {},
	"cookie":               {},
	"cookies":              {},
	"credential":           {},
	"credentials":          {},
	"id_token":             {},
	"password":             {},
	"password_hash":        {},
	"passwd":               {},
	"payload":              {},
	"private_key":          {},
	"raw_password":         {},
	"raw_token":            {},
	"recovery_code":        {},
	"refresh_token":        {},
	"request_body":         {},
	"response_body":        {},
	"secret":               {},
	"set_cookie":           {},
	"source":               {},
	"source_code":          {},
	"token":                {},
	"token_hash":           {},
}

type fieldsContextKey struct{}

// Field is a vendor-neutral structured logging field suitable for storage in
// context. It is converted to a Zap field only at the logger boundary.
type Field struct {
	Key   string
	Value string
}

// String constructs a structured string field.
func String(key, value string) Field {
	return Field{Key: key, Value: value}
}

// InheritContext copies validated structured fields from source into
// destination. Request IDs, authentication principals, deadlines, and
// cancellation remain attached to their own contexts.
//
// Use this when a long-lived operation must be rooted in a lifecycle context
// while retaining request-scoped log fields.
func InheritContext(destination, source context.Context) context.Context {
	destination = nonNilContext(destination)
	if source == nil {
		return destination
	}
	fields, _ := source.Value(fieldsContextKey{}).([]Field)
	if len(fields) == 0 {
		return destination
	}
	return WithFields(destination, fields...)
}

// WithFields stores a bounded immutable copy of valid fields in ctx. Reserved,
// duplicate, malformed, and oversize fields are ignored.
func WithFields(ctx context.Context, fields ...Field) context.Context {
	ctx = nonNilContext(ctx)

	existing, _ := ctx.Value(fieldsContextKey{}).([]Field)
	accepted := make([]Field, 0, min(maxContextFields, len(existing)+len(fields)))
	seen := make(map[string]struct{}, maxContextFields)
	appendValid := func(field Field) {
		if len(accepted) >= maxContextFields || !validField(field) {
			return
		}
		normalized := strings.ToLower(field.Key)
		if _, exists := seen[normalized]; exists {
			return
		}
		seen[normalized] = struct{}{}
		accepted = append(accepted, field)
	}
	for _, field := range existing {
		appendValid(field)
	}
	for _, field := range fields {
		appendValid(field)
	}
	if len(accepted) == 0 {
		return ctx
	}
	return context.WithValue(ctx, fieldsContextKey{}, accepted)
}

func contextLogFields(ctx context.Context) []zap.Field {
	fields, _ := ctx.Value(fieldsContextKey{}).([]Field)
	zapFields := make([]zap.Field, 0, len(fields)+4)
	if id := RequestID(ctx); id != "" {
		zapFields = append(zapFields, zap.String(fieldRequestID, id))
	}
	if id := CorrelationID(ctx); id != "" {
		zapFields = append(zapFields, zap.String(fieldCorrelation, id))
	}
	if span := trace.SpanContextFromContext(ctx); span.IsValid() {
		zapFields = append(zapFields,
			zap.String(fieldTraceID, span.TraceID().String()),
			zap.String(fieldSpanID, span.SpanID().String()),
		)
	}
	for _, field := range fields {
		if validField(field) {
			zapFields = append(zapFields, zap.String(field.Key, field.Value))
		}
	}
	return zapFields
}

func validField(field Field) bool {
	if len(field.Key) == 0 || len(field.Key) > maxFieldKeyBytes {
		return false
	}
	normalized := strings.ToLower(field.Key)
	if _, reserved := reservedFieldKeys[normalized]; reserved {
		return false
	}
	if isSensitiveFieldKey(normalized) {
		return false
	}
	for index := 0; index < len(field.Key); index++ {
		char := field.Key[index]
		if (char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			char == '_' || char == '.' || char == '-' {
			continue
		}
		return false
	}
	if len(field.Value) > maxFieldValueBytes || !utf8.ValidString(field.Value) {
		return false
	}
	for _, char := range field.Value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func isSensitiveFieldKey(normalized string) bool {
	if _, sensitive := sensitiveFieldKeys[normalized]; sensitive {
		return true
	}
	parts := strings.FieldsFunc(normalized, func(char rune) bool {
		return char == '_' || char == '.' || char == '-'
	})
	for index, part := range parts {
		switch part {
		case "password", "passwd", "secret", "cookie", "ciphertext", "payload":
			return true
		case "token":
			// Token record identifiers are safe correlation references. The
			// capability itself and every other token-shaped value remain denied.
			if index+2 != len(parts) || parts[index+1] != "id" {
				return true
			}
		case "credential":
			if index+2 != len(parts) || parts[index+1] != "id" {
				return true
			}
		case "authorization":
			if index+1 < len(parts) && parts[index+1] == "header" {
				return true
			}
		case "request", "response", "raw":
			if index+1 < len(parts) && parts[index+1] == "body" {
				return true
			}
		case "source":
			// Provenance metadata is safe; an unconstrained source value is not.
			if index+1 == len(parts) {
				return true
			}
			switch parts[len(parts)-1] {
			case "id", "kind", "type", "checksum", "version":
				continue
			default:
				return true
			}
		case "private", "api":
			if index+1 < len(parts) && parts[index+1] == "key" {
				return true
			}
		}
	}
	return false
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
