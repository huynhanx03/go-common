# Batch checkpoint Ent mixin

Embed `batchent.Mixin` in an application-owned Ent schema. The mixin stores the
immutable job identity and boundary, opaque cursor, committed version and
receipt, owner, lease expiry, and authority/fence high-water mark. `job_key` is
unique; `(status, lease_until)` supports due/active checkpoint queries. The
database checks keep version, processed count, and fence nonnegative.

```go
type Checkpoint struct{ ent.Schema }

func (Checkpoint) Mixin() []ent.Mixin {
	return []ent.Mixin{batchent.Mixin{}}
}
```

The application still owns migrations and the generated Ent client. Apply the
schema change through the application's migration workflow; do not reset or
delete checkpoint rows while workers can still run, because that would reset
the durable fencing high-water mark. Keep cursor and boundary size limits in
`batch.Config`, and use a new `JobKey` for an intentional rerun.

The integration-test Ent schema and PostgreSQL `Store` live in
`pkg/testutil/ent/testdata/batch/fixture`. They demonstrate row locks,
database-clock leases, receipt reconciliation, and transaction-scoped business
writes. They are test fixtures, not a generated production client or a
replacement for the service's repository.
