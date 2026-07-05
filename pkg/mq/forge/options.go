package forge

import "time"

// File system permissions.
const (
	dirPerm  = 0755 // directories
	filePerm = 0644 // data files (.log, .idx, .offset)
)

// File extensions used across the package.
const (
	extLog    = ".log"
	extIndex  = ".idx"
	extOffset = ".offset"
	extTmp    = ".tmp"
)

// Well-known naming conventions.
const (
	dlqSuffix           = ".dlq"
	dlqOriginalTopicKey = "forge-original-topic"
)

// Producer defaults.
const (
	defaultBatchBytes          = 16 * 1024        // 16 KB — max accumulated bytes before auto-flush
	defaultLingerTime          = 5 * time.Millisecond // max wait before flushing a partial batch
	estimatedRecordOverhead    = 16                // estimated header overhead per record in bufSize calc
)

// Consumer read heuristics.
const (
	estimatedBytesPerRecord = 256  // used to size read buffers
	minReadBytes            = 4096 // minimum bytes to read in a single Poll
)

// Buffer pool defaults.
const (
	defaultRecordBufCap = 4096 // initial capacity for record encoding buffer pool
)

// Record codec limits.
const (
	maxRecordsPerBatch = 65535 // RecordCount is uint16
	maxHeadersPerRecord = 10000 // cap to prevent OOM from corrupt data
)

// Offset store.
const (
	offsetByteSize = 8 // uint64 stored as big-endian bytes
)

// Decompressor tuning.
const (
	decompressInitMultiplier = 4 // initial dst = src * this
	decompressGrowthFactor   = 2 // double buffer on retry
)

// Storage defaults.
const (
	DefaultMaxSegmentBytes    = 256 << 20              // 256 MB
	DefaultMaxSegmentAge      = time.Hour              // 1 hour
	DefaultIndexInterval      = 4096                   // bytes between index entries
	DefaultMaxMessageSize     = 1 << 20                // 1 MB
	DefaultRetentionTime      = 7 * 24 * time.Hour     // 7 days
	DefaultRetentionBytes     = 1 << 30                // 1 GB
	DefaultRetentionInterval  = 5 * time.Minute         // how often auto-retention runs
	DefaultMinSegmentMergeAge = 10 * time.Minute        // segments older than this are merge candidates
	DefaultMinMergeSegments   = 3                       // minimum sealed segments to trigger merge
)

// Config holds all configuration for the commit log and segments.
type Config struct {
	MaxSegmentBytes    int64
	MaxSegmentAge      time.Duration
	IndexInterval      int
	MaxMessageSize     int
	RetentionTime      time.Duration
	RetentionBytes     int64
	RetentionInterval  time.Duration    // how often auto-retention runs
	FsyncEvery         int              // fsync every N batches, 0 = let OS decide
	MinSegmentMergeAge time.Duration    // sealed segments older than this are merge candidates
	MinMergeSegments   int              // minimum sealed segments to trigger merge
	OnRetentionError   func(error)      // optional callback for retention/merge errors
}

// Option configures the commit log.
type Option func(*Config)

func defaultConfig() Config {
	return Config{
		MaxSegmentBytes:    DefaultMaxSegmentBytes,
		MaxSegmentAge:      DefaultMaxSegmentAge,
		IndexInterval:      DefaultIndexInterval,
		MaxMessageSize:     DefaultMaxMessageSize,
		RetentionTime:      DefaultRetentionTime,
		RetentionBytes:     DefaultRetentionBytes,
		RetentionInterval:  DefaultRetentionInterval,
		FsyncEvery:         0,
		MinSegmentMergeAge: DefaultMinSegmentMergeAge,
		MinMergeSegments:   DefaultMinMergeSegments,
	}
}

// WithMaxSegmentBytes sets the max .log file size before rolling.
func WithMaxSegmentBytes(n int64) Option {
	return func(c *Config) {
		if n > 0 {
			c.MaxSegmentBytes = n
		}
	}
}

// WithMaxSegmentAge sets the max segment age before rolling.
func WithMaxSegmentAge(d time.Duration) Option {
	return func(c *Config) {
		if d > 0 {
			c.MaxSegmentAge = d
		}
	}
}

// WithIndexInterval sets bytes between sparse index entries.
func WithIndexInterval(n int) Option {
	return func(c *Config) {
		if n > 0 {
			c.IndexInterval = n
		}
	}
}

// WithMaxMessageSize sets the max single message size.
func WithMaxMessageSize(n int) Option {
	return func(c *Config) {
		if n > 0 {
			c.MaxMessageSize = n
		}
	}
}

// WithFsyncEvery sets how often to fsync (0 = OS decides).
func WithFsyncEvery(n int) Option {
	return func(c *Config) { c.FsyncEvery = n }
}

// WithRetentionTime sets time-based retention.
func WithRetentionTime(d time.Duration) Option {
	return func(c *Config) {
		if d > 0 {
			c.RetentionTime = d
		}
	}
}

// WithRetentionBytes sets size-based retention.
func WithRetentionBytes(n int64) Option {
	return func(c *Config) {
		if n > 0 {
			c.RetentionBytes = n
		}
	}
}

// WithRetentionInterval sets how often auto-retention runs.
func WithRetentionInterval(d time.Duration) Option {
	return func(c *Config) {
		if d > 0 {
			c.RetentionInterval = d
		}
	}
}

// WithMinSegmentMergeAge sets the minimum age for segments to be merge candidates.
// Use 0 to allow merging all sealed segments regardless of age.
func WithMinSegmentMergeAge(d time.Duration) Option {
	return func(c *Config) {
		if d >= 0 {
			c.MinSegmentMergeAge = d
		}
	}
}

// WithMinMergeSegments sets the minimum number of sealed segments to trigger merge.
func WithMinMergeSegments(n int) Option {
	return func(c *Config) {
		if n > 1 {
			c.MinMergeSegments = n
		}
	}
}

// WithOnRetentionError sets a callback for errors during background retention/merge.
func WithOnRetentionError(fn func(error)) Option {
	return func(c *Config) { c.OnRetentionError = fn }
}
