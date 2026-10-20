package forge

import (
	"errors"
	"fmt"
)

// Sentinel errors for the Forge MQ.
var (
	ErrCorruptBatch         = errors.New("forge: corrupt record batch")
	ErrCorruptRecord        = errors.New("forge: corrupt record")
	ErrIncompleteBatch      = errors.New("forge: incomplete trailing record batch")
	ErrChecksumMismatch     = errors.New("forge: CRC32C checksum mismatch")
	ErrOffsetNotFound       = errors.New("forge: offset not found")
	ErrClosed               = errors.New("forge: resource is closed")
	ErrEmptyBatch           = errors.New("forge: batch contains no records")
	ErrMessageTooLarge      = errors.New("forge: message exceeds max size")
	ErrBackpressure         = errors.New("forge: producer backpressure, pending buffer full")
	ErrBatchTooLarge        = errors.New("forge: batch exceeds max 65535 records")
	ErrInvalidConfig        = errors.New("forge: invalid configuration")
	ErrInvalidAck           = errors.New("forge: invalid acknowledgment level")
	ErrConsumerBusy         = errors.New("forge: consumer group is already owned")
	ErrBrokerBusy           = errors.New("forge: data directory is already owned")
	ErrInvalidOffsetCommit  = errors.New("forge: invalid offset commit")
	ErrStorageFull          = errors.New("forge: durable storage limit reached")
	ErrStorageUnavailable   = errors.New("forge: storage is unavailable until restart")
	ErrResourceLimit        = errors.New("forge: configured resource limit reached")
	ErrDLQNotConfigured     = errors.New("forge: dead-letter queue is not configured")
	ErrInvalidSegmentLayout = errors.New("forge: invalid segment layout")
)

// Acknowledgment describes the point at which a producer send may return.
type Acknowledgment uint8

const (
	// AckMemory confirms only bounded in-memory admission.
	AckMemory Acknowledgment = iota
	// AckAppend confirms the batch bytes were appended to the local log.
	AckAppend
	// AckFsync confirms the appended bytes, index, and any newly created
	// segment directory entries were synchronized to disk.
	AckFsync
)

func (ack Acknowledgment) valid() bool {
	return ack == AckMemory || ack == AckAppend || ack == AckFsync
}

// AcknowledgmentError distinguishes a failed durability level from an append
// failure. Admitted means Forge owns a copy which may still flush after the
// call returns; Appended means bytes reached the log. Retrying is therefore
// ambiguous when either field is true.
type AcknowledgmentError struct {
	Level    Acknowledgment
	Admitted bool
	Appended bool
	Cause    error
}

func (err *AcknowledgmentError) Error() string {
	if err == nil {
		return "forge: acknowledgment failed"
	}
	if err.Appended {
		return fmt.Sprintf("forge: acknowledgment level %d failed after append", err.Level)
	}
	if err.Admitted {
		return fmt.Sprintf("forge: acknowledgment level %d failed after memory admission", err.Level)
	}
	return fmt.Sprintf("forge: acknowledgment level %d failed before append", err.Level)
}

func (err *AcknowledgmentError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}

// CorruptionError identifies a complete corrupt batch without rendering
// record bytes or filesystem paths.
type CorruptionError struct {
	SegmentBase uint64
	ByteOffset  int64
	Cause       error
}

func (err *CorruptionError) Error() string {
	if err == nil {
		return "forge: segment corruption"
	}
	return fmt.Sprintf(
		"forge: segment %d corruption at byte %d",
		err.SegmentBase,
		err.ByteOffset,
	)
}

func (err *CorruptionError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}
