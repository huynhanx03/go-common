package encoding

import (
	"errors"
	"strings"
)

const (
	alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	base     = int64(62)
	maxLen   = 11
)

// Base62Encode converts an integer to a Base62 string
func Base62Encode(id int64) string {
	if id == 0 {
		return string(alphabet[0])
	}

	var chars [maxLen]byte
	k := maxLen
	n := id

	if n < 0 {
		n = -n
	}

	for n > 0 {
		k--
		remainder := n % base
		chars[k] = alphabet[remainder]
		n = n / base
	}

	return string(chars[k:])
}

// Base62Decode converts a Base62 string back to an integer
func Base62Decode(s string) (int64, error) {
	var id int64
	for _, char := range s {
		index := strings.IndexRune(alphabet, char)
		if index == -1 {
			return 0, errors.New("invalid character in base62 string")
		}
		id = id*base + int64(index)
	}
	return id, nil
}
