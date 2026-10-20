// Package outbox relays durable messages from a caller-owned Store to a
// Publisher. It defines transport- and storage-neutral contracts so adapters
// can combine a transactional state change with an at-least-once message
// transport without coupling the relay to a schema, database, or broker.
//
// Store owns atomic claiming and opaque lease fencing. Relay bounds concurrency,
// destination-scoped message ownership, dependency call durations, retry
// attempts, and shutdown. Options.Destinations lets one relay own an explicit
// subset of a shared outbox; an empty list intentionally means all destinations.
// Wakeup is only a latency hint; polling remains the correctness path. A missing
// correlation ID receives a deterministic message-derived fallback so retries
// retain one trace, while malformed non-empty identifiers are terminal.
//
// Router is an optional immutable local Publisher. It provides deterministic
// named fan-out while Relay continues to own durable delivery semantics.
package outbox
