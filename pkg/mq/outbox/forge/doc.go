// Package forge adapts the domain-neutral outbox Publisher contract to a
// caller-owned set of Forge producers. The adapter does not create, close, or
// otherwise own producer lifecycle. Publish always requests AckFsync. Failed
// acknowledgments are ambiguous after either memory admission or append,
// because an admitted record can still be flushed after Publish returns.
package forge
