package forge

import "math"

// Delivery is one absolute queue record returned by Consumer.Fetch.
// CommitOffset expects the next offset (Delivery.Offset + 1) only after the
// application side effect has committed.
type Delivery struct {
	Offset    uint64
	Timestamp int64
	Key       []byte
	Value     []byte
	Headers   []Header
}

func deliveryFromRecord(offset uint64, timestamp int64, record Record) (Delivery, error) {
	if (record.TimestampDelta > 0 && timestamp > math.MaxInt64-record.TimestampDelta) ||
		(record.TimestampDelta < 0 && timestamp < math.MinInt64-record.TimestampDelta) {
		return Delivery{}, ErrCorruptRecord
	}
	copied := copyRecord(record)
	return Delivery{
		Offset:    offset,
		Timestamp: timestamp + record.TimestampDelta,
		Key:       copied.Key,
		Value:     copied.Value,
		Headers:   copied.Headers,
	}, nil
}

func (delivery Delivery) record() Record {
	return Record{
		Key:     delivery.Key,
		Value:   delivery.Value,
		Headers: delivery.Headers,
	}
}
