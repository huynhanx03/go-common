# Forge MQ

A Kafka-inspired, embeddable message queue written in pure Go. Forge provides durable, append-only commit log storage with batching, compression, retention policies, and dead-letter queue support.

## Features

- **Append-only CommitLog** with rolling segments (`.log` + `.idx` files)
- **Producer batching** — configurable batch size (default 16KB) and linger time (default 5ms)
- **LZ4 compression** for record batches
- **Consumer groups** with persistent offset tracking
- **Dead-letter queue (DLQ)** for failed message routing
- **Retention policies** — time-based (default 7d) and size-based (default 1GB)
- **Segment compaction & merge** for storage optimization
- **Backpressure handling** with configurable pending buffer limits
- **CRC32C checksums** for data integrity
- **Sparse indexing** for fast offset lookups
- **Metrics hooks** for observability (OnFlush, OnPoll, OnDrop, OnBackpressure)
- **SpinLock-based concurrency** for sub-microsecond lock hold times

## Quick Start

```go
import "github.com/huynhanx03/judgify/pkg/mq/forge"

// Create a broker
broker, err := forge.NewBroker("/tmp/forge-data",
    forge.WithMaxSegmentBytes(256 << 20),
    forge.WithRetentionTime(24 * time.Hour),
)
defer broker.Close()

// Create or get a topic
topic, err := broker.CreateTopic("events")

// Produce messages
producer := broker.NewProducer(topic, forge.ProducerConfig{
    BatchSize:   16 * 1024,
    LingerTime:  5 * time.Millisecond,
    Compression: forge.CompressionLZ4,
})
defer producer.Close()

producer.Send(forge.Record{
    Key:   []byte("user-123"),
    Value: []byte(`{"action":"login"}`),
})

// Consume messages
consumer, err := broker.NewConsumer("my-group", "events")
batches, err := consumer.Poll(context.Background(), 100)
for _, batch := range batches {
    for _, record := range batch.Records {
        fmt.Printf("key=%s value=%s\n", record.Key, record.Value)
    }
}
consumer.Commit()
```

## Architecture

```
Broker
├── Topic ("events")
│   └── CommitLog
│       ├── Segment 0  →  000000000000.log + 000000000000.idx
│       ├── Segment 1  →  000000001024.log + 000000001024.idx
│       └── ...
├── OffsetStore
│   └── {group}/{topic}.offset
├── Producer  →  batching + linger + compression → CommitLog.Append()
└── Consumer  →  CommitLog.Read() + offset tracking + DLQ
```

## Configuration

| Option | Default | Description |
|--------|---------|-------------|
| `MaxSegmentBytes` | 256 MB | Max `.log` file size before rolling |
| `MaxSegmentAge` | 1 hour | Max segment age before rolling |
| `IndexInterval` | 4096 bytes | Bytes between sparse index entries |
| `MaxMessageSize` | 1 MB | Max single message size |
| `RetentionTime` | 7 days | Time-based retention |
| `RetentionBytes` | 1 GB | Size-based retention |
| `RetentionInterval` | 5 min | Background retention loop frequency |
| `FsyncEvery` | 0 (OS decides) | Fsync every N batches |
| `MinSegmentMergeAge` | 10 min | Min age for merge candidates |
| `MinMergeSegments` | 3 | Min sealed segments to trigger merge |

### Producer Config

| Field | Default | Description |
|-------|---------|-------------|
| `BatchSize` | 16 KB | Max bytes before auto-flush |
| `LingerTime` | 5 ms | Max wait before flushing partial batch |
| `Compression` | None | `CompressionNone` or `CompressionLZ4` |
| `MaxPendingBytes` | 0 (unlimited) | Backpressure limit |
| `OnError` | nil | Async error callback |
| `ShutdownTimeout` | 0 (block) | Max wait for flush on Close |

## Error Handling

| Error | Description |
|-------|-------------|
| `ErrCorruptBatch` | Batch header is malformed |
| `ErrCorruptRecord` | Record data is corrupted |
| `ErrChecksumMismatch` | CRC32C validation failed |
| `ErrOffsetNotFound` | Requested offset doesn't exist |
| `ErrClosed` | Operation on closed resource |
| `ErrMessageTooLarge` | Message exceeds `MaxMessageSize` |
| `ErrBackpressure` | Producer pending buffer is full |
| `ErrBatchTooLarge` | Batch exceeds 65535 records |

## Storage Layout

```
data-dir/
├── topics/
│   ├── events/
│   │   ├── 000000000000.log    # segment data
│   │   ├── 000000000000.idx    # sparse index
│   │   ├── 000000001024.log
│   │   └── 000000001024.idx
│   └── events.dlq/            # dead-letter queue topic
└── offsets/
    └── my-group/
        └── events.offset       # committed offset (uint64 big-endian)
```

## Testing

```bash
go test ./pkg/mq/forge/...           # unit + integration
go test ./pkg/mq/forge/... -run Stress  # stress tests
go test ./pkg/mq/forge/... -bench .     # benchmarks
```
