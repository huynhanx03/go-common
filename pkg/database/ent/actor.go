package ent

import (
	"context"
	"sync/atomic"
)

// ActorResolver extracts "who is acting" from a request context (a user ID,
// "system", "cron", ...). The audit mixins (ModifierMixin, SoftDeleteMixin)
// call it to stamp created_by / updated_by / deleted_by automatically.
type ActorResolver func(ctx context.Context) (actor string, ok bool)

var resolver atomic.Pointer[ActorResolver]

// SetActorResolver registers the application's actor resolver.
// Call once at application startup:
//
//	ent.SetActorResolver(func(ctx context.Context) (string, bool) {
//		return auth.UserID(ctx)
//	})
func SetActorResolver(r ActorResolver) {
	if r == nil {
		resolver.Store(nil)
		return
	}
	resolver.Store(&r)
}

// ActorFromContext resolves the current actor via the registered resolver.
// Returns ("", false) when no resolver is registered or the context carries
// no actor (background jobs, unauthenticated requests).
func ActorFromContext(ctx context.Context) (string, bool) {
	registered := resolver.Load()
	if registered == nil {
		return "", false
	}
	return (*registered)(ctx)
}
