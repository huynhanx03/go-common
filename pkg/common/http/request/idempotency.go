package request

import (
	"net/http"

	"github.com/google/uuid"
)

// ParseCanonicalUUIDIdempotencyKey accepts exactly one lower-case,
// hyphenated UUID header value. It is transport-only validation: callers must
// still bind the key to their canonical command hash and durable receipt.
func ParseCanonicalUUIDIdempotencyKey(
	header http.Header,
) (uuid.UUID, error) {
	if header == nil {
		return uuid.Nil, ErrInvalidIdempotencyKey
	}
	values := header.Values(IdempotencyKeyHeader)
	if len(values) != 1 || values[0] == "" {
		return uuid.Nil, ErrInvalidIdempotencyKey
	}
	parsed, err := uuid.Parse(values[0])
	if err != nil || parsed == uuid.Nil || parsed.String() != values[0] {
		return uuid.Nil, ErrInvalidIdempotencyKey
	}
	return parsed, nil
}
