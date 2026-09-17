package forge

import (
	"fmt"
	"os"
	"time"
)

// File system permissions.
const (
	dirPerm  = 0700 // single-owner directories may contain sensitive payloads
	filePerm = 0600 // data, index, offset, and lease files
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
	dlqSuffix               = ".dlq"
	dlqOriginalTopicKey     = "forge-original-topic"
	dlqOriginalOffsetKey    = "forge-original-offset"
	dlqOriginalTimestampKey = "forge-original-timestamp"
)

// Producer defaults.
const (
	defaultBatchBytes = 16 * 1024            // 16 KB — max accumulated bytes before auto-flush
	defaultLingerTime = 5 * time.Millisecond // max wait before flushing a partial batch
)

// Consumer read heuristics.
const (
	estimatedBytesPerRecord = 256      // used to size read buffers
	minReadBytes            = 4096     // minimum bytes to read in a single Poll
	maximumReadOwnedBytes   = 64 << 20 // hard decoded ownership bound per Read
	readRecordOverheadBytes = 96
	readHeaderOverheadBytes = 56
)

// Buffer pool defaults.
const (
	defaultRecordBufCap = 4096 // initial capacity for record encoding buffer pool
)

// Record codec limits.
const (
	maxRecordsPerBatch   = 65535 // RecordCount is uint16
	maxHeadersPerRecord  = 128
	maxHeadersPerBatch   = 65535
	maxEncodedBatchBytes = 16 << 20
	maxBatchPayloadBytes = maxEncodedBatchBytes - batchHeaderSize
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
	DefaultMaxSegmentBytes   = 256 << 20          // 256 MB
	DefaultMaxSegmentAge     = time.Hour          // 1 hour
	DefaultIndexInterval     = 4096               // bytes between index entries
	MaximumIndexInterval     = 1 << 20            // 1 MB maximum sparse-index scan window
	DefaultMaxMessageSize    = 1 << 20            // 1 MB
	DefaultRetentionTime     = 7 * 24 * time.Hour // 7 days
	DefaultRetentionBytes    = 1 << 30            // 1 GB
	DefaultRetentionInterval = 5 * time.Minute    // how often auto-retention runs
	DefaultMaxStorageBytes   = 2 << 30            // 2 GB hard safety limit per topic
	DefaultMaxTopics         = 64                 // broker-owned topic/file-set bound
	DefaultMaxProducers      = 256                // active broker-owned producer bound
	DefaultMaxConsumers      = 256                // active broker-owned consumer bound
	DefaultMaxConsumerGroups = 1024               // durable group-directory bound
	maxSegmentsPerTopic      = 128                // bounds open files and segment metadata
	maximumBrokerResources   = 65536
	maximumDirectoryEntries  = maximumBrokerResources*2 + 1
)

// RetentionMode determines whether consumer offsets protect segment data.
type RetentionMode uint8

const (
	// RetainUntilConsumed is the queue-safe default.
	RetainUntilConsumed RetentionMode = iota
	// RetainByAgeAndSize is destructive event-log retention.
	RetainByAgeAndSize
)

// Config holds all configuration for the commit log and segments.
type Config struct {
	MaxSegmentBytes   int64
	MaxSegmentAge     time.Duration
	IndexInterval     int
	MaxMessageSize    int
	RetentionTime     time.Duration
	RetentionBytes    int64
	RetentionInterval time.Duration // how often auto-retention runs
	FsyncEvery        int           // fsync every N batches, 0 = let OS decide
	OnRetentionError  func(error)   // optional callback for background retention errors
	MaxStorageBytes   int64
	RetentionMode     RetentionMode
	MaxTopics         int
	MaxProducers      int
	MaxConsumers      int
	MaxConsumerGroups int
	fileOps           fileOperations
}

// Option configures the commit log.
type Option func(*Config) error

func applyOption[T any](option func(*T) error, config *T) (resultErr error) {
	if option == nil || config == nil {
		return nil
	}
	defer func() {
		if recover() != nil {
			resultErr = fmt.Errorf("%w: option panic", ErrInvalidConfig)
		}
	}()
	return option(config)
}

func defaultConfig() Config {
	return Config{
		MaxSegmentBytes:   DefaultMaxSegmentBytes,
		MaxSegmentAge:     DefaultMaxSegmentAge,
		IndexInterval:     DefaultIndexInterval,
		MaxMessageSize:    DefaultMaxMessageSize,
		RetentionTime:     DefaultRetentionTime,
		RetentionBytes:    DefaultRetentionBytes,
		RetentionInterval: DefaultRetentionInterval,
		FsyncEvery:        0,
		MaxStorageBytes:   DefaultMaxStorageBytes,
		RetentionMode:     RetainUntilConsumed,
		MaxTopics:         DefaultMaxTopics,
		MaxProducers:      DefaultMaxProducers,
		MaxConsumers:      DefaultMaxConsumers,
		MaxConsumerGroups: DefaultMaxConsumerGroups,
		fileOps:           defaultFileOperations(),
	}
}

type fileOperations struct {
	write   func(*os.File, []byte) (int, error)
	sync    func(*os.File) error
	rename  func(string, string) error
	remove  func(string) error
	syncDir func(string) error
}

func defaultFileOperations() fileOperations {
	return fileOperations{
		write:  func(file *os.File, data []byte) (int, error) { return file.Write(data) },
		sync:   func(file *os.File) error { return file.Sync() },
		rename: os.Rename,
		remove: os.Remove,
		syncDir: func(path string) error {
			dir, err := os.Open(path)
			if err != nil {
				return err
			}
			defer dir.Close()
			return dir.Sync()
		},
	}
}

func (ops fileOperations) withDefaults() fileOperations {
	defaults := defaultFileOperations()
	if ops.write == nil {
		ops.write = defaults.write
	}
	if ops.sync == nil {
		ops.sync = defaults.sync
	}
	if ops.rename == nil {
		ops.rename = defaults.rename
	}
	if ops.remove == nil {
		ops.remove = defaults.remove
	}
	if ops.syncDir == nil {
		ops.syncDir = defaults.syncDir
	}
	return ops
}

// withFileOperations is intentionally package-private and exists for
// deterministic fault injection. Production callers cannot replace I/O.
func withFileOperations(ops fileOperations) Option {
	return func(config *Config) error {
		config.fileOps = ops.withDefaults()
		return nil
	}
}

func (config Config) validate() error {
	var indexEntries int64
	if config.IndexInterval > 0 {
		indexEntries = config.MaxStorageBytes / int64(config.IndexInterval)
		if config.MaxStorageBytes%int64(config.IndexInterval) != 0 {
			indexEntries++
		}
	}
	if config.MaxSegmentBytes <= 0 ||
		config.MaxSegmentBytes > int64(^uint(0)>>1) ||
		config.MaxSegmentAge <= 0 ||
		config.IndexInterval <= 0 ||
		config.IndexInterval > MaximumIndexInterval ||
		config.MaxMessageSize <= 0 ||
		config.MaxMessageSize > maxBatchPayloadBytes ||
		config.RetentionTime <= 0 ||
		config.RetentionBytes <= 0 ||
		config.RetentionInterval < 0 ||
		config.FsyncEvery < 0 ||
		config.MaxStorageBytes <= 0 ||
		config.MaxStorageBytes < config.MaxSegmentBytes ||
		indexEntries > maxIndexEntries ||
		config.MaxTopics <= 0 || config.MaxTopics > maximumBrokerResources ||
		config.MaxProducers <= 0 || config.MaxProducers > maximumBrokerResources ||
		config.MaxConsumers <= 0 || config.MaxConsumers > maximumBrokerResources ||
		config.MaxConsumerGroups <= 0 || config.MaxConsumerGroups > maximumBrokerResources ||
		(config.RetentionMode != RetainUntilConsumed &&
			config.RetentionMode != RetainByAgeAndSize) {
		return fmt.Errorf("%w: commit log", ErrInvalidConfig)
	}
	return nil
}

// WithMaxTopics sets the number of topic logs one Broker may own.
func WithMaxTopics(maximum int) Option {
	return func(config *Config) error {
		if maximum <= 0 || maximum > maximumBrokerResources {
			return invalidOption("maximum topics")
		}
		config.MaxTopics = maximum
		return nil
	}
}

// WithMaxProducers sets the number of active producers one Broker may own.
func WithMaxProducers(maximum int) Option {
	return func(config *Config) error {
		if maximum <= 0 || maximum > maximumBrokerResources {
			return invalidOption("maximum producers")
		}
		config.MaxProducers = maximum
		return nil
	}
}

// WithMaxConsumers sets the number of active consumers one Broker may own.
func WithMaxConsumers(maximum int) Option {
	return func(config *Config) error {
		if maximum <= 0 || maximum > maximumBrokerResources {
			return invalidOption("maximum consumers")
		}
		config.MaxConsumers = maximum
		return nil
	}
}

// WithMaxConsumerGroups sets the durable consumer-group directory bound.
func WithMaxConsumerGroups(maximum int) Option {
	return func(config *Config) error {
		if maximum <= 0 || maximum > maximumBrokerResources {
			return invalidOption("maximum consumer groups")
		}
		config.MaxConsumerGroups = maximum
		return nil
	}
}

// WithMaxStorageBytes sets the hard .log-byte limit for each topic.
func WithMaxStorageBytes(size int64) Option {
	return func(config *Config) error {
		if size <= 0 {
			return invalidOption("max storage bytes")
		}
		config.MaxStorageBytes = size
		return nil
	}
}

// WithRetentionMode selects queue-safe or destructive event-log retention.
func WithRetentionMode(mode RetentionMode) Option {
	return func(config *Config) error {
		if mode != RetainUntilConsumed && mode != RetainByAgeAndSize {
			return invalidOption("retention mode")
		}
		config.RetentionMode = mode
		return nil
	}
}

// WithMaxSegmentBytes sets the max .log file size before rolling.
func WithMaxSegmentBytes(n int64) Option {
	return func(config *Config) error {
		if n <= 0 {
			return invalidOption("max segment bytes")
		}
		config.MaxSegmentBytes = n
		return nil
	}
}

// WithMaxSegmentAge sets the max segment age before rolling.
func WithMaxSegmentAge(d time.Duration) Option {
	return func(config *Config) error {
		if d <= 0 {
			return invalidOption("max segment age")
		}
		config.MaxSegmentAge = d
		return nil
	}
}

// WithIndexInterval sets bytes between sparse index entries.
func WithIndexInterval(n int) Option {
	return func(config *Config) error {
		if n <= 0 || n > MaximumIndexInterval {
			return invalidOption("index interval")
		}
		config.IndexInterval = n
		return nil
	}
}

// WithMaxMessageSize sets the max single message size.
func WithMaxMessageSize(n int) Option {
	return func(config *Config) error {
		if n <= 0 || n > maxBatchPayloadBytes {
			return invalidOption("max message size")
		}
		config.MaxMessageSize = n
		return nil
	}
}

// WithFsyncEvery sets how often to fsync (0 = OS decides).
func WithFsyncEvery(n int) Option {
	return func(config *Config) error {
		if n < 0 {
			return invalidOption("fsync interval")
		}
		config.FsyncEvery = n
		return nil
	}
}

// WithRetentionTime sets time-based retention.
func WithRetentionTime(d time.Duration) Option {
	return func(config *Config) error {
		if d <= 0 {
			return invalidOption("retention time")
		}
		config.RetentionTime = d
		return nil
	}
}

// WithRetentionBytes sets size-based retention.
func WithRetentionBytes(n int64) Option {
	return func(config *Config) error {
		if n <= 0 {
			return invalidOption("retention bytes")
		}
		config.RetentionBytes = n
		return nil
	}
}

// WithRetentionInterval sets how often auto-retention runs.
func WithRetentionInterval(d time.Duration) Option {
	return func(config *Config) error {
		if d <= 0 {
			return invalidOption("retention interval")
		}
		config.RetentionInterval = d
		return nil
	}
}

// WithoutAutomaticRetention disables the background loop. Callers then own
// explicit retention scheduling and shutdown ordering.
func WithoutAutomaticRetention() Option {
	return func(config *Config) error {
		config.RetentionInterval = 0
		return nil
	}
}

// WithOnRetentionError sets a callback for background retention errors.
func WithOnRetentionError(fn func(error)) Option {
	return func(config *Config) error {
		config.OnRetentionError = fn
		return nil
	}
}

func invalidOption(name string) error {
	return fmt.Errorf("%w: %s", ErrInvalidConfig, name)
}
