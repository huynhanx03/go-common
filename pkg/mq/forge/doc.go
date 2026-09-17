// Package forge provides an embedded, durable, at-least-once queue for one
// node and one owner process.
//
// A Broker exclusively owns its data directory. Producers support bounded
// batching and explicit memory, append, or fsync acknowledgments. Consumers
// expose absolute delivery offsets and persist progress only through explicit
// monotonic commits. The default retention mode protects every registered
// consumer group and applies storage backpressure instead of deleting
// unconsumed records.
//
// Log files are authoritative and sparse indexes are rebuilt from bounded,
// checksum-verified batches during startup. Read results own their bytes, have
// hard encoded/decoded memory ceilings, and remain valid after retention or
// shutdown unmaps a sealed segment. Broker-owned topic, producer, consumer,
// and durable consumer-group resources also have explicit configurable caps.
//
// Forge is deliberately domain-neutral. Applications should carry correlation
// and idempotency identifiers in headers and combine Forge with a transactional
// outbox when an authoritative state change must reliably emit a message.
package forge
