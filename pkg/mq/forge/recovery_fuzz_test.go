package forge

import (
	"errors"
	"testing"
)

func FuzzRecovery(f *testing.F) {
	valid, err := EncodeBatch(&RecordBatch{
		RecordCount: 1,
		Records:     []Record{{Key: []byte("key"), Value: []byte("value")}},
	}, nil)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid)
	f.Add([]byte{})
	f.Add([]byte{0, 1, 2})
	f.Add(append(append([]byte(nil), valid...), 1, 2, 3))

	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > maxEncodedBatchBytes+1 {
			return
		}
		size, sizeErr := BatchSize(input)
		if sizeErr != nil {
			if !errors.Is(sizeErr, ErrCorruptBatch) &&
				!errors.Is(sizeErr, ErrIncompleteBatch) {
				t.Fatalf("unexpected BatchSize error: %v", sizeErr)
			}
			return
		}
		if size <= 0 || size > len(input) || size > maxEncodedBatchBytes {
			t.Fatalf("unsafe batch size %d for input %d", size, len(input))
		}
		_, decodeErr := DecodeBatch(input[:size])
		if decodeErr != nil &&
			!errors.Is(decodeErr, ErrCorruptBatch) &&
			!errors.Is(decodeErr, ErrCorruptRecord) &&
			!errors.Is(decodeErr, ErrChecksumMismatch) {
			t.Fatalf("unexpected DecodeBatch error: %v", decodeErr)
		}
	})
}
