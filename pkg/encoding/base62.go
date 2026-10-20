package encoding

import (
	"errors"
	"math"
	"strings"
)

const (
	alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	base     = int64(62)
	maxLen   = 11
)

var (
	ErrNegativeBase62Integer = errors.New("base62 integer codec: negative value")
	ErrBase62IntegerOverflow = errors.New("base62 integer codec: overflow")
	ErrInvalidBase62Integer  = errors.New("base62 integer codec: invalid encoding")
)

// Base62Encode converts the absolute value of an integer to Base62.
// Deprecated: this integer compatibility codec has ambiguous negative-value
// semantics; use pkg/encoding/base62 for canonical byte encoding.
func Base62Encode(id int64) string {
	if id == 0 {
		return string(alphabet[0])
	}

	var chars [maxLen]byte
	k := maxLen
	var n uint64
	if id < 0 {
		n = uint64(-(id + 1)) + 1
	} else {
		n = uint64(id)
	}

	for n > 0 {
		k--
		remainder := n % uint64(base)
		chars[k] = alphabet[remainder]
		n /= uint64(base)
	}

	return string(chars[k:])
}

// Base62EncodeChecked rejects negative integers.
func Base62EncodeChecked(id int64) (string, error) {
	if id < 0 {
		return "", ErrNegativeBase62Integer
	}
	return Base62Encode(id), nil
}

// Base62Decode converts a Base62 string back to a non-negative integer.
// Deprecated: use pkg/encoding/base62 for canonical byte encoding.
func Base62Decode(s string) (int64, error) {
	if len(s) == 0 || len(s) > maxLen {
		return 0, ErrInvalidBase62Integer
	}
	var id int64
	for _, char := range s {
		index := strings.IndexRune(alphabet, char)
		if index == -1 {
			return 0, ErrInvalidBase62Integer
		}
		if id > (math.MaxInt64-int64(index))/base {
			return 0, ErrBase62IntegerOverflow
		}
		id = id*base + int64(index)
	}
	return id, nil
}
