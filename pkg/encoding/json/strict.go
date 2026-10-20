package json

import (
	"bytes"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
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
// The default input cap is one MiB; protocols with smaller budgets should use
// UnmarshalStrictLimit.
func UnmarshalStrict(data []byte, target any) error {
	return UnmarshalStrictLimit(data, target, DefaultStrictMaxBytes)
}

// UnmarshalStrictLimit applies the strict contract with an explicit byte cap.
func UnmarshalStrictLimit(data []byte, target any, maximum int) error {
	if maximum <= 0 ||
		maximum > HardStrictMaxBytes ||
		len(data) == 0 ||
		len(data) > maximum ||
		!utf8.Valid(data) {
		return ErrInvalidDocument
	}
	if err := validateUniqueKeys(data); err != nil {
		return errors.Join(ErrInvalidDocument, err)
	}

	decoder := stdjson.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.Join(ErrInvalidDocument, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrInvalidDocument
	}
	return nil
}

type containerState struct {
	delimiter    stdjson.Delim
	keys         map[string]struct{}
	expectingKey bool
}

func validateUniqueKeys(data []byte) error {
	decoder := stdjson.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	stack := make([]containerState, 0, 8)
	rootStarted := false
	rootComplete := false

	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			if !rootComplete || len(stack) != 0 {
				return ErrInvalidDocument
			}
			return nil
		}
		if err != nil {
			return err
		}
		if rootComplete {
			return ErrInvalidDocument
		}

		if len(stack) > 0 {
			top := &stack[len(stack)-1]
			if top.delimiter == '{' && top.expectingKey {
				if closing, ok := token.(stdjson.Delim); ok && closing == '}' {
					stack = stack[:len(stack)-1]
					if len(stack) == 0 {
						rootComplete = true
					}
					continue
				}
				key, ok := token.(string)
				if !ok {
					return ErrInvalidDocument
				}
				if _, duplicate := top.keys[key]; duplicate {
					return fmt.Errorf("%w: duplicate object key", ErrInvalidDocument)
				}
				top.keys[key] = struct{}{}
				top.expectingKey = false
				continue
			}
		}

		if delimiter, compound := token.(stdjson.Delim); compound {
			switch delimiter {
			case '{', '[':
				if len(stack) >= maxStrictNesting {
					return fmt.Errorf("%w: nesting limit exceeded", ErrInvalidDocument)
				}
				markParentValueConsumed(stack)
				state := containerState{delimiter: delimiter}
				if delimiter == '{' {
					state.keys = make(map[string]struct{})
					state.expectingKey = true
				}
				stack = append(stack, state)
				rootStarted = true
			case '}', ']':
				if len(stack) == 0 || stack[len(stack)-1].delimiter+2 != delimiter {
					return ErrInvalidDocument
				}
				if stack[len(stack)-1].delimiter == '{' &&
					!stack[len(stack)-1].expectingKey {
					return ErrInvalidDocument
				}
				stack = stack[:len(stack)-1]
				if len(stack) == 0 {
					rootComplete = true
				}
			default:
				return ErrInvalidDocument
			}
			continue
		}

		if !rootStarted {
			rootStarted = true
		}
		markParentValueConsumed(stack)
		if len(stack) == 0 {
			rootComplete = true
		}
	}
}

func markParentValueConsumed(stack []containerState) {
	if len(stack) == 0 {
		return
	}
	parent := &stack[len(stack)-1]
	if parent.delimiter == '{' {
		parent.expectingKey = true
	}
}
