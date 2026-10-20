// Package stream provides bounded-memory helpers for processing io.Reader streams.
package stream

import (
	"context"
	"errors"
	"io"
)

const (
	defaultChunkSize = 256 << 10
	maxEmptyReads    = 100
)

var (
	// ErrInvalidArgument reports a nil context, reader, or visitor.
	ErrInvalidArgument = errors.New("stream: invalid argument")
	// ErrInvalidOptions reports invalid window sizes or a capacity overflow.
	ErrInvalidOptions = errors.New("stream: invalid window options")
)

// Window is a borrowed view of a stream region.
// Data is read-only and valid only for the duration of the ScanWindows visitor call.
type Window struct {
	// Data contains the current window, including retained overlap bytes.
	Data []byte
	// Offset is the absolute stream offset of Data[0].
	Offset int64
	// StartLimit is the first index that is not eligible as a match start
	// in a non-final window. It equals len(Data) in the final window.
	StartLimit int
	// Final reports that no more input bytes will be provided.
	Final bool
}

// WindowOptions configures chunked stream iteration.
type WindowOptions struct {
	// ChunkSize is the maximum number of new bytes read per iteration.
	// Zero selects a 256 KiB default. Negative values are invalid.
	ChunkSize int
	// Overlap is the number of trailing bytes retained for the next window.
	// Configure at least the longest match length to recognize across chunks.
	Overlap int
}

// ScanWindows visits bounded windows of r while retaining the configured
// overlap between windows. Non-final windows may repeat trailing bytes; a
// consumer should only accept match starts before Window.StartLimit. Every
// start is eligible in the final window.
// For example, a matcher for values up to 32 bytes should use Overlap: 32 and
// ignore candidate starts at or after StartLimit until Final is true.
//
// The iterator uses one reusable buffer with capacity ChunkSize+Overlap.
// A visitor must not mutate or retain Data, and must copy any bytes it needs
// after returning. A visitor error stops iteration and is returned unchanged.
// Ordinary io.EOF is successful;
// another reader error is returned after any bytes from that read have been
// delivered as a final window. If that final visit fails, its error takes
// precedence over the reader error.
//
// The context is checked between reads and visits. It cannot interrupt an
// arbitrary reader blocked in Read; use a context-aware reader or close the
// reader from the caller when prompt cancellation is required.
func ScanWindows(
	ctx context.Context,
	r io.Reader,
	opts WindowOptions,
	visit func(Window) error,
) error {
	if ctx == nil || r == nil || visit == nil {
		return ErrInvalidArgument
	}

	chunkSize := opts.ChunkSize
	if chunkSize == 0 {
		chunkSize = defaultChunkSize
	}
	if chunkSize < 0 || opts.Overlap < 0 {
		return ErrInvalidOptions
	}
	maxInt := int(^uint(0) >> 1)
	if chunkSize > maxInt-opts.Overlap {
		return ErrInvalidOptions
	}

	window := make([]byte, 0, chunkSize+opts.Overlap)
	var base int64
	emptyReads := 0

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		newBytes := 0
		var readErr error
		for newBytes < chunkSize {
			if err := ctx.Err(); err != nil {
				return err
			}

			readLimit := min(chunkSize-newBytes, cap(window)-len(window))
			oldLen := len(window)
			n, err := r.Read(window[oldLen : oldLen+readLimit])
			window = window[:oldLen+n]
			newBytes += n
			if n > 0 {
				emptyReads = 0
			} else if err == nil {
				emptyReads++
				if emptyReads >= maxEmptyReads {
					readErr = io.ErrNoProgress
					break
				}
			}
			if err != nil {
				readErr = err
				break
			}
		}

		if err := ctx.Err(); err != nil {
			return err
		}
		if readErr != nil {
			visitErr := visit(Window{
				Data:       window,
				Offset:     base,
				StartLimit: len(window),
				Final:      true,
			})
			if visitErr != nil {
				return visitErr
			}
			if readErr == io.EOF {
				return nil
			}
			return readErr
		}

		if len(window) <= opts.Overlap {
			continue
		}

		startLimit := len(window) - opts.Overlap
		if err := visit(Window{
			Data:       window,
			Offset:     base,
			StartLimit: startLimit,
		}); err != nil {
			return err
		}

		copy(window, window[startLimit:])
		window = window[:opts.Overlap]
		base += int64(startLimit)
	}
}
