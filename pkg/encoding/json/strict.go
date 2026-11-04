package json

import (
	jsonv2 "encoding/json/v2"
	"errors"
)

const (
	maxStrictNesting      = 128
	DefaultStrictMaxBytes = 1 << 20
	HardStrictMaxBytes    = 16 << 20
)

var ErrInvalidDocument = errors.New("json: invalid strict document")

// UnmarshalStrict rejects invalid UTF-8, duplicate object keys at any depth,
// unknown struct fields, excessive nesting, and trailing JSON values before
// decoding into target. It is intended for bounded untrusted protocol input;
// the default input cap is one MiB; protocols with smaller budgets should use
// UnmarshalStrictLimit.
func UnmarshalStrict(data []byte, target any) error {
	return UnmarshalStrictLimit(data, target, DefaultStrictMaxBytes)
}

// UnmarshalStrictLimit applies the strict contract with an explicit byte cap.
func UnmarshalStrictLimit(data []byte, target any, maximum int) error {
	if maximum <= 0 || maximum > HardStrictMaxBytes || len(data) == 0 || len(data) > maximum {
		return ErrInvalidDocument
	}
	if err := validateStrictNesting(data); err != nil {
		return ErrInvalidDocument
	}

	if err := jsonv2.Unmarshal(data, target, jsonv2.RejectUnknownMembers(true)); err != nil {
		return errors.Join(ErrInvalidDocument, err)
	}
	return nil
}

// validateStrictNesting cheaply enforces the package's lower nesting limit.
// json/v2 performs the full syntax validation, including matching delimiters,
// rejecting duplicate names, and rejecting invalid UTF-8.
func validateStrictNesting(data []byte) error {
	depth := 0
	inString := false
	escaped := false

	for _, b := range data {
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch b {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}

		switch b {
		case '"':
			inString = true
		case '{', '[':
			depth++
			if depth > maxStrictNesting {
				return ErrInvalidDocument
			}
		case '}', ']':
			if depth > 0 {
				depth--
			}
		}
	}
	return nil
}
