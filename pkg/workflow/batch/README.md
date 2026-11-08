# Batch checkpoints

`pkg/workflow/batch` runs bounded, serial segments over an application-owned
transactional `Store`. Each key has an immutable input fingerprint, cursor
schema, and boundary. The Store owns the database transaction that applies one
page and advances its checkpoint together with the application's writes.

```go
runner, err := batch.New(store, log, batch.Config{})
if err != nil {
	return err
}

result, err := runner.Run(ctx, batch.Definition{
	Key:              "expire-credit/2026-11-07/partition-03",
	InputFingerprint: "cutoff=2026-11-07T00:00:00Z",
	CursorSchema:     "credit-id-v1",
	Boundary:         []byte("2026-11-07T00:00:00Z"),
}, func(ctx context.Context, checkpoint batch.Checkpoint) (batch.Progress, error) {
	rows, nextCursor, err := repository.FindNextPage(ctx, checkpoint.Cursor, checkpoint.Definition.Boundary)
	if err != nil {
		return batch.Progress{}, err
	}
	if err := repository.ExpireInTransaction(ctx, rows); err != nil {
		return batch.Progress{}, err
	}
	return batch.Progress{Cursor: nextCursor, Processed: int64(len(rows)), Done: len(rows) == 0}, nil
})
```

The callback receives the transaction context supplied by `Store.Apply`.
Business writes and the checkpoint receipt must commit or roll back together.
Do not perform HTTP calls, publish broker messages, or make other irreversible
changes in a page callback; write an outbox record in the same transaction for
those effects. Keep the page short and use stable keyset ordering in the
application repository.

The runner claims the job with a fenced lease from the Store, validates cursor
progress and bounds, and processes one page at a time. `MaxPages` limits one
segment; a nonterminal result can be resumed with the same definition. A
completed key is terminal, and changing its fingerprint, cursor schema, or
boundary is a conflict. Use a new key for a deliberate rerun.

An ambiguous page result is retried once with the same step ID so the Store can
prove the exact committed receipt or safely rerun a rolled-back transaction.
The Store must check owner, fence, lease expiry, expected version, and receipt
while holding its durable checkpoint row lock. The runner does not treat a
larger version alone as proof that its page committed.

Fencing only protects data that checks the token. The Store must persist the
authority and high-water fence with the protected rows. External providers need
their own idempotency support. Checkpoint retention and authority transitions
after restore belong to the application.

For an Ent schema mixin and a PostgreSQL transaction/row-lock reference, see
[`batch/ent`](ent/README.md). To run a batch segment as a durable Temporal
Activity with bounded history, see [`workflow/temporal`](../temporal/README.md).
