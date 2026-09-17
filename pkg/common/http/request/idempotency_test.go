package request

import (
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func TestParseCanonicalUUIDIdempotencyKeyRequiresExactlyOneCanonicalValue(t *testing.T) {
	t.Parallel()
	key := uuid.MustParse("abcdef01-2345-4789-abcd-ef0123456789")
	header := make(http.Header)
	header.Set(IdempotencyKeyHeader, key.String())
	parsed, err := ParseCanonicalUUIDIdempotencyKey(header)
	if err != nil {
		t.Fatalf("ParseCanonicalUUIDIdempotencyKey() error = %v", err)
	}
	if parsed != key {
		t.Fatalf("ParseCanonicalUUIDIdempotencyKey() = %s, want %s", parsed, key)
	}

	invalid := []http.Header{
		nil,
		{},
		{IdempotencyKeyHeader: []string{key.String(), uuid.NewString()}},
		{IdempotencyKeyHeader: []string{" " + key.String()}},
		{IdempotencyKeyHeader: []string{key.String() + " "}},
		{IdempotencyKeyHeader: []string{uuid.Nil.String()}},
		{IdempotencyKeyHeader: []string{"not-a-uuid"}},
		{IdempotencyKeyHeader: []string{stringsUpper(key.String())}},
	}
	for index, value := range invalid {
		if _, err := ParseCanonicalUUIDIdempotencyKey(value); !errors.Is(
			err,
			ErrInvalidIdempotencyKey,
		) {
			t.Fatalf("case %d error = %v", index, err)
		}
	}
}

func stringsUpper(value string) string {
	result := []byte(value)
	for index, current := range result {
		if current >= 'a' && current <= 'f' {
			result[index] = current - ('a' - 'A')
		}
	}
	return string(result)
}
