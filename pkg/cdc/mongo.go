package cdc

import (
	"encoding/json"
	"strings"
	"time"
)

// MongoDate handles MongoDB's Extended JSON date format: {"$date": 1234567890}
type MongoDate struct {
	Date int64 `json:"$date"`
}

// ToTime converts the MongoDate to a standard Go time.Time
func (m *MongoDate) ToTime() time.Time {
	return time.UnixMilli(m.Date)
}

// MongoID handles MongoDB's Extended JSON ID format: {"$oid": "..."}
type MongoID struct {
	OID string `json:"$oid"`
}

// ParseMongoDBKey extracts the ID string from a Kafka Key which might be a raw string or a JSON object
// This is a generic helper useful for any MongoDB CDC consumer.
func ParseMongoDBKey(key []byte) string {
	s := string(key)
	// Remove outer quotes if present (e.g. "694abc...")
	s = strings.Trim(s, "\"")

	// Quick check: if it looks like a JSON object with $oid
	if strings.HasPrefix(s, "{") && strings.Contains(s, "$oid") {
		var oid MongoID
		// Try parsing as {"$oid": "..."}
		if err := json.Unmarshal(key, &oid); err == nil && oid.OID != "" {
			return oid.OID
		}

		// Try parsing as stringified JSON: "{\" $oid\": ... }"
		var strContent string
		if err := json.Unmarshal(key, &strContent); err == nil {
			if err := json.Unmarshal([]byte(strContent), &oid); err == nil && oid.OID != "" {
				return oid.OID
			}
		}
	}

	return s
}
