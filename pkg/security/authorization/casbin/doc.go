// Package casbin provides an immutable-snapshot Casbin runtime.
//
// The consuming service owns policy persistence, Ent adapters, role/resource
// catalogs, transactions, self-lockout and privileged-role invariants, audit,
// outbox publication, and desired-revision coordination. Runtime accepts only
// complete snapshots, prepares them without touching live policy, atomically
// publishes one validated snapshot, and never writes policy rows.
package casbin
