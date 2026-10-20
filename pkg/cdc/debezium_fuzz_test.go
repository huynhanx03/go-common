package cdc

import (
	"errors"
	"testing"
)

func FuzzDebezium(f *testing.F) {
	seeds := [][]byte{
		[]byte(`null`),
		[]byte(`{"before":null,"after":{"id":"1"},"source":{},"op":"c","ts_ms":1}`),
		[]byte(`{"payload":{"before":{"id":"1"},"after":null,"source":{},"op":"d","ts_ms":1}}`),
		[]byte(`{"before":null,"after":"{\"id\":\"1\"}","source":{},"op":"r","ts_ms":1}`),
		[]byte(`{"before":null,"after":{"id":"1","id":"2"},"source":{},"op":"c","ts_ms":1}`),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > DefaultDebeziumMaxBytes+1 {
			return
		}
		event, err := ParseDebeziumMessage[debeziumDocument](input)
		if err != nil {
			if !errors.Is(err, ErrTombstone) &&
				!errors.Is(err, ErrDebeziumTooLarge) &&
				!errors.Is(err, ErrInvalidDebeziumEvent) {
				t.Fatalf("unexpected error class: %v", err)
			}
			return
		}
		if event == nil {
			t.Fatal("successful parse returned a nil event")
		}
		switch event.Op {
		case OpCreate, OpUpdate, OpDelete, OpRead:
		default:
			t.Fatalf("successful parse returned invalid operation %q", event.Op)
		}
	})
}
