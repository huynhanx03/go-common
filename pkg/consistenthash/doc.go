// Package consistenthash provides deterministic, weighted placement over an
// immutable consistent-hash ring. Reads are lock-free; Replace atomically
// publishes an entirely new member set.
//
// It is deliberately a placement primitive. Membership discovery, health
// policy, load feedback, and data migration belong to the calling service.
package consistenthash
