package cdc

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	commonjson "github.com/huynhanx03/go-common/pkg/encoding/json"
)

const (
	// MaxMongoKeyBytes bounds one Kafka key before parsing.
	MaxMongoKeyBytes   = 4 << 10
	mongoObjectIDBytes = 12
	mongoObjectIDChars = mongoObjectIDBytes * 2
)

var (
	ErrInvalidMongoKey  = errors.New("cdc: invalid MongoDB key")
	ErrInvalidMongoDate = errors.New("cdc: invalid MongoDB date")

	minMongoUnixMilli = time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	maxMongoUnixMilli = time.Date(9999, 12, 31, 23, 59, 59, int(time.Millisecond-time.Nanosecond), time.UTC).UnixMilli()
)

// MongoDate handles MongoDB's Extended JSON date format.
type MongoDate struct {
	Date int64 `json:"$date"`
}

// ToTime converts a bounded MongoDB date to UTC.
func (date *MongoDate) ToTime() (time.Time, error) {
	if date == nil || date.Date < minMongoUnixMilli || date.Date > maxMongoUnixMilli {
		return time.Time{}, ErrInvalidMongoDate
	}
	return time.UnixMilli(date.Date).UTC(), nil
}

// MongoID handles MongoDB's Extended JSON ObjectID format.
type MongoID struct {
	OID string `json:"$oid"`
}

// ParseMongoDBKey extracts and validates a 12-byte MongoDB ObjectID from a
// bare hex key, JSON string, extended-JSON object, or stringified object.
func ParseMongoDBKey(key []byte) (string, error) {
	if len(key) == 0 || len(key) > MaxMongoKeyBytes {
		return "", ErrInvalidMongoKey
	}
	trimmed := bytes.TrimSpace(key)
	if len(trimmed) == 0 {
		return "", ErrInvalidMongoKey
	}

	if isMongoObjectID(string(trimmed)) {
		return strings.ToLower(string(trimmed)), nil
	}

	switch trimmed[0] {
	case '"':
		var decoded string
		if err := commonjson.UnmarshalStrictLimit(trimmed, &decoded, MaxMongoKeyBytes); err != nil {
			return "", ErrInvalidMongoKey
		}
		if isMongoObjectID(decoded) {
			return strings.ToLower(decoded), nil
		}
		return parseMongoObject([]byte(decoded))
	case '{':
		return parseMongoObject(trimmed)
	default:
		return "", ErrInvalidMongoKey
	}
}

func parseMongoObject(data []byte) (string, error) {
	if len(data) == 0 || len(data) > MaxMongoKeyBytes {
		return "", ErrInvalidMongoKey
	}
	var id MongoID
	if err := commonjson.UnmarshalStrictLimit(data, &id, MaxMongoKeyBytes); err != nil ||
		!isMongoObjectID(id.OID) {
		return "", ErrInvalidMongoKey
	}
	return strings.ToLower(id.OID), nil
}

func isMongoObjectID(value string) bool {
	if len(value) != mongoObjectIDChars {
		return false
	}
	var decoded [mongoObjectIDBytes]byte
	_, err := hex.Decode(decoded[:], []byte(value))
	return err == nil
}
