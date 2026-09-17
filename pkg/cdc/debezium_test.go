package cdc

import (
	"errors"
	"strings"
	"testing"
	"time"
)

type debeziumDocument struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func TestParseDebeziumMessageFormatsAndOperations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		op     Operation
		before *debeziumDocument
		after  *debeziumDocument
	}{
		{
			name:  "envelope create",
			input: `{"schema":{"type":"struct"},"payload":{"before":null,"after":{"id":"1","name":"new"},"source":{"connector":"postgresql"},"op":"c","ts_ms":1}}`,
			op:    OpCreate,
			after: &debeziumDocument{ID: "1", Name: "new"},
		},
		{
			name:   "flat update",
			input:  `{"before":{"id":"1","name":"old"},"after":{"id":"1","name":"new"},"source":{},"op":"u","ts_ms":2}`,
			op:     OpUpdate,
			before: &debeziumDocument{ID: "1", Name: "old"},
			after:  &debeziumDocument{ID: "1", Name: "new"},
		},
		{
			name:   "stringified delete",
			input:  `{"before":"{\"id\":\"1\",\"name\":\"old\"}","after":null,"source":{},"op":"d","ts_ms":3}`,
			op:     OpDelete,
			before: &debeziumDocument{ID: "1", Name: "old"},
		},
		{
			name:  "snapshot read",
			input: `{"before":null,"after":{"id":"1","name":"snapshot"},"source":{},"op":"r","ts_ms":4}`,
			op:    OpRead,
			after: &debeziumDocument{ID: "1", Name: "snapshot"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseDebeziumMessage[debeziumDocument]([]byte(tt.input))
			if err != nil {
				t.Fatalf("ParseDebeziumMessage() error = %v", err)
			}
			if got.Op != tt.op {
				t.Fatalf("operation = %q, want %q", got.Op, tt.op)
			}
			if !documentsEqual(got.Before, tt.before) {
				t.Fatalf("before = %#v, want %#v", got.Before, tt.before)
			}
			if !documentsEqual(got.After, tt.after) {
				t.Fatalf("after = %#v, want %#v", got.After, tt.after)
			}
			if len(got.Source) == 0 {
				t.Fatal("source must be retained as bounded raw JSON")
			}
		})
	}
}

func TestParseDebeziumMessageRejectsInvalidContracts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  error
	}{
		{name: "tombstone", input: `null`, want: ErrTombstone},
		{name: "envelope tombstone", input: `{"payload":null}`, want: ErrTombstone},
		{name: "unknown operation", input: `{"before":null,"after":{"id":"1"},"source":{},"op":"x","ts_ms":1}`, want: ErrInvalidDebeziumEvent},
		{name: "create before present", input: `{"before":{"id":"1"},"after":{"id":"1"},"source":{},"op":"c","ts_ms":1}`, want: ErrInvalidDebeziumEvent},
		{name: "create after absent", input: `{"before":null,"after":null,"source":{},"op":"c","ts_ms":1}`, want: ErrInvalidDebeziumEvent},
		{name: "update missing before", input: `{"before":null,"after":{"id":"1"},"source":{},"op":"u","ts_ms":1}`, want: ErrInvalidDebeziumEvent},
		{name: "update missing after", input: `{"before":{"id":"1"},"after":null,"source":{},"op":"u","ts_ms":1}`, want: ErrInvalidDebeziumEvent},
		{name: "delete missing before", input: `{"before":null,"after":null,"source":{},"op":"d","ts_ms":1}`, want: ErrInvalidDebeziumEvent},
		{name: "delete after present", input: `{"before":{"id":"1"},"after":{"id":"1"},"source":{},"op":"d","ts_ms":1}`, want: ErrInvalidDebeziumEvent},
		{name: "missing before field", input: `{"after":{"id":"1"},"source":{},"op":"c","ts_ms":1}`, want: ErrInvalidDebeziumEvent},
		{name: "duplicate operation", input: `{"before":null,"after":{"id":"1"},"source":{},"op":"c","op":"d","ts_ms":1}`, want: ErrInvalidDebeziumEvent},
		{name: "nested duplicate", input: `{"before":null,"after":{"id":"1","id":"2"},"source":{},"op":"c","ts_ms":1}`, want: ErrInvalidDebeziumEvent},
		{name: "stringified duplicate", input: `{"before":null,"after":"{\"id\":\"1\",\"id\":\"2\"}","source":{},"op":"c","ts_ms":1}`, want: ErrInvalidDebeziumEvent},
		{name: "source is scalar", input: `{"before":null,"after":{"id":"1"},"source":"postgresql","op":"c","ts_ms":1}`, want: ErrInvalidDebeziumEvent},
		{name: "mixed wrapper and flat event", input: `{"payload":{"before":null,"after":{"id":"1"},"source":{},"op":"c","ts_ms":1},"before":null,"after":{"id":"2"},"source":{},"op":"c","ts_ms":2}`, want: ErrInvalidDebeziumEvent},
		{name: "array root", input: `[]`, want: ErrInvalidDebeziumEvent},
		{name: "trailing value", input: `{"before":null,"after":{"id":"1"},"source":{},"op":"c","ts_ms":1}{}`, want: ErrInvalidDebeziumEvent},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := ParseDebeziumMessage[debeziumDocument]([]byte(tt.input))
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want errors.Is(_, %v)", err, tt.want)
			}
			if strings.Contains(err.Error(), tt.input) {
				t.Fatal("diagnostic leaked the untrusted payload")
			}
		})
	}
}

func TestParseDebeziumMessageEnforcesByteAndNestingLimits(t *testing.T) {
	t.Parallel()

	limits := ParseLimits{MaxMessageBytes: 128, MaxNestedBytes: 32}
	tooLarge := []byte(`{"before":null,"after":{"id":"` + strings.Repeat("a", 128) + `"},"source":{},"op":"c","ts_ms":1}`)
	if _, err := ParseDebeziumMessageWithLimits[debeziumDocument](tooLarge, limits); !errors.Is(err, ErrDebeziumTooLarge) {
		t.Fatalf("oversized message error = %v", err)
	}

	nestedTooLarge := []byte(`{"before":null,"after":"{\"id\":\"` + strings.Repeat("b", 48) + `\"}","source":{},"op":"c","ts_ms":1}`)
	limits.MaxMessageBytes = len(nestedTooLarge) + 1
	if _, err := ParseDebeziumMessageWithLimits[debeziumDocument](nestedTooLarge, limits); !errors.Is(err, ErrDebeziumTooLarge) {
		t.Fatalf("oversized nested value error = %v", err)
	}

	deep := strings.Repeat(`{"x":`, 130) + `0` + strings.Repeat(`}`, 130)
	input := []byte(`{"before":null,"after":{"id":"1"},"source":` + deep + `,"op":"c","ts_ms":1}`)
	limits = DefaultParseLimits()
	if _, err := ParseDebeziumMessageWithLimits[debeziumDocument](input, limits); !errors.Is(err, ErrInvalidDebeziumEvent) {
		t.Fatalf("excessive nesting error = %v", err)
	}

	sourceTooLarge := []byte(`{"before":null,"after":{"id":"1"},"source":{"connector":"` + strings.Repeat("s", 64) + `"},"op":"c","ts_ms":1}`)
	limits = ParseLimits{MaxMessageBytes: len(sourceTooLarge) + 1, MaxNestedBytes: 32}
	if _, err := ParseDebeziumMessageWithLimits[debeziumDocument](sourceTooLarge, limits); !errors.Is(err, ErrDebeziumTooLarge) {
		t.Fatalf("oversized source error = %v, want ErrDebeziumTooLarge", err)
	}
}

func TestParseErrorZeroValueIsSafe(t *testing.T) {
	t.Parallel()

	if got := (&ParseError{}).Error(); got != "cdc: parse error" {
		t.Fatalf("zero ParseError = %q, want bounded fallback", got)
	}
}

func TestParseMongoDBKey(t *testing.T) {
	t.Parallel()

	const oid = "507f1f77bcf86cd799439011"
	for _, input := range []string{
		oid,
		`"` + oid + `"`,
		`{"$oid":"` + oid + `"}`,
		`"{\"$oid\":\"` + oid + `\"}"`,
	} {
		got, err := ParseMongoDBKey([]byte(input))
		if err != nil {
			t.Fatalf("ParseMongoDBKey(%q) error = %v", input, err)
		}
		if got != oid {
			t.Fatalf("ParseMongoDBKey(%q) = %q, want %q", input, got, oid)
		}
	}
}

func TestParseMongoDBKeyRejectsMalformedInput(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		"",
		"not-an-object-id",
		`{"$oid":""}`,
		`{"$oid":"507f1f77bcf86cd799439011","$oid":"507f1f77bcf86cd799439012"}`,
		`{"other":"507f1f77bcf86cd799439011"}`,
		`"{malformed}"`,
		strings.Repeat("a", MaxMongoKeyBytes+1),
	} {
		_, err := ParseMongoDBKey([]byte(input))
		if !errors.Is(err, ErrInvalidMongoKey) {
			t.Fatalf("ParseMongoDBKey(%q) error = %v, want ErrInvalidMongoKey", input, err)
		}
		if strings.Contains(err.Error(), input) && input != "" {
			t.Fatal("diagnostic leaked the untrusted key")
		}
	}
}

func TestMongoDateToTime(t *testing.T) {
	t.Parallel()

	got, err := (&MongoDate{Date: 1_700_000_000_123}).ToTime()
	if err != nil {
		t.Fatalf("ToTime() error = %v", err)
	}
	if got.UnixMilli() != 1_700_000_000_123 {
		t.Fatalf("UnixMilli() = %d", got.UnixMilli())
	}

	for _, date := range []*MongoDate{
		nil,
		{Date: minMongoUnixMilli - 1},
		{Date: maxMongoUnixMilli + 1},
	} {
		if _, err := date.ToTime(); !errors.Is(err, ErrInvalidMongoDate) {
			t.Fatalf("ToTime(%v) error = %v, want ErrInvalidMongoDate", date, err)
		}
	}

	if minMongoUnixMilli != time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli() {
		t.Fatal("minimum date constant drifted")
	}
}

func documentsEqual(left, right *debeziumDocument) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}
