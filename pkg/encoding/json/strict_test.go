package json_test

import (
	"errors"
	"strings"
	"testing"

	commonjson "github.com/huynhanx03/go-common/pkg/encoding/json"
)

func TestUnmarshalStrictRejectsAmbiguousDocuments(t *testing.T) {
	t.Parallel()

	type payload struct {
		Name string         `json:"name"`
		Data map[string]any `json:"data"`
	}
	var decoded payload
	if err := commonjson.UnmarshalStrict(
		[]byte(`{"name":"ok","data":{"nested":true}}`),
		&decoded,
	); err != nil {
		t.Fatalf("UnmarshalStrict: %v", err)
	}
	if decoded.Name != "ok" {
		t.Fatalf("decoded = %+v", decoded)
	}

	for _, document := range []string{
		`{"name":"one","name":"two","data":{}}`,
		`{"name":"one","\u006eame":"two","data":{}}`,
		`{"name":"one","data":{"key":1,"key":2}}`,
		`{"name":"one","unknown":true,"data":{}}`,
		`{"name":"one","data":{}}{"name":"two","data":{}}`,
		`{"name":`,
		strings.Repeat("[", 129) + strings.Repeat("]", 129),
	} {
		if err := commonjson.UnmarshalStrict([]byte(document), &decoded); err == nil {
			t.Fatalf("UnmarshalStrict(%q) error = nil", document)
		}
	}
}

func TestUnmarshalStrictValidatesArgumentsAndUTF8(t *testing.T) {
	t.Parallel()

	var target struct {
		Value string `json:"value"`
	}
	if err := commonjson.UnmarshalStrict([]byte(`{"value":"ok"}`), nil); err == nil {
		t.Fatal("nil target error = nil")
	}
	if err := commonjson.UnmarshalStrict(nil, &target); err == nil {
		t.Fatal("empty document error = nil")
	}
	invalidUTF8 := []byte{'{', '"', 'v', 'a', 'l', 'u', 'e', '"', ':', '"', 0xff, '"', '}'}
	if err := commonjson.UnmarshalStrict(invalidUTF8, &target); err == nil {
		t.Fatal("invalid UTF-8 error = nil")
	}
}

func TestUnmarshalStrictMatchesStructFieldNamesExactly(t *testing.T) {
	t.Parallel()

	var target struct {
		Name string `json:"name"`
	}
	if err := commonjson.UnmarshalStrict([]byte(`{"NAME":"case mismatch"}`), &target); err == nil {
		t.Fatal("case-mismatched field error = nil")
	}

	if err := commonjson.UnmarshalStrict(
		[]byte(`{"name":"`+strings.Repeat("[", 129)+`literal contains ] } and an escaped quote: \\\""}`),
		&target,
	); err != nil {
		t.Fatalf("UnmarshalStrict with delimiters in string: %v", err)
	}
}

func TestUnmarshalStrictLimitEnforcesByteBudget(t *testing.T) {
	t.Parallel()

	data := []byte(`{"value":"ok"}`)
	var target struct {
		Value string `json:"value"`
	}
	if err := commonjson.UnmarshalStrictLimit(data, &target, len(data)); err != nil {
		t.Fatalf("document at byte limit: %v", err)
	}

	for _, maximum := range []int{
		len(data) - 1,
		0,
		commonjson.HardStrictMaxBytes + 1,
	} {
		if err := commonjson.UnmarshalStrictLimit(data, &target, maximum); !errors.Is(err, commonjson.ErrInvalidDocument) {
			t.Errorf("maximum %d error = %v, want ErrInvalidDocument", maximum, err)
		}
	}
}
