package forge

// copyRecord detaches a record from caller-owned or decoded storage.
func copyRecord(record Record) Record {
	record.Key = copyBytes(record.Key)
	record.Value = copyBytes(record.Value)
	if len(record.Headers) > 0 {
		headers := make([]Header, len(record.Headers))
		for index, header := range record.Headers {
			headers[index] = Header{
				Key:   copyBytes(header.Key),
				Value: copyBytes(header.Value),
			}
		}
		record.Headers = headers
	}
	return record
}

func copyBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	result := make([]byte, len(value))
	copy(result, value)
	return result
}
