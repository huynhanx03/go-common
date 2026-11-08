# Consumer

One processing runtime for Forge, Kafka, and local message sources. Options
control workers, bounded admission, ordering, priority, and batching.

```go
processor, err := consumer.New(handle,
	consumer.WithWorkers(8),
	consumer.WithQueueCapacity(256),
	consumer.WithFullQueue(consumer.Block),
)
if err != nil {
	return err
}
defer processor.Close(shutdownContext)
```

`Process(ctx, messages)` returns one `Result` per input, in input order:
`Success`, `Retry`, or `DeadLetter`. Adapters own acknowledgment and durable DLQ.
Panic, invalid outcomes, cancellation, or `Success` with an error become `Retry`.

## Ordering and priority

Messages with the same `Stream` run sequentially by default; an empty stream
uses one fallback stream. Independent streams can use different workers.
One source partition should have only one outstanding `Process` call.
A retry prevents later messages in that call's ordered stream from executing.

Choose finer ordering when the domain permits it:

```go
consumer.WithOrdering(func(m consumer.Message) string {
	return m.Stream + ":" + string(m.Key)
})
consumer.WithPriority(func(m consumer.Message) int { return priority(m) })
consumer.WithPriorityBurst(16)
```

Higher priority runs first among ready messages; the oldest ready message gets
a turn after the configured number of priority bypasses. FIFO within each
ordering key remains intact. `WithUnordered()` allows independent messages to
run in any order. Priority operates on admitted work, within bounded source
windows; it cannot prioritize an unread broker backlog.

## Batch

```go
processor, err := consumer.NewBatch(handleBatch,
	consumer.WithWorkers(4),
	consumer.WithQueueCapacity(512),
	consumer.WithUnordered(),
	consumer.WithBatch(100, 10*time.Millisecond),
)
```

`handleBatch(ctx, messages)` returns a result for each message. It transfers
ownership of the result slice to the runtime. Incorrect result counts retry the
whole batch. Provider limits and splitting one job into multiple tokens belong
to the service.

Each batch uses one `Process` call's context and contains at most one message
per ordered key. The worker waits up to the batch delay for further admissions,
then flushes a partial batch. It also flushes when admission ends or a full
queue needs the current batch to finish before admission can progress.
Kafka also uses this delay when collecting its source window. Forge processes
records currently available in its log. `Collect[T]` provides the same bounded
channel gathering for other sources; channel close flushes the final window.

Single-message handlers automatically restore request IDs from headers. Inside
a batch, use `MessageContext(ctx, message)` for logs about a particular message.

## Capacity and lifecycle

Defaults: one worker, 256 queued messages, blocking admission, stream ordering.
Batch defaults: 128 messages, 5 ms gathering delay. `Reject` returns `ErrQueueFull`
instead of waiting. Blocked admissions respect cancellation and shutdown.
`Stats()` exposes queued and running message counts.

Each runtime owns fixed workers and its own queue. Create separate instances
to isolate providers or workloads. At most queue capacity plus workers × batch
size messages execute or wait inside the runtime; source/client buffers are
separate. `Process` retains result bookkeeping proportional to its input count.
The runtime copies retained payloads and headers.

Stop source adapters, then `Close(ctx)` to reject new work and drain accepted
work. A close deadline cancels handler contexts. A handler that ignores
cancellation must still return before its worker can exit.

Forge: `source.Run(ctx, processor)` blocks until cancellation or retry. Kafka:
`group.StartWithRuntime(ctx, topics, processor, onError, deadLetter)` starts
consumption. Both leave the supplied runtime under caller ownership.

Only a completed contiguous source prefix is acknowledged. Effects from later
completed messages can be replayed after an earlier failure. Processing remains
at least once; use the application's inbox/idempotency transaction as needed.
