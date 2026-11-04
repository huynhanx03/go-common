package cache

import (
	"time"

	"github.com/huynhanx03/go-common/pkg/encoding/json"
)

type entry[T any] struct {
	Value    T     `json:"v"`
	ExpireAt int64 `json:"e"`
	DeltaMs  int64 `json:"d,omitempty"`
	Negative bool  `json:"n,omitempty"`
}

func newEntry[T any](value T, ttl, delta time.Duration) entry[T] {
	return entry[T]{
		Value:    value,
		ExpireAt: time.Now().Add(ttl).UnixMilli(),
		DeltaMs:  delta.Milliseconds(),
	}
}

func encodeEntry[T any](value entry[T]) ([]byte, error) {
	return json.Marshal(value)
}

func decodeEntry[T any](data []byte) (entry[T], error) {
	var cached entry[T]
	if err := json.Unmarshal(data, &cached); err != nil {
		return entry[T]{}, err
	}
	if cached.Negative {
		return entry[T]{}, errInvalidEntry
	}

	return cached, nil
}

func encodeNegativeEntry(ttl time.Duration) ([]byte, error) {
	if ttl <= 0 {
		return nil, ErrInvalidConfig
	}
	return json.Marshal(entry[struct{}]{
		ExpireAt: time.Now().Add(ttl).UnixMilli(),
		Negative: true,
	})
}

func decodeNegativeEntry(data []byte) (time.Time, error) {
	var cached entry[struct{}]
	if err := json.Unmarshal(data, &cached); err != nil {
		return time.Time{}, err
	}
	if !cached.Negative {
		return time.Time{}, errInvalidEntry
	}
	return time.UnixMilli(cached.ExpireAt), nil
}
