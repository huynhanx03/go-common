package forge

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// keyRecord pairs a record with its absolute offset for compaction sorting.
type keyRecord struct {
	offset uint64
	rec    Record
}

// Compact performs log compaction: scans all sealed segments, keeps the last
// record for each key, and rewrites them into new segments. Keyless records
// (nil/empty key) are always retained. The active segment is never compacted.
//
// This is similar to Kafka's log compaction — useful for CDC/changelog topics
// where only the latest state per key matters.
func (cl *CommitLog) Compact() error {
	cl.mu.Lock()
	defer cl.mu.Unlock()

	if cl.closed {
		return ErrClosed
	}

	sealed := cl.sealedSegments()
	if len(sealed) == 0 {
		return nil
	}

	// Phase 1: scan all sealed segments, build key→latest record map.
	latest := make(map[string]keyRecord)
	var keylessRecords []keyRecord

	for _, seg := range sealed {
		batches, err := cl.readAllBatches(seg)
		if err != nil {
			return fmt.Errorf("forge: compact read segment %d: %w", seg.baseOffset, err)
		}
		for _, batch := range batches {
			for _, rec := range batch.Records {
				absOffset := batch.BaseOffset + uint64(rec.OffsetDelta)
				// Deep-copy: record byte slices may reference mmap'd memory
				// that becomes invalid after segment removal.
				rec = copyRecord(rec)
				if len(rec.Key) == 0 {
					keylessRecords = append(keylessRecords, keyRecord{offset: absOffset, rec: rec})
				} else {
					latest[string(rec.Key)] = keyRecord{offset: absOffset, rec: rec}
				}
			}
		}
	}

	// Phase 2: collect all surviving records, sorted by original offset.
	survivors := make([]keyRecord, 0, len(latest)+len(keylessRecords))
	for _, kr := range latest {
		survivors = append(survivors, kr)
	}
	survivors = append(survivors, keylessRecords...)
	sortKeyRecords(survivors)

	if len(survivors) == 0 {
		// All segments were empty — just remove them.
		return cl.removeSegments(sealed)
	}

	// Phase 3+4: crash-safe write via staging directory.
	newBaseOffset := sealed[0].baseOffset
	return cl.stageAndSwapSegment(sealed, newBaseOffset, func(seg *segment) error {
		return cl.writeBatches(seg, survivors, newBaseOffset)
	})
}

// sealedSegments returns all segments except the active one.
func (cl *CommitLog) sealedSegments() []*segment {
	if len(cl.segments) <= 1 {
		return nil
	}
	sealed := make([]*segment, 0, len(cl.segments)-1)
	for _, seg := range cl.segments {
		if seg != cl.activeSegment {
			sealed = append(sealed, seg)
		}
	}
	return sealed
}

// readAllBatches reads every batch from a segment.
func (cl *CommitLog) readAllBatches(seg *segment) ([]*RecordBatch, error) {
	return seg.ReadFrom(seg.baseOffset, int(seg.size))
}

// writeBatches writes records into a segment in batches of maxRecordsPerBatch.
func (cl *CommitLog) writeBatches(seg *segment, records []keyRecord, baseOffset uint64) error {
	const batchLimit = 1000 // records per batch for compacted output

	for i := 0; i < len(records); i += batchLimit {
		end := i + batchLimit
		if end > len(records) {
			end = len(records)
		}

		batch := &RecordBatch{
			BaseOffset:  baseOffset + uint64(i),
			RecordCount: uint16(end - i),
		}
		batch.Records = make([]Record, end-i)
		for j, kr := range records[i:end] {
			batch.Records[j] = kr.rec
			batch.Records[j].OffsetDelta = int64(j)
		}

		dstPtr := encodeDstPool.Get().(*[]byte)
		encoded, err := EncodeBatch(batch, (*dstPtr)[:0])
		if err != nil {
			*dstPtr = (*dstPtr)[:0]
			encodeDstPool.Put(dstPtr)
			return err
		}
		if err := seg.Append(encoded, batch); err != nil {
			*dstPtr = encoded[:0]
			encodeDstPool.Put(dstPtr)
			return err
		}
		*dstPtr = encoded[:0]
		encodeDstPool.Put(dstPtr)
	}
	return nil
}

// removeSegments removes segments from disk and from cl.segments slice.
func (cl *CommitLog) removeSegments(toRemove []*segment) error {
	removeSet := make(map[*segment]struct{}, len(toRemove))
	for _, s := range toRemove {
		removeSet[s] = struct{}{}
	}

	for _, s := range toRemove {
		if err := s.Remove(); err != nil {
			return fmt.Errorf("forge: compact remove segment %d: %w", s.baseOffset, err)
		}
	}

	keep := make([]*segment, 0, len(cl.segments))
	for _, s := range cl.segments {
		if _, ok := removeSet[s]; !ok {
			keep = append(keep, s)
		}
	}
	cl.segments = keep
	return nil
}

// insertSegmentSorted inserts a segment into cl.segments maintaining baseOffset order.
func (cl *CommitLog) insertSegmentSorted(seg *segment) {
	pos := 0
	for pos < len(cl.segments) && cl.segments[pos].baseOffset < seg.baseOffset {
		pos++
	}
	cl.segments = append(cl.segments, nil)
	copy(cl.segments[pos+1:], cl.segments[pos:])
	cl.segments[pos] = seg
}

// copyRecord deep-copies a record's byte slices so they don't reference mmap'd memory.
func copyRecord(r Record) Record {
	r.Key = copyBytes(r.Key)
	r.Value = copyBytes(r.Value)
	if len(r.Headers) > 0 {
		headers := make([]Header, len(r.Headers))
		for i, h := range r.Headers {
			headers[i] = Header{Key: copyBytes(h.Key), Value: copyBytes(h.Value)}
		}
		r.Headers = headers
	}
	return r
}

// copyBytes returns an independent copy of b, or nil if b is nil.
func copyBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	c := make([]byte, len(b))
	copy(c, b)
	return c
}

// copyBatches deep-copies all batches' records to detach from mmap'd memory.
func copyBatches(batches []*RecordBatch) []*RecordBatch {
	for _, batch := range batches {
		for i := range batch.Records {
			batch.Records[i] = copyRecord(batch.Records[i])
		}
	}
	return batches
}

// stageAndSwapSegment writes a new segment crash-safely:
//  1. Write to .staging/ subdirectory
//  2. Sync and close
//  3. Remove old segments (frees filenames)
//  4. Rename staged files to final location
//  5. Reopen and insert into segment list
//
// On crash between steps 3-4, recoverStaging() restores files on next startup.
func (cl *CommitLog) stageAndSwapSegment(toRemove []*segment, newBaseOffset uint64, writeFn func(seg *segment) error) error {
	stageDir := filepath.Join(cl.dir, ".staging")
	if err := os.MkdirAll(stageDir, dirPerm); err != nil {
		return fmt.Errorf("forge: staging mkdir: %w", err)
	}

	stageSeg, err := openSegment(stageDir, newBaseOffset, cl.config)
	if err != nil {
		os.RemoveAll(stageDir)
		return err
	}

	if err := writeFn(stageSeg); err != nil {
		stageSeg.Close()
		os.RemoveAll(stageDir)
		return err
	}

	if err := stageSeg.Close(); err != nil {
		os.RemoveAll(stageDir)
		return err
	}

	// Point of no return: delete old segments, then move staged files.
	if err := cl.removeSegments(toRemove); err != nil {
		return err
	}

	logName := fmt.Sprintf(segmentNameFmt+extLog, newBaseOffset)
	idxName := fmt.Sprintf(segmentNameFmt+extIndex, newBaseOffset)

	if err := os.Rename(filepath.Join(stageDir, logName), filepath.Join(cl.dir, logName)); err != nil {
		return fmt.Errorf("forge: rename staged log: %w", err)
	}
	if err := os.Rename(filepath.Join(stageDir, idxName), filepath.Join(cl.dir, idxName)); err != nil {
		return fmt.Errorf("forge: rename staged idx: %w", err)
	}

	os.RemoveAll(stageDir)

	// Reopen the final segment.
	finalSeg, err := openSegment(cl.dir, newBaseOffset, cl.config)
	if err != nil {
		return err
	}
	finalSeg.seal()
	cl.insertSegmentSorted(finalSeg)

	return nil
}

// recoverStaging moves any staged segment files from .staging/ to the main dir.
// Called during CommitLog initialization to handle incomplete compact/merge operations.
func recoverStaging(dir string) {
	stageDir := filepath.Join(dir, ".staging")
	entries, err := os.ReadDir(stageDir)
	if err != nil {
		return // no staging dir — nothing to recover
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		src := filepath.Join(stageDir, e.Name())
		dst := filepath.Join(dir, e.Name())
		os.Rename(src, dst)
	}
	os.RemoveAll(stageDir)
}

// sortKeyRecords sorts by offset ascending using O(n log n) stdlib sort.
func sortKeyRecords(records []keyRecord) {
	sort.Slice(records, func(i, j int) bool {
		return records[i].offset < records[j].offset
	})
}
