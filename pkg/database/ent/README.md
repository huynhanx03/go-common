# ent

Shared toolkit for [Ent](https://entgo.io) based services: audit mixins, soft
delete, transactions, query logging, safe dynamic filters, and error mapping.
Generic parts live here; anything that needs the generated client stays a
one-liner in the application.

## Setup

### 1. Driver

```go
drv, err := ent.NewDriver(cfg.Database) // mysql or postgres, pooled + pinged
client := gen.NewClient(gen.Driver(ent.WrapLogging(drv, 0))) // 0 = 200ms slow threshold
```

`WrapLogging` times every statement and logs through `logger.FromContext`,
so lines carry the request's `cid`. Errors log at Error, statements slower
than the threshold at Warn, everything at Debug. There is no query-log
switch: the logger's level is the switch — dev runs at Debug and sees every
statement, prod runs at Info and the Debug lines (the only place args
appear) are dropped before formatting.

### 2. Actor resolver (who is acting)

Register once at startup; the mixins use it to stamp `created_by`,
`updated_by`, and `deleted_by`:

```go
ent.SetActorResolver(func(ctx context.Context) (string, bool) {
    return auth.UserID(ctx) // user id, "system", "cron", ...
})
```

### 3. Recommended `entc.go` feature flags

```go
//go:build ignore

package main

import (
    "entgo.io/ent/entc"
    "entgo.io/ent/entc/gen"
)

func main() {
    err := entc.Generate("./schema", &gen.Config{Features: []gen.Feature{
        gen.FeatureIntercept,      // interceptors (soft delete filtering)
        gen.FeatureUpsert,         // OnConflict: create-or-skip, upsert
        gen.FeatureModifier,       // Modify(): custom SELECT/UPDATE fragments
        gen.FeatureLock,           // ForUpdate / ForShare row locking
        gen.FeatureExecQuery,      // client.QueryContext: raw SQL
    }})
    ...
}
```

## Mixins

```go
func (User) Mixin() []ent.Mixin {
    return []ent.Mixin{
        e.UUIDMixin{},                 // id: UUIDv7 primary key (time-ordered, index-friendly)
        e.PublicIDMixin{Prefix: "UR"}, // public_id: "UR20260705xK9" — the ID users see
        e.TimeMixin{},                 // created_at / updated_at (UTC + DB-side default)
        e.ModifierMixin{},             // created_by / updated_by (stamped from the actor)
        mixin.SoftDelete,              // deleted_at / deleted_by — see below
    }
}
```

- `UUIDMixin` — UUIDv7 primary key via `ent.NewUUID()`. Time-ordered, so
  inserts append to the index instead of fragmenting it.
- `PublicIDMixin` — short human-friendly `public_id` (prefix + UTC date +
  base62 random via `unique.PublicID`), unique + immutable, generated
  automatically on create. Use it only on entities whose ID users actually
  see; keep the UUID internal.
- `TimeMixin` — timestamps in UTC, plus `CURRENT_TIMESTAMP` database
  defaults so rows inserted outside the app are stamped too.
- `ModifierMixin` — stamps the actor on create/update. Explicitly set
  values win; `ent.SkipModifier(ctx)` bypasses stamping (imports,
  migrations).

## Soft delete

Fields and query filtering are fully generic. Only the delete→update
conversion must re-enter the generated client, so the app declares a
one-line bridge:

```go
// internal/ent/mixin/soft_delete.go
type SoftDeleteMixin struct{ e.SoftDeleteMixin }

func (SoftDeleteMixin) Hooks() []ent.Hook {
    return []ent.Hook{e.SoftDeleteHook(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
        return m.(interface{ Client() *gen.Client }).Client().Mutate(ctx, m)
    })}
}
```

Behavior:

- `client.User.DeleteOneID(id).Exec(ctx)` → `UPDATE ... SET deleted_at = now(), deleted_by = actor`
- every query automatically appends `WHERE deleted_at IS NULL`
- `ent.SkipSoftDelete(ctx)` → queries include deleted rows, deletes are permanent

## Transactions

```go
err := ent.WithTx(ctx, client.Tx, func(t *gen.Tx) error {
    if _, err := t.Order.Create().Save(ctx); err != nil {
        return err
    }
    return t.Wallet.UpdateOneID(id).AddBalance(-10).Exec(ctx)
})
```

Commit on success, rollback on error, rollback + re-panic on panic. To
satisfy the shared `tx.Manager` interface (repositories resolve the tx
client from the context):

```go
manager := ent.NewTxManager(client.Tx, func(ctx context.Context, t *gen.Tx) context.Context {
    return gen.NewTxContext(ctx, t)
})
```

## Dynamic filters, sort, pagination

Apply client-supplied `dto.QueryOptions` at the selector level:

```go
users, err := client.User.Query().
    Modify(func(s *sql.Selector) {
        e.ApplyQueryOptions(opts, s, "name", "status", "created_at") // column whitelist
    }).
    All(ctx)
```

- Column names are validated (snake_cased, identifier-only) and — when a
  whitelist is passed — restricted to it. Malformed or non-whitelisted
  keys are skipped, never interpolated: the column position is not
  parameterized by the driver, so this is the injection guard.
- Filter operators via `SearchFilter.Type`: `search` (contains,
  case-insensitive), `exact`/`in`, `not_in`, `eq`, `neq`, `gt`, `gte`,
  `lt`, `lte`, `is_null`, `is_not_null`. Unknown types fall back to `eq`.
- No sort given → `id DESC`, so paginated pages stay stable.
- `Pagination.Cursor` set → keyset pagination (`WHERE id < cursor`, no
  OFFSET): O(1) at any depth, while `OFFSET n` scans and discards n rows.
  Pass the last row's id of the previous page; it pairs with the default
  `id DESC` order (UUIDv7 ids sort chronologically). Leave `Sort` empty
  when using a cursor.

## Fast totals for pagination

`COUNT(*)` walks every matching row — on large tables it costs more than
the page query. `CountWithEstimate` asks the planner first (EXPLAIN, O(1)):
small result → exact count (users read those numbers), huge result → the
estimate is plenty for "1.2M results, 60k pages":

```go
s := sql.Dialect(drv.Dialect()).Select("*").From(sql.Table(user.Table))
e.ApplyFilters(opts.Filters, s, allowedCols...)

total, err := e.CountWithEstimate(ctx, drv, s, 0) // 0 = exact under 10k
meta := dto.CalculatePagination(page, size, total)
```

Postgres and MySQL are supported; any estimation failure falls back to the
exact count automatically. Pass the filtered selector before sort and
pagination are applied.

## Common recipes (generated features)

```go
// Create, skip when it already exists (sql/upsert)
client.User.Create().SetEmail(e).OnConflict().DoNothing().Exec(ctx)

// Upsert: insert or refresh (sql/upsert)
client.User.Create().SetEmail(e).SetName(n).OnConflict().UpdateNewValues().Exec(ctx)

// Partial select — fetch only what you need
client.User.Query().Select(user.FieldID, user.FieldName).All(ctx)

// Custom UPDATE fragment (sql/modifier)
client.User.Update().Modify(func(u *sql.UpdateBuilder) {
    u.Set("counter", sql.Expr(sql.Raw("counter + 1")))
}).Exec(ctx)

// Row locking inside a transaction (sql/lock)
t.User.Query().Where(user.ID(id)).ForUpdate().Only(ctx)

// Raw SQL when the builder can't express it (sql/execquery)
rows, err := client.QueryContext(ctx, "SELECT status, COUNT(*) FROM users GROUP BY status")
```

## Error mapping

Convert generated errors to `apperr.AppError` HTTP-ready codes. Register
the generated predicates once at startup, then map at the boundary:

```go
ent.RegisterErrorPredicates(ent.ErrorPredicates{
    IsNotFound:        gen.IsNotFound,
    IsValidationError: gen.IsValidationError,
    IsConstraintError: gen.IsConstraintError,
    IsNotLoaded:       gen.IsNotLoaded,
    IsNotSingular:     gen.IsNotSingular,
})

if err != nil {
    return ent.MapEntError(err, "user") // 404 / 409 / 400 / 500 with clean messages
}
```
