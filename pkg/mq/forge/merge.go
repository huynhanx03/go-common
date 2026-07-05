package forge

import (
	"fmt"
	"time"
)

// MergeSegments combines multiple small sealed segments into a single larger segment.
// Only merges segments that are older than MinSegmentMergeAge and when at least
// MinMergeSegments candidates exist. The active segment is never merged.
func (cl *CommitLog) MergeSegments() error {
	cl.mu.Lock()
	defer cl.mu.Unlock()

	if cl.closed {
		return ErrClosed
	}

	candidates := cl.mergeCandidates()
	if len(candidates) < cl.config.MinMergeSegments {
		return nil
	}

	// Zero-copy merge: read raw bytes from each segment (no decode/re-encode).
	// Deep-copy raw data since mmap'd memory becomes invalid after segment removal.
	var rawChunks [][]byte
	hasData := false
	for _, seg := range candidates {
		raw, err := seg.readRawBytes()
		if err != nil {
			return fmt.Errorf("forge: merge read segment %d: %w", seg.baseOffset, err)
		}
		rawChunks = append(rawChunks, raw)
		if len(raw) > 0 {
			hasData = true
		}
	}

	if !hasData {
		return cl.removeSegments(candidates)
	}

	// Crash-safe merge via staging directory.
	newBaseOffset := candidates[0].baseOffset
	return cl.stageAndSwapSegment(candidates, newBaseOffset, func(seg *segment) error {
		// Write raw batch bytes directly, parsing only headers for index updates.
		for _, raw := range rawChunks {
			off := 0
			for off < len(raw) {
				batchSize, err := BatchSize(raw[off:])
				if err != nil || off+batchSize > len(raw) {
					break
				}

				batchData := raw[off : off+batchSize]
				baseOffset, recordCount, err := ParseBatchHeader(batchData)
				if err != nil {
					return fmt.Errorf("forge: merge parse header: %w", err)
				}

				// Minimal RecordBatch for segment.Append index/offset tracking.
				hdr := &RecordBatch{
					BaseOffset:  baseOffset,
					RecordCount: recordCount,
				}
				if err := seg.Append(batchData, hdr); err != nil {
					return fmt.Errorf("forge: merge write: %w", err)
				}
				off += batchSize
			}
		}
		return nil
	})
}

// mergeCandidates returns sealed segments older than MinSegmentMergeAge.
func (cl *CommitLog) mergeCandidates() []*segment {
	cutoff := time.Now().UnixNano() - int64(cl.config.MinSegmentMergeAge)
	var candidates []*segment
	for _, seg := range cl.segments {
		if seg == cl.activeSegment {
			continue
		}
		if seg.created <= cutoff {
			candidates = append(candidates, seg)
		}
	}
	return candidates
}
