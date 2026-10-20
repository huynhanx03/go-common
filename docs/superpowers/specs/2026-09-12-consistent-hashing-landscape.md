# Consistent Hashing: Open-Source Landscape and Product Decision

## Executive conclusion

`go-common` should build a small, deterministic **placement selector**, not a distributed scheduler and not a membership system. The first engine should be a concurrent virtual-node ring with immutable snapshots, lock-free readers, compact arrays, exact-mapping prefix acceleration, stable IDs, checksum, and zero-allocation primary/fallback lookup.

That component is appropriate for cache affinity, sticky routing, deterministic sharding, preferred owners, and ordered failover candidates. It is not sufficient for exclusive ownership, health, overload protection, topology-aware replication, or data migration.

For Judgify specifically, the current durable worker path should **not** use consistent hashing to gate database claims. PostgreSQL already performs capability validation, global/per-user fairness checks, capacity reservation, `FOR UPDATE ... SKIP LOCKED`, leases, heartbeat and boot fencing. A ring can be evaluated later as a soft affinity accelerator—such as improving reusable artifact/cache locality—but only after a workload trace shows useful reuse and only while the database remains authoritative.

The current Judgify `worker_key` also contains hostname and PID. It distinguishes process boots operationally but is not a stable ring identity across restarts. If affinity routing is introduced, add a separate stable logical `routing_id`; do not reuse `worker_key`, `worker_id`, `boot_id`, pod UID, IP address, or slice index.

## The real problem space

“Consistent hashing” names several different placement problems:

1. **Affinity:** send the same key to the same healthy backend most of the time.
2. **Ownership:** decide which node is responsible for a key.
3. **Replication:** produce multiple distinct owners in a deterministic order.
4. **Load control:** prevent hot keys or uneven work from overloading one node.
5. **Topology placement:** spread replicas across hosts, racks, zones, or device classes.
6. **Migration:** move persisted data safely when ownership changes.
7. **Membership:** agree on which nodes exist and which version of membership is active.

A hash function solves only placement. Production systems add different mechanisms around it. Ringpop couples SWIM membership, a ring, checksums, and forwarding.^1 Grafana dskit adds health, lifecycle state, replication and zone awareness.^2 Ceph CRUSH consumes an explicit topology and placement rules.^3 Redis Cluster exposes fixed slots that operators can migrate.^4 HAProxy can reject an otherwise consistent first choice when its concurrent load exceeds a bound.^5

The product design must keep these concerns separate rather than growing one opaque “smart ring.”

## Algorithm families

### Virtual-node continuum / Ketama

Members own multiple points on a circular hash space. A key hashes to a point and selects the next member clockwise. More virtual nodes reduce random imbalance but increase build cost and memory.

Best fit:

- arbitrary stable member IDs;
- cache/request affinity;
- weighted nodes;
- deterministic clockwise `LookupN`;
- membership changes are less frequent than lookups.

Weak fit:

- millions of members;
- topology-aware replication without another policy layer;
- workload where job cost differs drastically and current load matters more than key count.

Representative implementations: groupcache, StatHat, serialx/hashring, go-zero, Uber Ringpop, NGINX Ketama, Envoy RingHash and Grafana dskit.

Amazon Dynamo used virtual nodes not only to smooth ownership, but also to spread a failed node's ranges across multiple survivors and to assign more ranges to higher-capacity machines.^27 This distinction matters: vnode count improves statistical distribution and recovery fan-out, but does not know actual request cost.

### Multi-probe consistent hashing

Multi-probe places each member only once or a few times, then derives multiple candidate positions from each key and selects the closest member among those probes. It trades additional per-key hash/search work for much lower vnode memory while improving balance over a one-point ring.^20

Best fit:

- many independent rings where hundreds of vnodes per member are too expensive;
- memory is more constrained than lookup CPU;
- primary selection matters more than a simple clockwise replica order.

Weak fit:

- the lowest possible hot-path latency;
- straightforward weights and `LookupN` semantics;
- a product requiring mature, widely deployed Go implementations.

It deserves a benchmark research branch, not inclusion in v1. The compact 12-byte vnode layout and adaptive prefix index are easier to audit and have much stronger operational precedent. If retained-memory measurements become the blocking constraint, compare multi-probe against Rendezvous before reducing correctness or observability in the main ring.

### Fixed partitions or slots

Hash a key into one of a fixed number of partitions, then consult a partition-owner table. Lookup is direct and operational tooling can migrate ownership partition by partition.

Best fit:

- persisted data where movement must be explicit and observable;
- operational resharding;
- a stable partition-count compatibility contract;
- bounded-load assignment computed during membership change.

Weak fit:

- a lightweight generic library where users may later change partition count;
- many tiny independent rings, because every ring pays table overhead;
- exact classic-ring movement semantics.

Representative implementations: `buraksezer/consistent`, Redis Cluster's 16,384 slots, Cassandra token ranges, and storage systems using intermediate placement groups.

### Rendezvous / highest-random-weight hashing

For each key, score every member and choose the highest score. It needs no vnode ring and handles arbitrary stable IDs naturally. Weighted variants and top-K selection are conceptually straightforward.

Best fit:

- small member sets;
- membership changes often;
- minimal implementation/state;
- direct top-K candidates;
- many independent selectors where retaining a vnode ring for each is wasteful.

Weak fit:

- hundreds or thousands of candidates on a very hot lookup path, because primary lookup is `O(N)`;
- implementations that mutate member arrays without atomic publication.

`dgryski/go-rendezvous` prehashes member IDs and then scores all members for every lookup. Its memory is small and lookup code is simple, but it has no synchronization and no built-in top-K/weights.^6

### Jump consistent hash

Jump maps a 64-bit key into dense buckets `[0, n)` with very small memory and roughly logarithmic work. It is elegant when buckets are consecutive and growth appends the next bucket.^7

Best fit:

- dense, stable integer shard IDs;
- append-only bucket growth;
- one primary owner;
- minimal memory.

Weak fit:

- arbitrary workers joining/leaving by stable string ID;
- removal of an arbitrary bucket;
- weights;
- ordered fallback owners.

A mapping layer from dense bucket index to arbitrary workers reintroduces a membership compatibility problem. Removing or reordering entries can remap far more keys than users expect.

### AnchorHash

AnchorHash supports arbitrary removals from a predeclared bucket capacity with small lookup cost and minimal disruption. The Go implementation stores arrays describing active/removed buckets and exposes a path API.^8

Best fit:

- maximum bucket capacity is known in advance;
- bucket IDs are dense integers;
- removals are arbitrary but additions restore previously removed buckets;
- every participant agrees on the exact ordered history of changes.

Weak fit:

- arbitrary string IDs and weights;
- independently observed membership snapshots without a total order;
- a generic `LookupN` API.

The implementation explicitly notes that agents must agree on removal order. That is an extra distributed-consensus requirement that a simple shared library cannot provide.

### Maglev hashing

Maglev computes a permutation per backend and fills a fixed prime-sized lookup table. Request lookup is one hash plus one array access. Google's production design used connection tracking as the first protection and a 65,537-entry table by default; larger tables reduce disruption but cost more build time and memory.^9 Envoy exposes Maglev with a default table size of 65,537 and describes its movement as minimal disruption rather than an absolute consistency guarantee.^10

Best fit:

- L4/L7 load balancers with extremely high lookup volume;
- backend set is moderate;
- fixed table memory is acceptable;
- near-perfect table balance matters more than exact ring consistency.

Weak fit:

- many tiny rings;
- arbitrary replication/fallback semantics beyond table variants;
- users expecting only the mathematically necessary keys to move.

Maglev is a strong alternative for a proxy dataplane, but it is not the default for `go-common` because the package needs compact per-ring memory, exact stable-ID ownership and natural `LookupN`.

### CRUSH and topology-aware placement

CRUSH maps objects to storage devices through a cluster hierarchy and placement rules, allowing replicas to span failure domains without a central lookup server.^3

Best fit:

- durable replicated/erasure-coded storage;
- host/rack/zone/device-class constraints;
- very large storage topologies.

Weak fit:

- a general-purpose Go routing helper;
- simple worker affinity;
- a small API that application engineers can reason about locally.

CRUSH is a system design, not a hash-ring utility. Its lesson for `go-common` is to keep topology policy above the core selector.

## Open-source implementation audit

| Project | Structure and concurrency | What it is good at | What not to copy |
|---|---|---|---|
| Google groupcache | sorted `[]int` + `map[int]string`; no lock | canonical small vnode ring | 32-bit CRC, collision overwrite, no remove or concurrency^11 |
| StatHat consistent | 32-bit points/maps with embedded `RWMutex`; 20 replicas | simple cache sharding, `GetN` | reader lock, mutable public replica count, map-heavy storage, allocating `GetN`^12 |
| serialx/hashring | map + sorted interface hash keys; update methods often return a new ring | immutable-style add/remove, weights, multi-node order | MD5 default, interface comparisons, allocations/maps, one in-place update method is not publication-safe^13 |
| go-zero `core/hash` | `RWMutex`, sorted keys, `map[uint64][]any` | weights, 64-bit ring, collision buckets | conversion/interface allocations, reader lock, remove-then-add transition^14 |
| buraksezer/consistent | vnode ring builds fixed partition maps under `RWMutex` | fast primary lookup and bounded partition counts | writer blocks readers, fixed partition contract, panic paths, per-request sorting in closest-N^15 |
| Uber Ringpop | red-black tree + `RWMutex`, SWIM membership, checksums, forwarding | complete application-sharding lifecycle and membership diagnostics | project inactive, 32-bit ring, broad coupling, allocating `LookupN`^1 |
| Meta mcrouter | multiple cache hash schemes, replicated pools, health/failover and online reconfiguration | very high-rate Memcached routing and cache warm-up | FurcHash selects dense pool indexes; full router behavior is far beyond a generic arbitrary-ID selector^28 |
| Grafana dskit | ring descriptor/tokens, `RWMutex`, health/lifecycle/zone-aware replication | large production observability systems needing quorum/failure-domain behavior | too broad as a low-level common primitive; time/health in selection complicates determinism^2 |
| dgryski rendezvous | prehashed node arrays; score all nodes | tiny member sets, low resident memory | `O(N)` lookup, no concurrency/weights/top-K API^6 |
| lithammer Jump | mathematical dense-bucket function | numeric stable buckets and minimal memory | cannot naturally remove arbitrary worker IDs or return replicas^16 |
| wdamron AnchorHash | compact integer arrays and ordered remove/add history | fixed maximum integer bucket universe | requires consistent mutation order; no arbitrary IDs/weights^8 |
| Envoy RingHash | weighted Ketama ring, XXH64 default, bounded ring size | HTTP/gRPC affinity with configurable ring quality | proxy-specific health/locality state; large configured rings can consume significant memory^10 |
| Envoy Maglev | precomputed prime-size table | very high-rate proxy selection | table memory per cluster and approximate, not strict, disruption^10 |
| NGINX ingress | Ketama request-key stickiness and optional subset mode | URI/cookie/header affinity and balancing stickiness against spread | not ownership; unhealthy upstream handling can change result per proxy^17 |
| HAProxy | consistent hashing plus optional concurrent-load bound | sticky request routing protected from a hot first-choice server | runtime load makes selection process-local and non-deterministic across callers^5 |
| Cassandra | token ranges/vnodes plus topology-aware replication and streaming | durable data placement and controlled bootstrap | data movement machinery and token metadata cannot be reduced to a library call^18 |
| Redis Cluster | CRC16 into 16,384 explicit slots; hash tags | transparent client routing and operator-controlled resharding | it is not automatic minimal-movement ownership; slot migration is a distributed protocol^4 |
| Ceph CRUSH | hierarchy/map/rules and computed replica placement | durable topology-aware storage | far beyond application-level affinity^3 |

## Lessons that materially change the design

### Controlled primary-lookup benchmark

An exploratory benchmark used the same 1,024 logical keys and XXH64 on Apple M5 with Go 1.27. Each vnode implementation used 256 points per member; the bounded implementation used 16,381 partitions. Values below are representative medians from five runs with a 300 ms benchmark window.

| Members | groupcache ring | bounded partitions | go-zero ring | Rendezvous | Jump dense | Anchor dense |
|---:|---:|---:|---:|---:|---:|---:|
| 8 | 49 ns / 1 alloc | 12.1 ns / 0 | 74 ns / 2 alloc | 8.35 ns / 0 | 2.35 ns / 0 | 2.33 ns / 0 |
| 32 | 53 ns / 1 alloc | 12.2 ns / 0 | 77 ns / 2 alloc | 29 ns / 0 | 3.3 ns / 0 | 2.47 ns / 0 |
| 128 | 62 ns / 1 alloc | 12.9 ns / 0 | ~101 ns / 2 alloc | 104 ns / 0 | 4.33 ns / 0 | 2.52 ns / 0 |
| 512 | 73 ns / 1 alloc | 13.2 ns / 0 | 110 ns / 2 alloc | 330 ns / 0 | 5.7 ns / 0 | 2.46 ns / 0 |

These are algorithm-shape measurements, not a final league table:

- Jump and Anchor received a prehashed key and returned a dense integer bucket; they did not resolve arbitrary member IDs or replicas.
- groupcache truncates XXH64 to 32 bits and converts the string key to bytes.
- go-zero accepts `any` and performs its representation conversion.
- bounded partitions pays substantial table-build work outside the timed lookup and uses different ownership semantics.
- no candidate performed health, topology, admission or authoritative claims.
- the proposed implementation does not exist yet, so no performance claim is accepted for it.

The evidence supports a two-dimensional decision. Rendezvous is compelling for very small member sets; bounded/precomputed tables dominate raw primary lookup; a compact indexed vnode ring is the strongest general compromise for arbitrary IDs, weights and top-K once membership grows. The final implementation must rerun this harness with identical public semantics and include build time, retained memory, parallel readers, top-K and concurrent replacement.

### Vnode balance simulation

An exact continuum simulation measured ownership interval widths rather than sampling keys. It generated 200 independent 64-member rings for each vnode count and measured coefficient of variation (CV), max/min ownership ratio, and the share captured by a newly added 65th member.

| Vnodes/member | Median CV | p95 CV | Median max/min | p95 max/min | New member p05–p95 | Expected new share |
|---:|---:|---:|---:|---:|---:|---:|
| 32 | 17.4% | 20.2% | 2.28 | 2.81 | 1.10–2.03% | 1.54% |
| 64 | 12.3% | 14.0% | 1.79 | 2.06 | 1.18–1.84% | 1.54% |
| 128 | 8.9% | 10.2% | 1.52 | 1.70 | 1.31–1.77% | 1.54% |
| 160 | 7.8% | 8.9% | 1.44 | 1.57 | 1.35–1.73% | 1.54% |
| 256 | 6.2% | 7.1% | 1.34 | 1.43 | 1.37–1.69% | 1.54% |
| 512 | 4.4% | 5.1% | 1.23 | 1.30 | 1.42–1.64% | 1.54% |

This corrects a common testing mistake: a max/min threshold of 1.30 is not a realistic stable gate for a random ring with 256 vnodes and 64 members. Extreme ratios are sensitive to member count and random seed. CV across many deterministic scenarios plus movement percentiles is a more reliable quality gate.

256 remains the recommended starting default because it costs about 3 KiB of token arrays per unit-weight member while bringing median CV near 6%. 512 roughly halves the variance contribution again but doubles resident token memory and build work. Products requiring a hard per-node load ceiling should not keep increasing vnodes indefinitely; they need bounded admission or a bounded-load algorithm.

### 1. Immutable publication matters more than choosing `RWMutex` versus `sync.Map`

The read workload is many orders of magnitude more frequent than membership change. A lock-free immutable snapshot keeps reader latency independent of build time. `sync.Map` does not help because lookup still needs an ordered successor operation and membership arrays must change coherently.

The right atomic unit is the entire logical membership: member table, token positions, owners, checksum, generation and accelerator. Publishing individual nodes or tokens creates intermediate mappings and makes cross-process debugging difficult.

### 2. Separate points and owners

Binary search only needs token points. A dense `[]uint64` lets the CPU read useful cache lines without loading strings, interfaces, pointers or member values. The owner array is touched once after the index is known. This layout is more cache-friendly than `[]struct{point, member}` and far smaller than a map per vnode.

### 3. Accelerate the search, not the ownership model

A fixed partition table makes primary lookup fast by defining a different mapping. The proposed prefix index instead maps high hash bits to lower-bound ranges in the sorted point array. It can change size on every snapshot without moving any key.

This gets most of the cache-local table benefit while retaining classic ring semantics and compact memory. It should remain an internal optimization verified against a full binary-search oracle.

### 4. `LookupN` is a first-class requirement

Several libraries optimize primary lookup and then implement top-K as an expensive afterthought. For failover and replication, the ring should find the primary once and walk clockwise, skipping repeated owner indexes. The caller supplies the destination buffer, so the hot path needs no map or slice allocation.

Top-K semantics must be explicit: it is an ordered list of distinct candidate members, not proof that those members are healthy, in distinct zones, or permitted to execute the work.

### 5. Static weights and runtime load are different dimensions

Member weight should represent relatively stable capacity. Current inflight count, queue age, free disk and error rate change too quickly to encode as vnode changes. Rebuilding on those signals creates churn and makes different processes disagree.

For request routing, HAProxy's approach—start from deterministic preference and reject overloaded candidates—is the useful pattern.^5 For Judgify, existing database capacity, global limit and per-user limit checks already perform this admission function more safely than a ring.

Three mechanisms are often incorrectly called “weighting”:

1. **Static capacity share:** a member receives proportionally more vnode points. This is deterministic and belongs in the ring.
2. **Bounded partition count:** a rebuild assigns no member more than a configured number of discrete partitions. This is what `buraksezer/consistent` primarily provides.
3. **Bounded live load:** selection skips a currently overloaded first choice. This requires synchronized or local runtime measurements and belongs in admission/routing policy.

Google's bounded-load algorithm was implemented in Cloud Pub/Sub, and Google reports that Vimeo's HAProxy application reduced cache bandwidth by almost a factor of eight.^21 That result supports bounded fallback where hot-key load is the problem; it does not imply that a generic ownership ring should maintain mutable request counters.

### 6. Membership convergence is outside the ring

Two correct rings with different member snapshots produce different answers. Ringpop uses gossip and checksums; dskit uses KV/memberlist state; Redis and Cassandra maintain explicit cluster metadata. A generic selector should expose generation/checksum but should not pretend that this creates consensus.

If exact ownership matters, callers need one ordered, versioned membership source. If the ring is only a preference, temporary divergence is acceptable because the authoritative lease/claim layer resolves conflicts.

### 7. Identity stability is a data-model concern

The token input must survive process restart, address change and deployment. Operational instance IDs remain useful in `Member.Value`, but the placement ID must be logical and stable.

In current Judgify code:

```text
worker_key = prefix + hostname + PID
worker_id  = new UUIDv7
boot_id    = new UUIDv7
```

All three can change across restart or rescheduling. A future ring integration therefore needs a separate `routing_id`, such as a StatefulSet ordinal or provisioned worker-slot ID. Without it, every restart appears as remove-old/add-new and moves approximately twice the necessary key share.

### 8. Hash compatibility and threat model must be explicit

XXH64 is a strong default for trusted routing keys: its specification is portable across CPU width and endianness, it is non-cryptographic, and it is tested for dispersion and collision behavior.^22 Envoy also defaults its ring hash to XXH64.^10 The existing Go dependency has optimized amd64 and arm64 implementations and direct byte/string APIs.^23

Do not use Go `hash/maphash` for cross-process placement. Its `Seed` is intentionally process-local and cannot be serialized or recreated.^24 Do not claim XXH64 protects against an attacker deliberately constructing collisions or hotspots. If arbitrary users can control raw routing keys and hotspot abuse matters, first hash a canonical key with a keyed process-shared function, then pass the resulting 64-bit value to `LookupHash`.

The public v1 algorithm should therefore fix XXH64 and expose `LookupHash`, rather than accepting an arbitrary hasher that silently creates incompatible rings. A future custom-hash option must require an explicit algorithm ID included in checksum/version diagnostics.

### 9. Atomic snapshot publication is RCU-like, not mutable lock-free programming

The builder creates ordinary mutable temporary data, freezes it, and atomically publishes one pointer. Readers never mutate it. Go atomics are sequentially consistent, so observing the stored pointer also observes the completed snapshot initialization.^25 Garbage collection safely retains an old snapshot while an in-flight reader still references it.

This follows the same broad hot-path principle documented by gRPC Go: a balancer generates a new picker from a state snapshot when state changes, and `Pick` should not block or perform time-consuming work.^26 The package should remain simple: one atomic load per operation, one writer mutex to order `Replace`, no hazard pointers, epochs, compare-and-swap loops, or `sync.Map`.

## When to use which algorithm

| Use case | Recommended algorithm/system | Why |
|---|---|---|
| In-process choice among 2–16 changing endpoints | Rendezvous | no vnode memory; `O(N)` is still tiny; simple top-K |
| Shared generic affinity across 10–1,000 stable arbitrary IDs | Proposed vnode ring | bounded lookup, weights, exact movement, natural fallback order |
| Memcached/cache pool with standard Ketama compatibility | Ketama-compatible ring | interoperability is more important than a custom token encoding |
| Ultra-hot proxy routing with tens/hundreds of clusters | Maglev | one array access and highly uniform table |
| Dense append-only integer shards | Jump | minimal memory and excellent simplicity |
| Fixed maximum dense buckets with arbitrary removals and globally ordered updates | AnchorHash | fast minimal-memory lookup under its lifecycle assumptions |
| Persisted database data requiring controlled migration | Explicit slots/partitions | ownership changes must drive a migration protocol |
| Replicas across racks/zones/device classes | CRUSH or topology policy above top-K | failure-domain constraints are the actual requirement |
| Hot-request protection with affinity | preference hash + bounded-load fallback | deterministic first choice, dynamic admission second |
| Judgify's current durable pull/claim loop | PostgreSQL scheduler, no ring gate | compatibility, fairness, capacity and leases already determine safe work |
| Future Judgify cache/artifact affinity | vnode ring or rendezvous after trace analysis | affinity can reduce cache misses without becoming authority |
| Thousands of small rings with moderate members | Rendezvous or multi-probe after benchmark | avoids hundreds of resident vnode records per member |

## Detailed Judgify decision

### Current flow

Each worker:

1. proves the current boot is live;
2. measures local sandbox/disk/queue capacity;
3. asks PostgreSQL for compatible work;
4. PostgreSQL checks worker capability evidence and activated release compatibility;
5. PostgreSQL enforces global and per-user running limits;
6. a candidate row is selected with `FOR UPDATE ... SKIP LOCKED`;
7. capacity is reserved and a secret lease is created;
8. heartbeats renew the lease and fencing stops stale execution.

This is a robust pull scheduler. Adding `hash(job) == thisWorker` to its query would introduce several problems:

- workers may temporarily disagree on membership;
- the preferred worker may be full, unhealthy or incompatible;
- priority/fairness order can be distorted while a job waits for its nominal owner;
- a PID-derived worker key changes on restart;
- querying the Kth hash candidate efficiently inside PostgreSQL is awkward;
- there is no demonstrated state-locality benefit because sandbox runs are isolated.

### Safe ways to use a ring later

1. **Soft compile/artifact affinity:** order compatible workers by a stable artifact or source checksum, then let capacity/leases choose among top-K. First measure whether workers retain reusable state long enough to matter.
2. **Central dispatcher hint:** if a future dispatcher pushes jobs, use ring top-K to choose whom to ask first. The worker must still claim the lease in PostgreSQL before execution.
3. **WebSocket/session affinity:** route a connection or subscription key toward an instance holding ephemeral local state, with reconnect fallback.
4. **Background partition ownership:** assign idempotent scanning ranges to workers, but persist checkpoints and use fencing for side effects.

### Unsafe uses

- treating hash ownership as a lock;
- rejecting a valid database lease because the local ring now prefers another worker;
- using boot ID/PID/address as stable identity;
- rebuilding weights from current queue depth every heartbeat;
- assuming top-K members are in distinct zones;
- moving durable data without a migration state machine.

## Recommended `go-common` scope

### Ship in v1

- `pkg/hashring` with one virtual-node algorithm version;
- immutable snapshot and atomic whole-state replacement;
- `Member[T]` with stable `ID`, immutable caller-owned `Value`, bounded static `Weight`;
- XXH64 byte lookup and prehashed lookup;
- zero-allocation primary and caller-buffer distinct top-K;
- compact point/owner arrays;
- adaptive prefix search index;
- deterministic binary vnode encoding and bounded collision retries;
- generation, checksum and golden compatibility vectors;
- distribution, movement, race, fuzz, allocation and comparative benchmarks.

### Consider after v1 evidence

- `pkg/rendezvous` for many tiny rings or member counts below a measured crossover;
- a topology-aware candidate filter operating on ring output;
- Ketama compatibility mode only when an external ecosystem requires identical mappings;
- serialized snapshot import/export only when startup rebuild cost is demonstrated to matter.

### Do not put in the package

- discovery, gossip, heartbeat or health state;
- leases, locks, fencing or job claims;
- mutable current-load weights;
- retries, RPC forwarding or cache clients;
- rack/zone quorum rules;
- data transfer and reshard orchestration;
- implicit background goroutines.

This boundary keeps the algorithm testable and lets consumers compose the authority model they actually need.

## Proposed usage model

### Cache or sticky routing

```go
ring.Replace(discoverySnapshot)
member, ok := ring.LookupHash(hash.Sum64(cacheKey))
if ok && health.Usable(member.ID) {
    return member.Value
}

var fallback [3]hashring.Member[*Client]
n := ring.LookupNHashInto(hash.Sum64(cacheKey), fallback[:])
return firstHealthy(fallback[:n])
```

The ring determines preference; health determines usability. Rebuild only for meaningful membership/capacity changes, not every request.

### Exclusive work

```go
var preferred [3]hashring.Member[Worker]
n := ring.LookupNHashInto(hash.Sum64(jobID), preferred[:])

for _, worker := range preferred[:n] {
    if !admission.CanTry(worker) {
        continue
    }
    if lease, ok := authority.TryClaim(jobID, worker.ID); ok {
        return dispatch(worker, lease)
    }
}
```

The authority operation is indispensable. If multiple callers disagree on the ring, only one claim succeeds.

## Operational rules

- Canonicalize membership before `Replace`; reject duplicate IDs.
- Log version, generation, member count, total tokens and checksum on publication.
- Debounce noisy discovery updates.
- Alert when peers expected to share membership report different checksums for longer than the convergence window.
- Bound weight and total tokens to prevent configuration-driven memory exhaustion.
- Track lookup/fallback rank and no-eligible-candidate metrics in the caller, not the ring.
- Roll out algorithm-version changes as a coordinated compatibility event.
- Keep a previous snapshot only if the product has an explicit rollback/debug need; otherwise allow GC to reclaim it.

## Final product decision

The proposed immutable vnode ring remains the best first implementation for `go-common`, but its positioning changes:

- It is a **general deterministic affinity and candidate-ordering primitive**.
- It is **not Judgify's scheduler**.
- The adaptive prefix index remains justified because it improves large-ring lookup without introducing fixed-partition semantics.
- Rendezvous is the most credible second engine, but should be added only after benchmarks show real consumers with many small member sets.
- Bounded load belongs in caller admission/fallback logic; topology belongs in a higher placement policy; durable ownership belongs in a lease/fencing authority.

That boundary delivers a fast reusable package without importing the accidental complexity of Ringpop, dskit, Redis Cluster, Cassandra or CRUSH.

## Sources

1. Uber. “[ringpop-go](https://github.com/uber/ringpop-go)” and “[How Ringpop Helps Distribute Your Application](https://www.uber.com/gb/en/blog/ringpop-open-source-nodejs-library/).” Repository notes that the Go project is no longer actively developed.
2. Grafana Labs. “[dskit](https://github.com/grafana/dskit)” and “[ring implementation](https://github.com/grafana/dskit/blob/main/ring/ring.go).” Production use by Mimir, Loki, Tempo and Pyroscope.
3. Ceph. “[CRUSH Maps](https://docs.ceph.com/en/umbrella/rados/operations/crush-map/).” Topology and failure-domain-aware storage placement.
4. Redis. “[Redis Cluster Specification](https://redis.io/docs/latest/operate/oss_and_stack/reference/cluster-spec/).” 16,384 slots, hash tags and slot migration behavior.
5. HAProxy. “[Configuration Manual: hash-balance-factor and consistent hashing](https://docs.haproxy.org/2.0/configuration.html).” Dynamic bounded-load fallback from a consistent first choice.
6. Damian Gryski. “[go-rendezvous](https://github.com/dgryski/go-rendezvous/blob/master/rdv.go).” Go HRW implementation with prehashed node IDs.
7. John Lamping and Eric Veach. “[A Fast, Minimal Memory, Consistent Hash Algorithm](https://arxiv.org/abs/1406.2294).” Jump consistent hash.
8. Mendelson et al. “[AnchorHash: A Scalable Consistent Hash](https://arxiv.org/abs/1812.09674)” and West Damron, “[go-anchorhash](https://github.com/wdamron/go-anchorhash).” Algorithm and Go implementation.
9. Eisenbud et al. “[Maglev: A Fast and Reliable Software Network Load Balancer](https://research.google.com/pubs/archive/44824.pdf).” NSDI 2016.
10. Envoy. “[Load Balancing Architecture](https://www.envoyproxy.io/docs/envoy/latest/intro/arch_overview/upstream/load_balancing/load_balancers.html)” and “[RingHash/Maglev configuration](https://github.com/envoyproxy/envoy/blob/main/api/envoy/config/cluster/v3/cluster.proto).” Current defaults and tradeoffs.
11. Google. “[groupcache consistenthash](https://github.com/golang/groupcache/blob/master/consistenthash/consistenthash.go).” Classic 32-bit sorted ring.
12. StatHat. “[consistent](https://github.com/stathat/consistent/blob/master/consistent.go).” Go ring with `RWMutex` and distinct-node lookup.
13. serialx. “[hashring](https://github.com/serialx/hashring/blob/master/hashring.go).” Weighted immutable-style Go ring.
14. go-zero. “[core/hash consistenthash](https://github.com/zeromicro/go-zero/blob/master/core/hash/consistenthash.go).” Weighted 64-bit ring with collision buckets.
15. Burak Sezer. “[consistent](https://github.com/buraksezer/consistent).” Fixed-partition consistent hashing with bounded loads.
16. Daniel Lithammer. “[go-jump-consistent-hash](https://github.com/lithammer/go-jump-consistent-hash).” Go Jump implementation.
17. Kubernetes ingress-nginx. “[Custom NGINX upstream hashing](https://github.com/kubernetes/ingress-nginx/blob/main/docs/user-guide/nginx-configuration/annotations.md).” Ketama and subset routing modes.
18. Apache Cassandra. “[Production Recommendations: Tokens](https://cassandra.apache.org/doc/stable/cassandra/getting-started/production.html)” and “[Topology Changes](https://cassandra.apache.org/doc/latest/cassandra/managing/operating/topo_changes.html).” Vnode balance and operational overhead.
19. Karger et al. “[Consistent Hashing and Random Trees](https://people.csail.mit.edu/karger/Papers/web.pdf).” Original ring model.
20. Appleton and O’Reilly. “[Multi-Probe Consistent Hashing](https://arxiv.org/abs/1505.00062).” Lower-memory ring balance through multiple key probes.
21. Mirrokni et al. “[Consistent Hashing with Bounded Loads](https://arxiv.org/abs/1608.01350).” Capacity-bounded assignment.
22. Yann Collet. “[XXH32 and XXH64 Specification](https://github.com/Cyan4973/xxHash/blob/dev/doc/xxhash_spec.md).” Portable non-cryptographic digest definition and threat boundary.
23. Caleb Spare. “[cespare/xxhash v2](https://github.com/cespare/xxhash).” Optimized Go XXH64 implementation.
24. Go Authors. “[hash/maphash](https://pkg.go.dev/hash/maphash).” Process-local, non-serializable seed semantics.
25. Go Authors. “[The Go Memory Model](https://go.dev/ref/mem).” Sequential consistency and synchronization guarantees for atomic operations.
26. gRPC Authors. “[grpc-go balancer API](https://github.com/grpc/grpc-go/blob/master/balancer/balancer.go).” Snapshot-generated, non-blocking picker contract.
27. Amazon. “[Dynamo: Amazon’s Highly Available Key-value Store](https://www.amazon.science/publications/dynamo-amazons-highly-available-key-value-store).” Virtual nodes, heterogeneous capacity and failure redistribution.
28. Meta. “[mcrouter](https://github.com/facebook/mcrouter)” and “[Introducing mcrouter](https://engineering.fb.com/2014/09/15/web/introducing-mcrouter-a-memcached-protocol-router-for-scaling-memcached-deployments/).” Cache routing, FurcHash, replicas, failover and online reconfiguration.
