package cache

import (
	"errors"
	"testing"
	"time"
)

func TestEntryEncodingDistinguishesPositiveAndNegativeValues(t *testing.T) {
	original := newEntry("value", time.Minute, 37*time.Millisecond)
	data, err := encodeEntry(original)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeEntry[string](data)
	if err != nil || decoded.Value != "value" || decoded.DeltaMs != 37 || decoded.ExpireAt != original.ExpireAt {
		t.Fatalf("decoded entry = %+v, %v", decoded, err)
	}
	if _, err := decodeEntry[string]([]byte("{")); err == nil {
		t.Fatal("decodeEntry accepted malformed JSON")
	}
	negative, err := encodeNegativeEntry(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := encodeNegativeEntry(0); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("encodeNegativeEntry(0) = %v", err)
	}
	if _, err := decodeEntry[struct{}](negative); !errors.Is(err, errInvalidEntry) {
		t.Fatalf("decodeEntry(negative) = %v", err)
	}
	if _, err := decodeNegativeEntry([]byte("{")); err == nil {
		t.Fatal("decodeNegativeEntry accepted malformed JSON")
	}
	positiveEmpty, err := encodeEntry(entry[struct{}]{ExpireAt: time.Now().Add(time.Minute).UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeNegativeEntry(positiveEmpty); !errors.Is(err, errInvalidEntry) {
		t.Fatalf("decodeNegativeEntry(positive) = %v", err)
	}
	if expires, err := decodeNegativeEntry(negative); err != nil || time.Until(expires) <= 0 {
		t.Fatalf("decoded negative expiry = %v, %v", expires, err)
	}
}
