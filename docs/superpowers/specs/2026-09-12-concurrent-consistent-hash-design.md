# Production Concurrent Consistent Hash for Go Common

**Date:** 2026-09-13

**Status:** Proposed

**Target package:** `pkg/consistenthash`
**Primary use case:** deterministic affinity and fallback ordering across independently running Go processes. Judgify may use it only as a soft routing accelerator after workload evidence; its PostgreSQL scheduler remains authoritative.

## Executive decision

Build a virtual-node consistent-hash **placement selector** around an immutable snapshot. Writers construct a complete replacement snapshot under a mutex and publish it with `atomic.Pointer`; readers never take a lock. Store multi-member rings as `points []uint64` plus an adaptive `[]uint16` or `[]uint32` owner-index array and a sorted member table; an empty ring stores no points and a singleton returns its sole member directly. For sufficiently large rings, build an adaptive high-bit prefix index that narrows binary search while preserving exact vnode-ring mapping.

Use XXH64 from the repository's existing `pkg/hash` package, deterministic binary token encoding, stable member IDs, explicit integer capacity weights, build-time collision resolution, zero-allocation lookup APIs, `LookupNInto` for distinct fallbacks, and an algorithm version plus checksum for cross-process drift detection.

This is deliberately not a copy of any one upstream implementation:

- From groupcache: the small, understandable sorted vnode ring.
- From `buraksezer/consistent`: precompute expensive work and keep lookup tiny.
- From go-zero: 64-bit hashing, weights, removal, and explicit collision awareness.
- Added for this product: lock-free reads, atomic whole-membership replacement, compact resident memory, deterministic collision handling, exact-mapping prefix acceleration, generic typed values, and reproducibility checks.

## Goals and non-goals

### Goals

- Concurrent lookup and membership replacement with no reader locks.
- Deterministic results across processes, architectures, and membership input order.
- Minimal key movement when a member is added or removed under stable hash/token settings and collision-free token positions; forced collision recovery is tested separately.
- Weighted members using stable, bounded integer capacity units.
- Primary lookup and ordered distinct fallback lookup.
- Zero heap allocations on steady-state `Lookup`, `LookupHash`, and caller-buffer `LookupNInto` paths.
- Compact resident memory and cache-friendly lookup.
- Memory shrinks when confirmed membership shrinks, without changing surviving members' token counts.
- Explicit failure for malformed membership and irrecoverable hash collisions.
- Observable algorithm version, snapshot generation, and checksum.

### Non-goals

- Service discovery, gossip, heartbeats, leases, health checking, or consensus.
- Automatic physical-worker scaling, background sweeps, implicit goroutines, automatic vnode-count changes, partition-count changes, or data handoff.
- Dynamic request-load balancing. Weight is static capacity, not current utilization.
- Exactly-once execution or ownership authority. Judgify must retain PostgreSQL claim/lease/fencing semantics.
- Cryptographic or adversarially keyed hashing. XXH64 is selected for trusted routing keys.
- Topology-aware rack/zone placement. Callers can request more candidates and apply policy outside the selector.

## Research findings

### Google groupcache

The reference implementation is a classic continuum: each member gets `replicas` points, points are sorted, and lookup uses `sort.Search` followed by wraparound. It is an excellent teaching implementation but not a production concurrent primitive:

- default CRC32 and a 32-bit hash space;
- `[]int` plus `map[int]string` resident data;
- string concatenation and conversion while building;
- hash collisions silently overwrite the map entry;
- no synchronization, removal, weights, multi-candidate lookup, or snapshot semantics.

Its main lesson is that ring semantics should remain simple and auditable, not that its storage or concurrency design should be copied.

### buraksezer/consistent

This implementation combines virtual nodes with a fixed number of partitions and a bounded-load assignment policy. Lookup hashes the key to a partition ID and performs a map lookup under `RLock`, so lookup is independent of member count. The defaults are 271 partitions, replication factor 20, and load 1.25.

Strong ideas:

- expensive distribution is precomputed;
- bounded-load assignment is explicit;
- partition count is a stable configuration contract;
- primary lookup is very fast.

Tradeoffs for `go-common`:

- readers take `RWMutex.RLock`, while add/remove holds the writer lock during redistribution;
- multiple maps and interface values increase resident memory and pointer chasing;
- fixed partition count quantizes balance and changing it remaps globally;
- `GetClosestN` hashes and sorts every member for every call, making it unsuitable as a hot fallback path;
- membership rebuild failures can panic rather than return a validation error.

The precomputation lesson is retained, but not the fixed-partition mapping. The proposed prefix index is only a search accelerator, so rebuilding or resizing it never changes ownership.

### go-zero `core/hash`

go-zero uses a 64-bit Murmur hash, at least 100 replicas, weights, `RWMutex`, sorted ring keys, and `map[uint64][]any`. A collision bucket avoids groupcache's silent overwrite and a second hash selects within that bucket.

Strong ideas:

- 64-bit ring;
- weighted membership and remove support;
- collisions are represented instead of discarded.

Tradeoffs for this product:

- every lookup takes `RLock`;
- `any`, representation conversion, maps, and collision slices add allocations or pointer chasing;
- `AddWithReplicas` performs remove and add as two observable phases;
- no whole-snapshot replace, `LookupN`, checksum, or cross-process compatibility contract.

We retain 64-bit points and collision correctness, but resolve collisions deterministically while building so the read snapshot needs no map or collision slice.

### Orientation benchmarks

An initial Apple M5 / current-Go audit produced the following orientation numbers from each repository's own benchmark shape. They are useful for finding hot-path problems, but they are **not** a fair winner table because key types, hash functions, member counts, and semantics differ. The implementation plan therefore requires a controlled comparison harness before accepting performance claims.

| Implementation/path | Observed order of magnitude | Allocation observation | Interpretation |
|---|---:|---:|---|
| groupcache `Get` | ~25 ns at 8 members to ~48 ns at 512 | 1 alloc/op | compact code and binary search are fast, but string→bytes conversion and no concurrency safety |
| buraksezer `LocateKey` | ~10 ns | 0 alloc/op | fixed partition lookup is an excellent throughput baseline |
| buraksezer `GetClosestN` | ~0.4 µs at 8 to ~51 µs at 512 | grows substantially | hashes/sorts members per request; not a viable fallback hot path |
| go-zero `Get` | ~96 ns | ~25 B, 3 allocs/op | lock, representation conversion, interface values, and maps are visible costs |

The target is not merely to beat a number: it is to combine zero-allocation lookup, non-blocking readers during rebuild, correct `LookupN`, stable arbitrary IDs, and bounded resident memory under one workload-equivalent benchmark.

### Other production references

- Karger's consistent hashing establishes the minimal-disruption ring model.
- Envoy exposes weighted ring hash and uses XXH64 by default; its ring size controls balance and memory. Envoy treats bounded load as a separate policy concern.
- Google's bounded-load work is valuable when overload is the requirement, but it is not a reason to mix changing runtime load into a deterministic ownership primitive.
- Cassandra's vnode experience reinforces that token count is an operational tradeoff, not “more is always better.”
- Grafana dskit demonstrates that replication, health, zones, and lifecycle are higher-level ring concerns and substantially enlarge an otherwise small hashing primitive.

## Options considered

| Design | Lookup | Resident memory | Reader concurrency | Main drawback |
|---|---:|---:|---|---|
| groupcache-style ring | `O(log V)` | slice + map per token | unsafe without external lock | collisions and concurrency |
| `RWMutex` vnode ring | `O(log V)` | implementation-dependent | readers serialize on lock cacheline | writer stalls readers |
| fixed partition table | `O(1)` | `O(P)` or `O(P*K)` | can be lock-free | partition count changes mapping |
| rendezvous / HRW | `O(N)` | `O(N)` | naturally stateless | hashes every member per lookup |
| Jump hash | `O(log N)` | `O(N)` member table | naturally stateless | dense numeric buckets; awkward stable arbitrary IDs and `LookupN` |
| immutable compact vnode ring | `O(log V)` | about 10 or 12 bytes/token | lock-free | binary search cost |
| **immutable ring + optional prefix index** | **`O(log(V/2^B))`** | **10 or 12 bytes/token + up to ~16 KiB default index** | **lock-free** | more builder/testing work |

`V` is total vnode count, `P` fixed partitions, `N` members, and `B` prefix bits.

## Proposed architecture

### Public model

```go
package consistenthash

type Member[T any] struct {
    ID     string
    Value  T
    Weight uint16 // 0 means the default weight of 1
}

type Options struct {
    VirtualNodes       uint32 // default 256 per weight unit
    MaxWeight          uint16 // default 64
    MaxTotalTokens     uint32 // default 1,048,576
    MaxMembers         uint32 // default 65,536; configurable up to 1,048,576
    MaxMemberIDBytes   uint32 // default 256
    MaxTotalIDBytes    uint32 // default 1,048,576
    PrefixTarget       uint32 // default 8 tokens per indexed bucket
    MaxPrefixBits      uint8  // default 12; at most ~16 KiB
    CollisionAttempts  uint8  // default 8
}

type Ring[T any] struct { /* private */ }

func New[T any](opts Options) (*Ring[T], error)
func (r *Ring[T]) Replace(members []Member[T]) error
func (r *Ring[T]) Lookup(key []byte) (Member[T], bool)
func (r *Ring[T]) LookupHash(sum uint64) (Member[T], bool)
func (r *Ring[T]) LookupNInto(key []byte, dst []Member[T]) int
func (r *Ring[T]) LookupNHashInto(sum uint64, dst []Member[T]) int
func (r *Ring[T]) Len() int
func (r *Ring[T]) Generation() uint64
func (r *Ring[T]) Checksum() uint64
```

`LookupHash` is the fastest path and lets callers hash strings without `[]byte` conversion using the existing `hash.Sum64(string)`. `LookupNInto` writes distinct members into caller-owned storage and returns the number written. If `dst` is larger than membership, it returns every member once.

`Replace` is the authoritative mutation API. Small `Add`/`Remove` conveniences are intentionally excluded: callers usually receive a complete discovery snapshot, and read-modify-write conveniences invite lost updates and unclear ordering.

`Replace` is the sole membership update API. Callers can scale actual workers up or down by submitting a new authoritative membership; the ring never decides when to do so and never starts a goroutine. A `Replace` that fails validation, budget checks, or collision handling returns an error without publishing. No API promises to migrate data or grant a lease. No public knob silently changes virtual-node count as membership size changes: a surviving member retains identical token inputs unless its explicit weight/configuration changes.

### Snapshot and concurrency

```go
type Ring[T any] struct {
    writeMu sync.Mutex
    opts    normalizedOptions
    state   atomic.Pointer[snapshot[T]]
}

type snapshot[T any] struct {
    generation uint64
    checksum   uint64
    members    []Member[T] // sorted by stable ID
    points     []uint64
    owners16   []uint16   // exactly one owner array is populated
    owners32   []uint32   // when member count exceeds 65,536
    prefixBits uint8
    prefix     []uint32
}
```

Readers load one pointer and touch immutable arrays. Writers serialize, validate and copy input, build the complete snapshot, then perform one atomic store. A reader observes either the old complete state or the new complete state; it never observes temporary removal, partial sorting, or mixed arrays.

Holding the writer mutex through build gives `Replace` calls a simple linearizable order. Membership changes are rare, so optimizing writer throughput at the cost of ambiguous completion order is not worthwhile. For zero members, lookup returns `false`; for exactly one member, both primary and top-K lookup return that member without storing or searching vnode points. Still validate the *logical* token budget for a singleton so adding a second member cannot silently bypass configured limits.

### Compact ring layout

The read snapshot contains no per-token map. Each multi-member token consumes approximately:

- 8 bytes in `points`;
- 2 bytes in `owners16` for at most 65,536 members, or 4 bytes in `owners32` above that;
- no resident collision object.

This is about 10 or 12 bytes per token excluding slice headers and member values. The builder may use a temporary sortable token struct and collision map; both become garbage after publication and do not pollute the hot-path layout. The lookup dispatches to the selected owner width once per snapshot, with no interface, map, or per-token branch; benchmark both widths to ensure the narrow representation is beneficial. Switching owner width or prefix configuration must never change the selected owner or checksum.

The resource contract is a **bounded ring structure**, not a hard process-RSS limit. Reject excessive member counts, IDs, total ID bytes, weight products, or total *logical* tokens before allocation; use overflow-safe arithmetic. Clone stable IDs when publishing so a short substring cannot pin a caller's enormous backing string. Published arrays have exact capacity. `Member.Value` may contain arbitrary caller-owned objects; referenced `T` values are not deep-copied and must be treated as immutable. During rebuild, the old snapshot, builder scratch, and new snapshot coexist; old readers can pin previous generations and Go's GC may defer reclamation. Document/benchmark retained structure and peak rebuild allocations separately. Do not claim a hard RSS cap or spin a memory-sweeper goroutine.

### Adaptive exact-mapping prefix index

For large rings, build `prefix` with `2^B + 1` entries. Entry `i` is the lower-bound token index for the start of high-bit bucket `i`:

```text
bucket = hash >> (64 - B)
lo     = prefix[bucket]
hi     = prefix[bucket+1]
index  = lower_bound(points[lo:hi], hash)
if not found in bucket: index = hi
if index == len(points): index = 0
```

This is not a partition ownership table. It merely narrows the exact lower-bound search, so changing `B` cannot move a key. Empty buckets point directly at the next clockwise token.

Choose `B` per snapshot to target eight tokens per bucket, capped at 12 bits, and omit the index when it would not materially shorten search. Typical extra memory is 8–16 KiB for tens of thousands of tokens.

### Determinism and token generation

1. Validate nonempty member IDs and weights, member/ID-byte limits and overflow-safe logical token budget before allocating points.
2. Copy members, clone IDs, and sort by `ID` byte order.
3. For each nonempty membership, derive `VirtualNodes * effectiveWeight` *logical* tokens per member using the same collision policy; singleton snapshots hash them for checksum but do not retain point/owner arrays. Empty snapshots have no tokens.
4. Hash a canonical binary record containing algorithm domain/version, ID length, ID bytes, vnode ordinal, and collision attempt.
5. If a point is occupied, increment the attempt and rehash. After the configured limit, return `ErrTokenCollision`.
6. Sort by point and compact into parallel arrays.

Do not concatenate decimal strings with member names: ambiguous encodings and avoidable allocations are unnecessary. Stable sorting plus deterministic retries ensures every process resolves an artificial collision identically.

Weight is a small integer capacity multiplier. A default member gets 256 tokens; weight 2 gets 512. The fixed multiplier means adding a member does not renormalize existing members and therefore preserves minimal-movement behavior. Validation caps weight and total tokens to prevent accidental memory exhaustion.

### LookupN

For an empty or singleton ring, return zero or one distinct owner immediately. Otherwise start at the primary token and walk clockwise until `dst` is full or every token has been inspected. Skip an owner already present in `dst`; expected replication factors are small, so a linear scan over selected members avoids a map allocation and is faster than allocating a deduplication structure. Choose the narrow/wide scanning path once, rather than branching on owner width for every token.

The order is deterministic and useful for replicas or failover candidates. Health filtering remains outside the ring: ask for enough candidates, then apply live health/zone policy. This avoids changing global ownership because one process observed a transient heartbeat differently.

### Version and checksum

Checksum input includes:

- algorithm identifier and version;
- compatibility-affecting settings: `VirtualNodes`, the fixed XXH64/encoding version, and the collision-attempt policy;
- sorted member IDs and effective weights;
- generated logical point/owner sequence for all nonempty rings, including a singleton whose token arrays are not retained. An empty ring has no point records.

It excludes `Value`, generation, prefix size, owner-index width, and resource-budget options (`MaxMembers`, ID limits, `MaxWeight`, `MaxTotalTokens`) because these do not define the mapping for an already accepted membership. Expose the checksum to logs/metrics so processes can detect divergent ownership inputs; budget mismatches need separate configuration monitoring.

Any future change to hashing, token encoding, collision resolution, or weight interpretation must increment the algorithm version. Performance-only prefix-index changes do not require a version bump because they preserve the exact result.

`VirtualNodes` and member weights are compatibility-affecting settings, not automatically tuned based on member count, traffic, or memory pressure. Modifying either is an explicit routing migration with the same careful review as changing the hash algorithm. Algorithm version is internal compatibility metadata, not a staged product release.

## Scaling and optional partitioned consumers

Physical workers can be added or removed only via an externally confirmed `Replace`; the next snapshot contains fewer or more tokens and its predecessor becomes collectible once readers release it. A service may have a separate, explicitly started controller that measures demand and coordinates drain/handoff, but this package does not own a lifecycle, goroutine, ticker, discovery client, or autoscaling policy. Health flaps must not cause unconditional membership churn.

For a future partitioned Forge, use a stable `message key -> partition ID` rule and compute `partition ID -> preferred worker` from this ring when membership changes; publish an owner table for direct hot-path routing. A fixed, small number of partitions cannot be made arbitrarily even by adding vnodes. Expensive partitions may need a separate load-aware assignment planner. Forge currently has one commit log per topic in a single owner process; multi-partition offsets, durable storage access, transfer, leases/fencing and activation belong to a separate Forge design. Neither ring publication nor a matching checksum transfers authoritative ownership. This is an integration boundary, not an implementation task of this spec.

## Judgify integration

- Current Judgify `worker_key` includes hostname and PID, so it is not stable across restarts and must not be used as `Member.ID`. Introduce a separate provisioned logical `routing_id` if ring affinity is adopted; never use worker UUID, boot UUID, pod UID, transient address, PID, slice index, or database row order.
- Keep `boot_id` and connection information in `Member.Value` for contacting the current worker instance.
- Do not gate the existing worker-pull claim query by ring ownership. It already enforces compatibility, fairness, capacity and exclusive leasing in PostgreSQL.
- If a future centralized dispatcher or measured cache-affinity path is introduced, build one ring per genuinely distinct eligibility pool only if necessary; otherwise use `LookupNInto` and filter candidates by capability to avoid many small rings.
- In that future path, use the ring only to select a preferred worker and fallback order.
- PostgreSQL remains the authority for claim, lease expiry, fencing token, retries, and recovery. A hash match is a routing hint, not proof of ownership.
- Do not rebuild the ring for short health flaps. Discovery should debounce membership replacement; transient health is applied while selecting from the candidate list.

The broader algorithm and open-source comparison is documented in `docs/superpowers/specs/2026-09-12-consistent-hashing-landscape.md`.

## Performance and correctness gates

Implementation is not production-ready until all gates pass:

- `LookupHash`, `Lookup`, and caller-buffer `LookupNInto` report 0 allocations/op.
- Empty and singleton snapshots retain no vnode arrays; singleton lookups return their sole member and at most one distinct fallback.
- `owners16` is selected at at most 65,536 members and `owners32` above it; ownership/checksum is identical for both layouts.
- `go test -race ./pkg/consistenthash` passes with concurrent `Replace` and lookup loops.
- For equal members and collision-free token positions, adding one member only moves keys to the new member; removing one only moves keys that belonged to it. Check 1↔2 transitions against a full-token oracle.
- Input permutation yields identical checksum and lookup results.
- Artificial low-entropy hashes produce deterministic collision recovery or a bounded error, never overwrite or hang.
- Across 200 deterministic 64-member scenarios at 256 vnodes/member, p95 ownership CV is at most 8% and p95 max/min ratio is at most 1.50. Measure exact continuum intervals so the test is fast and free of key-sampling noise.
- Across the same scenario family, the new member's share for 64→65 falls within 12% of theoretical `1/65` at the 5th–95th percentiles. For removal, only keys owned by the removed member may move.
- Resident multi-member point/index storage is bounded by `(8 + ownerWidth) * storedTokens + 4 * (2^prefixBits + 1)` bytes, where `ownerWidth` is 2 or 4; member/ID memory and transient build allocations are separately budgeted and measured. This is not a process-RSS bound.
- Benchmarks cover 8, 32, 128, and 512 members; 64, 256, and 512 vnodes; primary hash-only, primary bytes, `N=3`, replacement, parallel lookup, and lookup during replacement.
- A benchmark report compares the same key type, hash function, member count, and compiler against groupcache, `buraksezer/consistent`, and go-zero. Upstream README numbers are context, not an acceptance comparison.

## Risks and mitigations

- **Too many vnodes:** cap logical tokens, member count, and total cloned ID bytes; document resident/peak memory separately; benchmark 256 as the default rather than assuming more is better.
- **Mutable generic values:** the ring copies the `Member` struct but cannot deep-copy `T`; document that callers must treat referenced values as immutable.
- **Untrusted keys:** XXH64 is non-cryptographic. Use a keyed hash in an outer layer if an attacker can deliberately create routing hotspots.
- **GC burst during rebuild:** build frequency should be low; benchmark peak allocation and retain only compact arrays. Pooling is deferred until profiles prove it useful.
- **Algorithm drift:** version and checksum are public diagnostics; golden vectors pin behavior.
- **Overloading one worker:** static consistent hash does not observe queue depth. Use bounded inflight work, admission control, and fallback policy outside the ring.

## Sources

1. Google groupcache consistent hash source: https://github.com/golang/groupcache/blob/master/consistenthash/consistenthash.go
2. buraksezer/consistent source and documentation: https://github.com/buraksezer/consistent
3. go-zero consistent hash source: https://github.com/zeromicro/go-zero/blob/master/core/hash/consistenthash.go
4. Karger et al., *Consistent Hashing and Random Trees*: https://people.csail.mit.edu/karger/Papers/web.pdf
5. Envoy load-balancer architecture: https://www.envoyproxy.io/docs/envoy/latest/intro/arch_overview/upstream/load_balancing/load_balancers.html
6. Envoy ring-hash configuration: https://www.envoyproxy.io/docs/envoy/latest/api-v3/extensions/load_balancing_policies/ring_hash/v3/ring_hash.proto
7. Google Research, *Consistent Hashing with Bounded Loads*: https://research.google/pubs/consistent-hashing-with-bounded-loads/
8. Cassandra vnode configuration: https://github.com/apache/cassandra/blob/trunk/conf/cassandra.yaml
9. Grafana dskit ring implementation: https://github.com/grafana/dskit/blob/main/ring/ring.go
10. Lamping and Veach, *A Fast, Minimal Memory, Consistent Hash Algorithm* (Jump Hash): https://arxiv.org/abs/1406.2294
