# Feature flags

`pkg/toggle` provides a reusable feature-flag management contract. It uses the
official Unleash Go SDK for local evaluation and includes an Ent/PostgreSQL
Store. There is no Unleash Server to operate: each service owns its database,
Ent client, migrations, authorization and eventual UI. `pkg/toggle/httpapi`
provides an optional CRUD handler that services mount in their admin API.

Add the package to a service with:

```sh
go get github.com/huynhanx03/go-common/pkg/toggle
```

The root package defines service-neutral `Scope`, `Flag`, `Rule`, `Source`,
`Store`, `Manager` and `Client` contracts. `pkg/toggle/ent` is the PostgreSQL
adapter and schema mixins; `pkg/toggle/unleash` wraps upstream evaluation.
Services may implement `toggle.Store` themselves for another database.

## Add the Ent schemas

Create two schemas in the service's Ent schema directory. The service chooses
the Go schema names; the table annotations are the adapter's required storage
contract.

```go
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	toggleent "github.com/huynhanx03/go-common/pkg/toggle/ent"
)

type ToggleScope struct{ ent.Schema }

func (ToggleScope) Mixin() []ent.Mixin {
	return []ent.Mixin{toggleent.ScopeMixin{}}
}
func (ToggleScope) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "toggle_scopes"}}
}

type ToggleFlag struct{ ent.Schema }

func (ToggleFlag) Mixin() []ent.Mixin {
	return []ent.Mixin{toggleent.FlagMixin{}}
}
func (ToggleFlag) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "toggle_flags"}}
}
```

Generate the service's Ent client and run its normal migration process. Ent
supplies each schema's default integer ID; PostgreSQL generates those IDs for
Store writes. `ScopeMixin` defines the unique namespace/environment pair and a
nonnegative revision. `FlagMixin` defines the unique namespace/environment/key
pair, enabled state, description, JSONB definition, and creation/update times.
The Store never runs DDL or manages a migration version.

The Store writes through the supplied SQL driver, so it does not run generated
Ent hooks, privacy rules, interceptors, or Go-only defaults. Extra columns on
these tables must be nullable or have database-side defaults. The adapter uses
its own short transactions and cannot join a service business transaction. If
the service requires hooks or a shared transaction, implement `toggle.Store`
with that service's Ent client instead.

## Create the Store and Manager

Share one Ent SQL driver between the generated service client and the toggle
Store. The generated client owns closing that driver and the underlying DB.

```go
db, err := sql.Open("postgres", dsn)
if err != nil {
	return err
}
sqlDriver := entsql.OpenDB(dialect.Postgres, db)
appEnt := appent.NewClient(appent.Driver(sqlDriver))
if err := appEnt.Schema.Create(ctx); err != nil {
	return err
}

store, err := toggleent.NewStore(sqlDriver)
if err != nil {
	return err
}
manager, err := toggle.NewManager(store)
if err != nil {
	return err
}
```

`NewStore` accepts only the PostgreSQL dialect. It does not open, close, or
migrate the database. Keep the generated Ent client or DB under the service's
normal lifecycle and close it only after toggle clients stop.

`Manager` is the management surface for service admin endpoints. Protect those
endpoints with the service's own authentication and authorization. Writes use
scope-wide optimistic revisions, so concurrent writes to different keys in
one scope also conflict by design:

```go
snapshot, err := manager.List(ctx, scope)
if err != nil {
	return err
}
nextRevision, err := manager.Put(ctx, scope, toggle.Flag{
	Key: "checkout-v2", Enabled: true,
	Rules: []toggle.Rule{{
		Rollout: 25,
		Constraints: []toggle.Constraint{{
			ContextName: "region", Operator: "IN", Values: []string{"us"},
		}},
		Variants: []toggle.Variant{{
			Name: "compact", Weight: 1000,
			Payload: &toggle.Payload{Type: "json", Value: `{"layout":"compact"}`},
		}},
	}},
}, snapshot.Revision)
```

Pass the returned revision to the next write. `ErrConflict` means another
writer advanced the complete scope snapshot; read it again and apply the
service's conflict policy. Put replaces the full definition. Delete of a
missing key returns `ErrNotFound` without advancing the revision.

## Connect the management API and authorization

`pkg/toggle/httpapi` exposes CRUD endpoints over a `Manager`. It requires an
`Authorizer`, so callers cannot construct the handler without providing a
scope authorization decision. Authentication middleware should put the
service's principal in the request context; the authorizer then checks whether
that principal may manage the requested namespace and environment.

```go
management, err := httpapi.New(manager, httpapi.AuthorizerFunc(
	func(ctx context.Context, scope toggle.Scope) error {
		principal, ok := auth.PrincipalFromContext(ctx)
		if !ok {
			return httpapi.ErrUnauthenticated
		}
		allowed, err := policy.CanManageFeatureFlags(ctx, principal, scope)
		if err != nil {
			return err // The handler fails closed with 500; do not hide policy outages.
		}
		if !allowed {
			return httpapi.ErrForbidden
		}
		return nil
	},
))
if err != nil {
	return err
}

adminMux.Handle("/admin/", authMiddleware(http.StripPrefix("/admin", management)))
```

The principal, middleware and policy calls are service-owned examples: replace
them with the service's auth APIs. Keep any network allowlists, CSRF controls
and rate limits in the service's normal admin API middleware.

The routes are:

| Method | Path | Behavior |
| --- | --- | --- |
| `GET` | `/v1/toggles/{namespace}/{environment}` | Return the complete snapshot and its `ETag` revision. |
| `GET` | `/v1/toggles/{namespace}/{environment}/{key}` | Return one flag and its scope `ETag` revision. |
| `PUT` | `/v1/toggles/{namespace}/{environment}/{key}` | Replace a flag; body is a `toggle.Flag`, and `key` must match the path. |
| `DELETE` | `/v1/toggles/{namespace}/{environment}/{key}` | Delete a flag. |

`PUT` and `DELETE` require `If-Match: "<revision>"`, copied from the last
`ETag`. The handler returns `428` when it is missing, `412` when another
writer has advanced the scope, `404` for an unknown flag, and `400` for an
invalid definition or request. Successful writes return the new `ETag`;
`PUT` also returns `{"revision": N}`. After a `412`, the UI can reload the
snapshot and ask the user to reapply their edit.

## Evaluate locally with Unleash

Bind a Client to one scope. `Source` can be the Manager or any implementation
that returns complete, consistent `toggle.Snapshot` values.

```go
flags, err := unleash.New(ctx, unleash.Config{
	Scope: scope, AppName: "billing-api", Source: manager,
	RefreshInterval: 15 * time.Second,
	OnError: func(err error) { recordRefreshFailure(err) },
})
if err != nil {
	return err // first snapshot could not be loaded or validated
}
defer func() {
	closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = flags.Close(closeCtx)
}()

evaluation := toggle.EvaluationContext{
	UserID: user.ID,
	Properties: map[string]string{"region": user.Region},
}
enabled, err := flags.IsEnabled(ctx, "checkout-v2", evaluation, false)
if err != nil {
	return err
}
if enabled {
	variant, err := flags.GetVariant(ctx, "checkout-v2", evaluation)
	if err != nil {
		return err
	}
	if variant.Enabled && variant.Payload != nil {
		useCheckoutLayout(variant.Payload.Value)
	}
}
```

Keep this client as a service-level singleton for its bound scope. During
shutdown, close it with a bounded context before closing the Ent SQL driver or
database. Construct one client per scope/environment used by that process.

`fallback` applies only to an unknown boolean flag or an evaluation error;
disabled flags evaluate false. Unknown or disabled variants return a disabled
result. A Client uses the SDK's in-memory evaluation state. Evaluation does not
query PostgreSQL, Redis, or the common cache package, and it does not keep a
per-user result cache. No additional cache adapter is required.

The SDK refreshes complete snapshots in the background. The default interval
is 15 seconds, but propagation also includes source work, scheduling and SDK
retry backoff; it is not a maximum delay. A successful Manager write means the
database commit completed, not that every process has applied it. During a
database outage, startup fails if the first snapshot cannot load; an existing
Client retains its last accepted state and reports refresh errors through
`OnError` until the source recovers. Keep that callback quick, nonblocking,
nonpanicking, and do not call `Close` from it.

Rules are ORed; constraints within a rule are ANDed. Percentage allocation,
stickiness and variants use the upstream Unleash SDK. Supported constraint
operators are `IN`, `NOT_IN`, `STR_CONTAINS`, `STR_STARTS_WITH`,
`STR_ENDS_WITH`, `NUM_EQ`, `NUM_LT`, `NUM_LTE`, `NUM_GT`, and `NUM_GTE`.
Stickiness can use `userId`, `sessionId`, `default`, `random`, or a custom
evaluation property. `default` uses the SDK's user/session fallback behavior;
`random` is intentionally not stable between calls. A Client rejects reserved
context properties such as `userId`, `appName`, and `environment`; supply
identity through the dedicated context fields.

See [the package examples](example_test.go) and [Unleash SDK example](unleash/example_test.go).
