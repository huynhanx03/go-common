# Wide-column repository

`widecolumn.BaseRepository` provides the small set of CQL operations that can
be implemented safely from generic model metadata:

- validated, explicitly quoted table and column identifiers;
- create/upsert, key lookup, existence, and delete;
- bounded logged or unlogged batches;
- one-page scans using the driver's opaque paging state;
- strict row mapping and iterator error propagation.

Create the client with `NewClientChecked` (or use `New`, which connects
immediately) so invalid seed hosts and connection limits fail during boot.
The client clones the settings object; applying defaults never mutates config
owned by the caller. `Connect` is safe to retry: a replacement session is
connected before the previous session is closed. `Ping(ctx)` executes a
lightweight system query for readiness checks.

New services should construct repositories during boot with the checked
constructor:

```go
repository, err := widecolumn.NewCheckedBaseRepository(
	session,
	Event{},
	widecolumn.WithIDColumn("event_id"),
	widecolumn.WithMaxBatchSize(25),
)
if err != nil {
	return err
}
```

`WithMapper` can inject a service-owned mapper when a model needs additional
value conversions; otherwise the package's immutable default mapper is used.

The original `NewBaseRepository` remains source-compatible. It retains any
configuration error and returns that error from every operation; callers can
also inspect it with `ValidationError`.

## Pagination contract

`Find` always fetches at most `dto.MaxPageSize` rows. Pass the response's
`pagination.next_cursor` back as `pagination.cursor` to fetch the next page.
The token is an opaque, bounded base64url encoding of the database driver's
paging state. Applications must not decode, log, or derive business meaning
from it.
`total_items` and `total_pages` are intentionally zero because a generic
full-table count would be an unbounded Cassandra operation; use a domain-level
counter when an exact total is needed.

Offset pages, generic filters, and generic sorts intentionally fail with
`ErrUnsupportedQuery`. Cassandra and Scylla queries are safe and predictable
only when partition/clustering-key access patterns are explicit. Implement
those queries in a domain repository; do not add `ALLOW FILTERING` to this
generic layer.

## Batch contract

The compatibility default is a logged batch with a maximum of 50 statements;
configuration cannot raise the safety ceiling above 100 statements.
Use an unlogged batch only when its partitioning and atomicity trade-offs are
understood:

```go
widecolumn.WithBatchType(gocql.UnloggedBatch)
```

The repository rejects an oversized batch before submitting any statement. It
does not silently split a batch because doing so would create partial-success
semantics that the bulk API cannot report safely.
