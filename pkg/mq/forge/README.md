# Forge

Forge is an embedded, durable queue for a **single node and one owner process**.
It provides a bounded local delivery log without requiring Kafka, Redis, or a
separate broker service.

Use Forge for local message processing, transactional-outbox relays,
projections, and other at-least-once workloads. It is not a distributed broker
and does not claim exactly-once delivery.

## Production contract

- One `Broker` owns one data directory. A second process receives
  `ErrBrokerBusy`.
- One active `Consumer` owns each `(group, topic)` pair. A duplicate receives
  `ErrConsumerBusy`.
- `SendContext(..., AckFsync)` returns only after the record, index, and any
  newly created segment directory entries are synchronized to disk.
- `Fetch` advances an in-memory cursor only. `CommitOffset` is the sole durable
  acknowledgment.
- A crash before `CommitOffset` causes redelivery after restart.
- Delivery is at least once. Handlers must be idempotent.
- Buffers, messages, batches, headers, storage, fetch sizes, topics, active
  producers/consumers, and durable consumer groups are bounded.
- Segment count and sparse-index memory are hard-bounded per topic; unsafe
  storage/index configurations fail construction.
- The default `RetainUntilConsumed` mode never deletes data required by a
  registered consumer group.
- `Broker.Close` stops maintenance, drains owned producers, closes consumers and
  logs, and releases the process lease.

Forge uses ordinary blocking mutexes around filesystem work. Do not open the
same data directory through multiple `Broker` values, even inside one process.
Share one broker and create producers/consumers from it.

The stable v1 scope intentionally excludes log compaction and segment merging.
Those operations require a durable operation manifest and a larger crash matrix;
whole-segment retention is sufficient for queue workloads.

## Recommended transactional-outbox flow

The database remains the source of truth:

1. Commit the domain mutation and outbox row in one database transaction.
2. Relay the outbox row to Forge with `AckFsync`.
3. Mark the outbox row published only after the fsync acknowledgment.
4. A consumer fetches a delivery and performs its idempotent durable side effect.
5. Commit `delivery.Offset + 1` only after that database transaction commits.

A timeout or `AcknowledgmentError` with `Admitted == true` or
`Appended == true` is ambiguous: the record may still flush or may already be
durable, so never blindly create a new logical event. Reuse an event ID and
deduplicate at the consumer/database boundary.

## Quick start

```go
broker, err := forge.NewBroker("./data/forge",
	forge.WithMaxStorageBytes(2<<30),
)
if err != nil {
	return err
}
defer broker.Close()

producer, err := broker.NewProducer(
	"events",
	forge.WithBatchSize(64<<10),
	forge.WithLinger(5*time.Millisecond),
	forge.WithCompression(forge.CompressionLZ4),
)
if err != nil {
	return err
}

err = producer.SendContext(
	ctx,
	[]byte(eventID),
	payload,
	[]forge.Header{{Key: []byte("cid"), Value: []byte(cid)}},
	forge.AckFsync,
)
if err != nil {
	return err
}

consumer, err := broker.NewConsumer("projector", "events")
if err != nil {
	return err
}
defer consumer.Close()

for {
	deliveries, err := consumer.FetchWait(
		ctx,
		32,
		forge.DefaultFetchWaitInterval,
	)
	if err != nil {
		return err
	}
	for _, delivery := range deliveries {
		if err := handleIdempotently(ctx, delivery); err != nil {
			_ = consumer.Rollback()
			return err
		}
		if err := consumer.CommitOffset(ctx, delivery.Offset+1); err != nil {
			return err
		}
	}
}
```

`FetchWait` uses an in-process append notification and a polling fallback. The
fallback also detects writes made through a directly constructed commit log.
Cancellation and `Consumer.Close` wake an outstanding wait immediately.
Every returned delivery owns its key, value, and header bytes; retention,
segment unmapping, and broker shutdown cannot invalidate them.

## Acknowledgment levels

| Level | Guarantee at successful return | Intended use |
|---|---|---|
| `AckMemory` | Deep-copied into the bounded producer buffer | Rebuildable telemetry |
| `AckAppend` | Appended to the local log | Throughput-oriented local events |
| `AckFsync` | Log, index, and new directory entries synchronized to disk | Durable message relay |

`AckMemory` failures after return are reported through `WithOnError`; keep that
callback non-blocking. Forge contains callback panics and invokes metrics/error
callbacks outside data-path mutexes. Callbacks should export metrics or logs,
must not perform durable side effects, and must not call lifecycle methods such
as `Close` from the callback goroutine.

## Consumer semantics

- `Fetch(ctx, n)` returns copied `Delivery` values with absolute offsets.
- `CommitOffset(ctx, next)` is monotonic and cannot pass the fetched cursor.
- Partial commits are supported.
- `Rollback()` resets the cursor to the last durable commit.
- `Replay(offset)` changes only the in-memory cursor.
- The deprecated `Seek(offset)` alias is range-checked exactly like `Replay`.
- `DeleteConsumerGroup` removes an inactive group's progress and allows retention
  to reclaim data it pinned.
- `NackContext` writes to a configured DLQ with `AckFsync`. Prefer a retry policy before
  DLQ for transient failures.

The old `Poll`, `Commit`, and `Seek` methods are compatibility shims. New code
should use explicit deliveries and offsets.

## Retention and capacity

`RetainUntilConsumed` is the default queue mode. Sealed segments are eligible for
deletion only when every registered group has committed beyond the segment. A
new consumer registers at offset zero and therefore pins existing data.

`MaxStorageBytes` is a hard per-topic limit over `.log` bytes. It defaults to
2 GiB. When it is reached, append returns `ErrStorageFull`; unconsumed records are
not deleted. Index, lock, and filesystem allocation overhead
are not included in this logical limit, so reserve additional disk headroom.
The segment-file cap can also return `ErrStorageFull` before the byte limit when
segments are configured unrealistically small. Reclaim fully consumed segments
or increase the segment size instead of creating unbounded open files.

`IndexInterval` and `MaxStorageBytes` are validated together against a hard
index-memory ceiling, and `IndexInterval` itself cannot exceed 1 MiB. Index
files are disposable accelerators: every startup scans bounded batches from the
authoritative log, rejects complete corruption, truncates only an incomplete
crash tail, and rebuilds the sparse index. A missing, stale, or partially
written index can therefore never hide valid log records or block recovery.
Reads scan from the nearest verified sparse entry without charging pre-offset
scan bytes to the caller's return budget, return only complete atomic batches,
and cap both encoded bytes and decoded caller-owned memory.

`MaxPendingBytes` bounds pending plus in-flight encoded record data and defaults
to the largest safe record-batch payload (just under 16 MiB). Forge also caps
record and header counts before admission, so concurrent senders cannot create
an aggregate batch the codec would reject. Capacity overflow returns
`ErrBackpressure` before ownership transfers.

`MaxMessageSize` is capped at that same encodable payload ceiling rather than
the complete batch ceiling, which reserves the fixed batch header. Individual
record framing and headers still count toward the configured message size.

`MaxTopics`, `MaxProducers`, `MaxConsumers`, and `MaxConsumerGroups` bound one
broker's file sets, active child resources, and durable offset directories.
Their defaults are conservative for a single-process embedded broker and can
be reduced with the corresponding options. Exceeding one returns
`ErrResourceLimit`; limits cannot be disabled with zero or configured above the
library hard ceiling.

`RetainByAgeAndSize` is destructive event-log retention and must be selected
explicitly. It can delete records a slow consumer has not processed.

## Failure handling

Use `errors.Is` for sentinels such as `ErrBackpressure`, `ErrStorageFull`,
`ErrStorageUnavailable`, `ErrResourceLimit`, `ErrBrokerBusy`,
`ErrConsumerBusy`, `ErrClosed`, and corruption errors. Use `errors.As` for
`*AcknowledgmentError` and `*CorruptionError`.

Backpressure means the caller should stop admitting messages and retry with
bounded jitter while preserving the same logical message ID. Corruption is terminal for
the affected log and requires operator action; Forge never silently skips a
complete corrupt batch. Only an incomplete crash tail is truncated during
recovery.

If a partial append cannot be rolled back, whole-segment deletion fails after a
segment file has been closed, or the post-retention directory fsync fails, the
affected commit log fails closed with `ErrStorageUnavailable`:
health/read/append operations return the same terminal storage error until the
broker is restarted. This prevents writes on an ambiguous tail and keeps
orphaned log files inside recovery/capacity accounting. Restart truncates an
incomplete tail and rebuilds any missing index from the authoritative log.

## Health and observability

- `Broker.Check(ctx)` verifies broker ownership/open logs.
- `Broker.Stats(ctx)` reports deterministic topic offsets, segment count, and
  logical log bytes.
- `Broker.ConsumerLag(ctx, group, topic)` reports durable group lag.
- `MetricsHook` exposes bounded flush, fetch, drop, and backpressure callbacks.

Attach correlation and idempotency metadata through record headers. Forge is
domain-neutral and does not prescribe header names, payload schemas, or logger types.

## Low-level ownership

`NewCommitLog` and the package-level `NewProducer` constructor expose the
storage engine for advanced embedding and tests. They do not acquire the
`Broker` process lease. A caller using either API must provide exclusive
ownership and close resources in dependency order. Applications should prefer
`NewBroker` plus `Broker.NewProducer` and `Broker.NewConsumer`.
`CommitLog.Read` returns caller-owned record bytes; mutating them never changes
persisted data, and no returned slice aliases a sealed segment's mmap. The
method accepts at most 16 MiB of encoded batches per call and also enforces a
64 MiB decoded-ownership ceiling. `CommitLog.Append` enforces the configured
per-message limit in addition to the codec's hard batch limit.

## Storage layout

```text
data/
├── .broker.lock
├── topics/
│   └── events/
│       ├── 00000000000000000000.log
│       └── 00000000000000000000.idx
└── offsets/
    └── projector/
        ├── events.lock
        └── events.offset
```

Offset writes use a temporary file, fsync, atomic rename, and directory fsync.

## Verification

```bash
go test ./pkg/mq/forge -count=100
go test -race ./pkg/mq/forge -count=20
go test ./pkg/mq/forge -fuzz=FuzzRecovery -fuzztime=30s
```

The test suite includes short writes, fsync/rename failures, corrupt and
incomplete batches, bounded allocation fuzzing, abrupt producer exit, abrupt
uncommitted consumer exit, ownership conflicts, retention pinning, and concurrent
producer ordering.
