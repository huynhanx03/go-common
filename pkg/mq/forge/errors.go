package forge

import "errors"

// Sentinel errors for the Forge MQ.
var (
	ErrCorruptBatch     = errors.New("forge: corrupt record batch")
	ErrCorruptRecord    = errors.New("forge: corrupt record")
	ErrChecksumMismatch = errors.New("forge: CRC32C checksum mismatch")
	ErrOffsetNotFound   = errors.New("forge: offset not found")
	ErrClosed           = errors.New("forge: resource is closed")
	ErrEmptyBatch       = errors.New("forge: batch contains no records")
	ErrMessageTooLarge  = errors.New("forge: message exceeds max size")
	ErrBackpressure     = errors.New("forge: producer backpressure, pending buffer full")
	ErrBatchTooLarge    = errors.New("forge: batch exceeds max 65535 records")
)
