# Concurrent Consistent Hash Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Each implementation task also uses superpowers:test-driven-development.

**Goal:** Ship a deterministic, weighted, virtual-node consistent-hash package with lock-free reads, atomic membership replacement, zero-allocation primary/fallback lookup, and compact memory for `go-common` affinity and candidate-ordering use cases. Judgify integration remains optional and may not replace its PostgreSQL scheduler.

**Architecture:** Build complete immutable snapshots under a writer mutex and publish them through `atomic.Pointer`. Empty and singleton snapshots retain no tokens; multi-member snapshots use a `uint16` or `uint32` owner index with an optional exact-mapping high-bit prefix accelerator. Physical membership replacement shrinks/grows the ring; no implicit goroutine or load-driven vnode changes are included.

**Tech Stack:** Go 1.27, `sync/atomic`, `sort`, `encoding/binary`, existing `github.com/huynhanx03/go-common/pkg/hash` XXH64 helpers, Go unit/property/fuzz/race tests, Go benchmarks, `benchstat` for final comparison.

**Spec:** `docs/superpowers/specs/2026-09-12-concurrent-consistent-hash-design.md` (background comparison: `docs/superpowers/specs/2026-09-12-consistent-hashing-landscape.md`)

## Global Constraints

- Go module: `github.com/huynhanx03/go-common`, Go 1.27.1; reuse the existing `pkg/hash` and XXH64 dependency.
- Routing compatibility depends on algorithm version, canonical encoding, `VirtualNodes`, stable IDs, explicit weights, and collision policy; it must not depend on member input order, generic `Value`, prefix size, or owner-index width.
- Defaults: 256 virtual nodes/weight unit, max weight 64, max logical tokens 1,048,576, max members 65,536 (configurable up to 1,048,576), max ID 256 bytes, max total cloned ID bytes 1,048,576, prefix target eight tokens/bucket, max prefix bits 12, collision attempts eight.
- Empty/singleton retain zero vnode points; validate logical token limits even for singleton. `Replace` failure retains the old snapshot and generation.
- `MaxTotalTokens` and ID/member caps bound the ring's own structure, not generic payload `T`, scratch allocations, old snapshots held by readers, GC timing, or process RSS.
- Ring is a placement-only library: no discovery, implicit goroutine, autoscaler, dynamic vnode-count changes, Forge partition/storage migration, or replacement of Judgify's PostgreSQL ownership.
- Do not touch unrelated user worktree changes. **Do not stage, commit, push, or create a PR at any point.** Deliver a final verification report only.

---

## File map

- Modify `pkg/hash/hash.go`: make `Sum64` compute only the primary XXH64 result.
- Modify `pkg/hash/hash_test.go`: compatibility and allocation tests for primary-only `Sum64`.
- Create `pkg/consistenthash/doc.go`: package contract and concurrency guarantees.
- Create `pkg/consistenthash/types.go`: public `Member`, `Options`, errors, normalized options.
- Create `pkg/consistenthash/ring.go`: `Ring`, constructor, atomic snapshot publication, metadata accessors.
- Create `pkg/consistenthash/build.go`: overflow-safe validation, cloned IDs, deterministic token generation, collision handling, checksum, prefix builder, narrow/wide owner storage.
- Create `pkg/consistenthash/lookup.go`: empty/singleton/16-bit/32-bit primary lookup and caller-buffer distinct fallback lookup.
- Create `pkg/consistenthash/ring_test.go`: API, determinism, movement, weights, concurrency, and error tests.
- Create `pkg/consistenthash/golden_test.go`: cross-process-compatible golden vectors.
- Create `pkg/consistenthash/fuzz_test.go`: malformed membership and lookup invariants.
- Create `pkg/consistenthash/benchmark_test.go`: latency, allocation, scaling, rebuild, and parallel benchmarks.
- Create `pkg/consistenthash/testdata/golden_v1.json`: algorithm-v1 fixed vectors.
- Create `benchmarks/consistenthashcompare/go.mod` and `benchmarks/consistenthashcompare/benchmark_test.go`: isolated upstream comparison module; pin upstream versions here without adding production-module dependencies.
- Modify `README.md`: add the package to the catalog and link its usage documentation.
- Create `docs/consistenthash.md`: consumer guide, scaling/memory contract, Forge integration boundary, Judgify example, compatibility policy.

### Task 1: Freeze the public contract and option validation

- [ ] **Step 1: Write failing constructor and validation tests**

Create `pkg/consistenthash/ring_test.go` with table tests for defaults, invalid limits, duplicate/empty IDs, ID longer than 256 bytes, more than 1,048,576 total ID bytes, excessive member count/weight/logical tokens, multiplication overflow, and empty-ring lookup. A one-member `Replace` exceeding `MaxTotalTokens` must fail even though it would store no points. Verify a long substring ID is cloned and does not retain its input backing string.

```go
func TestNewUsesProductionDefaults(t *testing.T) {
    ring, err := New[string](Options{})
    require.NoError(t, err)
    require.Equal(t, normalizedOptions{
        virtualNodes: 256,
        maxWeight: 64,
        maxTotalTokens: 1 << 20,
        maxMembers: 1 << 16,
        maxMemberIDBytes: 256,
        maxTotalIDBytes: 1 << 20,
        prefixTarget: 8,
        maxPrefixBits: 12,
        collisionAttempts: 8,
    }, ring.opts)
}

func TestReplaceRejectsDuplicateStableIDs(t *testing.T) {
    ring, _ := New[string](Options{})
    err := ring.Replace([]Member[string]{{ID: "worker-a"}, {ID: "worker-a"}})
    require.ErrorIs(t, err, ErrDuplicateMember)
}

func TestSingletonEnforcesLogicalTokenBudget(t *testing.T) {
    ring, err := New[string](Options{VirtualNodes: 256, MaxTotalTokens: 128})
    require.NoError(t, err)
    require.ErrorIs(t, ring.Replace([]Member[string]{{ID: "a"}}), ErrTooManyTokens)
    require.Equal(t, uint64(0), ring.Generation())
}
```

- [ ] **Step 2: Run the package test and confirm the red state**

Run: `go test ./pkg/consistenthash -run 'Test(New|Replace|Lookup)'`

Expected: FAIL because the package and public types do not exist.

- [ ] **Step 3: Implement the smallest public model**

Add `types.go`, `ring.go`, and `doc.go` with:

```go
type Member[T any] struct {
    ID string
    Value T
    Weight uint16
}

type Options struct {
    VirtualNodes uint32
    MaxWeight uint16
    MaxTotalTokens uint32
    MaxMembers uint32
    MaxMemberIDBytes uint32
    MaxTotalIDBytes uint32
    PrefixTarget uint32
    MaxPrefixBits uint8
    CollisionAttempts uint8
}

var (
    ErrInvalidOptions = errors.New("consistenthash: invalid options")
    ErrInvalidMember = errors.New("consistenthash: invalid member")
    ErrDuplicateMember = errors.New("consistenthash: duplicate member")
    ErrTooManyTokens = errors.New("consistenthash: too many tokens")
    ErrMemberLimit = errors.New("consistenthash: too many members")
    ErrIDLimit = errors.New("consistenthash: member ID limit exceeded")
    ErrTokenCollision = errors.New("consistenthash: token collision")
)
```

Normalize zero values to the specified defaults. Treat `Member.Weight == 0` as weight 1. Validate option ceilings, byte lengths and `uint64`-checked products/sums before allocating a point slice. Clone IDs with `strings.Clone` when publishing; a copied string header alone can retain an oversized caller buffer. Return wrapped sentinel errors containing the member ID or option name.

- [ ] **Step 4: Run focused tests**

Run: `go test ./pkg/consistenthash -run 'Test(New|Replace|Lookup)'`

Expected: PASS.


### Task 2: Build deterministic compact vnode snapshots

- [ ] **Step 1: Add failing determinism and structure tests**

Test that permutations of identical multi-member membership produce identical points, logical owner indexes, checksum, and results. Assert points are strictly increasing, owner indexes are valid, member order is sorted by ID, and expected logical token count equals `VirtualNodes * sum(effectiveWeight)`.

```go
func TestReplaceIsIndependentOfInputOrder(t *testing.T) {
    left := newTestRing(t)
    right := newTestRing(t)
    require.NoError(t, left.Replace(testMembers("c", "a", "b")))
    require.NoError(t, right.Replace(testMembers("b", "c", "a")))
    require.Equal(t, left.Checksum(), right.Checksum())
    require.Equal(t, left.state.Load().points, right.state.Load().points)
    require.Equal(t, left.state.Load().owners16, right.state.Load().owners16)
    require.Equal(t, left.state.Load().owners32, right.state.Load().owners32)
}
```

- [ ] **Step 2: Run and verify failure**

Run: `go test ./pkg/consistenthash -run 'TestReplace(IsIndependent|Builds|Sorts)'`

Expected: FAIL because snapshot construction is not implemented.

- [ ] **Step 3: Implement canonical token generation in `build.go`**

Use a reusable byte buffer and little-endian fields:

```text
"go-common/consistenthash" | algorithmVersion | idLength | id | vnodeOrdinal | collisionAttempt
```

Hash with the repository's XXH64 primary-only path. `pkg/hash.Sum64` must calculate the primary result directly rather than delegating to `Sum128`, because computing the unused secondary hash doubles hot-path work. Sort copied members by ID before generating points. For zero members publish no points. For singleton membership, derive logical tokens and collision resolution for the checksum but publish no retained point, owner, or prefix arrays. For multiple members, track occupied points only in a builder-local `map[uint64]struct{}`; retry a collision deterministically up to `CollisionAttempts`, returning `ErrTokenCollision` without publishing if exhausted. Sort temporary `{point, owner}` records and compact to exact-capacity `[]uint64` plus exactly one of `[]uint16` for up to 65,536 members or `[]uint32` above that. Dispatch once to the narrow/wide scanning path so there is no per-token owner-width branch. Do not retain the builder map.

- [ ] **Step 4: Implement algorithm-v1 checksum**

Hash canonical algorithm version, `VirtualNodes`, `CollisionAttempts`, member IDs/effective weights and final *logical* point-owner records for nonempty rings, including a singleton whose points are not retained. Empty rings have no point records. Exclude `Value`, generation, owner-index representation, prefix configuration and resource-budget options (`MaxMembers`, ID limits, `MaxWeight`, `MaxTotalTokens`); unit-test checksum equality when only these nonmapping options/payload/layout change. Add a package constant for the algorithm version. Unit-test that compact singleton and full-token reference checksums agree.

- [ ] **Step 5: Run tests**

Run: `go test ./pkg/consistenthash -run 'TestReplace(IsIndependent|Builds|Sorts)'`

Expected: PASS.


### Task 3: Implement lock-free exact primary lookup

- [ ] **Step 1: Add a failing primary-hash regression benchmark/test**

In `pkg/hash/hash_test.go`, verify `Sum64` remains byte-for-byte equal to `Sum128(...).Primary` for every supported key type. Add `BenchmarkSum64Bytes`, `BenchmarkSum64String`, and allocation checks so the ring does not inherit unnecessary secondary-hash work.

- [ ] **Step 2: Change `pkg/hash.Sum64` to calculate only the primary hash**

Use the same canonical numeric encodings and call `xxhash.Sum64` / `xxhash.Sum64String` directly. Do not implement `Sum64` by delegating to `Sum128`. Run: `go test ./pkg/hash -run 'TestSum(64|128)'` and `go test ./pkg/hash -run '^$' -bench BenchmarkSum64 -benchmem -count=5`.

Expected: compatibility PASS and both string/byte paths report zero allocations.

- [ ] **Step 3: Add failing lookup boundary tests**

Test empty state, singleton with no stored points (including `LookupHash(^uint64(0))`), exact point, between points, after the final point wrapping to zero, byte-key hashing, and prehashed lookup equivalence. Test 65,536 members use `owners16` and 65,537 members use `owners32` using `VirtualNodes: 1`, `MaxMembers: 65_537`, and sequential numeric IDs; verify both return valid owners without allocation.

```go
func TestLookupHashWrapsClockwise(t *testing.T) {
    ring := ringWithSnapshot([]uint64{10, 20, 30}, []uint32{0, 1, 2})
    got, ok := ring.LookupHash(31)
    require.True(t, ok)
    require.Equal(t, "a", got.ID)
}
```

- [ ] **Step 4: Run and verify failure**

Run: `go test ./pkg/consistenthash -run 'TestLookup'`

Expected: FAIL on unimplemented lookup.

- [ ] **Step 5: Implement one-snapshot lookup in `lookup.go`**

Load `state` exactly once. Return `(zero, false)` for no members and the sole `members[0]` for a singleton, before touching `points`. For multi-member rings, use a private hand-written lower-bound loop over `points` so the hot path has no closure and can later accept prefix bounds. Wrap at length, branch once per lookup to read `owners16[index]` or `owners32[index]`, and return the member by value. `Lookup(key)` delegates to `LookupHash(hash.Sum64(key))` without converting to string or interface.

- [ ] **Step 6: Add allocation assertions**

Use `testing.AllocsPerRun(10_000, ...)` and require zero allocations for `LookupHash` and `Lookup([]byte)` after setup for singleton, narrow, and wide layouts.

- [ ] **Step 7: Run tests and baseline benchmark**

Run: `go test ./pkg/consistenthash -run 'TestLookup'`

Run: `go test ./pkg/consistenthash -run '^$' -bench 'BenchmarkLookup(Hash)?$' -benchmem -count=5`

Expected: tests PASS and both benchmarks report `0 B/op, 0 allocs/op`.


### Task 4: Add the adaptive prefix accelerator without changing mapping

- [ ] **Step 1: Add failing equivalence and boundary tests**

For generated and hand-built rings, compare accelerated lookup against full-slice `sort.Search` for exact points, bucket edges, empty buckets, max `uint64`, and at least one million deterministic hashes. Verify snapshots can choose different prefix sizes while returning identical owners.

```go
func TestPrefixIndexPreservesExactRingMapping(t *testing.T) {
    snap := buildLargeSnapshot(t)
    for i := uint64(0); i < 1_000_000; i++ {
        sum := hash.Sum64(i)
        require.Equal(t, fullSearch(snap.points, sum), snap.search(sum))
    }
}
```

- [ ] **Step 2: Run and verify failure**

Run: `go test ./pkg/consistenthash -run 'TestPrefix'`

Expected: FAIL because prefix construction/search does not exist.

- [ ] **Step 3: Implement prefix sizing and construction**

Choose bits so average tokens per indexed bucket remains near `PrefixTarget`, never exceeding `MaxPrefixBits`. Skip the prefix array when it would not reduce the search range enough. Build `2^bits + 1` lower-bound offsets into `points`; verify all offsets are monotonic and the final offset equals `len(points)`.

- [ ] **Step 4: Route lookup through prefix bounds**

Search `points[lo:hi]` with the same private lower-bound primitive. If no point in the bucket is at or after the hash, use `hi`; wrap to zero at the end. Keep a full-slice oracle only in tests.

- [ ] **Step 5: Prove semantic and performance behavior**

Run: `go test ./pkg/consistenthash -run 'TestPrefix'`

Run: `go test ./pkg/consistenthash -run '^$' -bench 'BenchmarkLookupHash/(128|512)-members' -benchmem -count=10 > /tmp/consistenthash-prefix.txt`

Expected: equivalence tests PASS, zero allocations remain, and the prefix path improves large-ring median lookup. If it does not improve the median by at least 10%, keep the code but raise the activation threshold so that unhelpful snapshots use full search.


### Task 5: Implement zero-allocation distinct fallback lookup

- [ ] **Step 1: Add failing `LookupNInto` tests**

Cover primary-first ordering, clockwise order, duplicate-vnode suppression, wraparound, zero-length destination, destination larger than membership, one-member ring, and equivalence between byte and prehashed entry points.

```go
func TestLookupNIntoReturnsDistinctMembers(t *testing.T) {
    ring := configuredRing(t, 5)
    dst := make([]Member[string], 3)
    n := ring.LookupNHashInto(hash.Sum64("job-42"), dst)
    require.Equal(t, 3, n)
    require.NotEqual(t, dst[0].ID, dst[1].ID)
    require.NotEqual(t, dst[0].ID, dst[2].ID)
    require.NotEqual(t, dst[1].ID, dst[2].ID)
}

func TestSingletonFallbackHasOneOwner(t *testing.T) {
    ring, err := New[string](Options{})
    require.NoError(t, err)
    require.NoError(t, ring.Replace([]Member[string]{{ID: "only"}}))
    var dst [3]Member[string]
    require.Equal(t, 1, ring.LookupNHashInto(42, dst[:]))
    require.Equal(t, "only", dst[0].ID)
    require.Empty(t, ring.state.Load().points)
}
```

- [ ] **Step 2: Run and verify failure**

Run: `go test ./pkg/consistenthash -run 'TestLookupN'`

Expected: FAIL on missing methods.

- [ ] **Step 3: Implement clockwise fallback scanning**

Return zero for empty ring/destination and exactly one for a singleton with nonempty destination. Otherwise find the primary token once, walk at most `len(points)` entries with wraparound, and compare each candidate ID against already-written `dst[:n]`. Dispatch once to narrow or wide owner scanning; do not allocate a seen map: replication factors are normally 2–5, making the small linear scan the better hot-path tradeoff.

- [ ] **Step 4: Add and run allocation/scale benchmarks**

Run: `go test ./pkg/consistenthash -run 'TestLookupN'`

Run: `go test ./pkg/consistenthash -run '^$' -bench 'BenchmarkLookupNInto' -benchmem -count=5`

Expected: PASS and `N=1`, `N=3`, and `N=5` all report zero allocations.


### Task 6: Make replacement atomic and race-safe

- [ ] **Step 1: Add failing publication and concurrency tests**

Test generation increments only after successful replacement, failed replacement retains the old checksum/mapping, input slices can be mutated after return without affecting state, substring IDs are cloned before publication, and readers see only old or new complete checksums. Exercise empty→singleton→two-member→singleton→empty transitions; after the singleton replacement, verify resident token/index lengths are all zero. Start multiple lookup goroutines while repeatedly replacing two valid memberships.

```go
func TestFailedReplaceKeepsPublishedSnapshot(t *testing.T) {
    ring := configuredRing(t, 3)
    before := ring.Checksum()
    err := ring.Replace([]Member[string]{{ID: "duplicate"}, {ID: "duplicate"}})
    require.Error(t, err)
    require.Equal(t, before, ring.Checksum())
}
```

- [ ] **Step 2: Run the race detector and observe failure before final synchronization**

Run: `go test -race ./pkg/consistenthash -run 'Test(Concurrent|FailedReplace|InputOwnership)'`

Expected: FAIL until writer serialization, copying, and atomic publication are complete.

- [ ] **Step 3: Complete writer and metadata semantics**

Hold `writeMu` from validation through build and publish. Derive generation from the currently published snapshot, publish once, and expose metadata by a single atomic load. Never mutate a published slice. Clone IDs; document that referenced state inside generic `Value` must be caller-immutable. Old snapshots are reclaimed only after all readers release them and GC runs: `Replace` must not zero or reuse old arrays.

- [ ] **Step 4: Run race and parallel benchmark suites**

Run: `go test -race ./pkg/consistenthash`

Run: `go test ./pkg/consistenthash -run '^$' -bench 'BenchmarkParallel(Lookup|LookupDuringReplace)' -benchmem -count=5`

Expected: race PASS; lookups stay zero-allocation and continue during a slow replacement.


### Task 7: Pin compatibility with collision tests and golden vectors

- [ ] **Step 1: Introduce a private test hash seam**

Keep the public algorithm fixed to repository XXH64, but let package-internal tests inject deterministic low-entropy and constant hash functions into the builder.

- [ ] **Step 2: Add failing collision tests**

Verify low-entropy collisions resolve identically under input permutations. Verify a constant hash returns `ErrTokenCollision` within the configured attempt limit and leaves the previous snapshot untouched.

- [ ] **Step 3: Add algorithm compatibility golden fixtures**

Create `testdata/golden_v1.json` (the `1` is an internal mapping-format identifier, not a product release stage) containing normalized options, sorted IDs/weights, checksum, and lookup results for empty, singleton, multi-member, boundary, and representative keys. The test must fail loudly if encoding or ownership changes without an intentional algorithm-version bump and fixture review.

- [ ] **Step 4: Run compatibility tests**

Run: `go test ./pkg/consistenthash -run 'Test(Collision|Golden|Permutation)'`

Expected: PASS.


### Task 8: Prove distribution, weight, and minimal movement

- [ ] **Step 1: Add deterministic statistical tests**

Measure exact continuum ownership intervals across 200 deterministic 64-member scenarios at 256 vnodes. Require p95 coefficient of variation ≤ 8% and p95 max/min ratio ≤ 1.50. Repeat sampled-key tests with weights 1:2:4 and require observed shares within 12% relative error of expected shares.

- [ ] **Step 2: Add movement invariants**

For 64→65 members across the same 200 collision-free scenarios, assert every moved key lands on the new member and the 5th–95th percentile new share remains within 12% of `1/65`. For removal, assert only keys previously owned by the removed member move; also test 1→2 and 2→1 transitions and compare with the full-token reference ring so singleton compaction never alters mapping. Collision-specific behavior is covered by Task 7, not the strict no-extra-movement assertion. Keep sampled lookup tests as an independent check of the exact interval calculation.

- [ ] **Step 3: Verify vnode default against alternatives**

Benchmark/test 64, 128, 256, and 512 vnodes. Keep 256 as the default only if it satisfies the distribution gate and has materially lower build/memory cost than 512. If 128 also satisfies every distribution and movement gate, change the default to 128 and regenerate golden vectors before release. Once chosen, a vnode count is a compatibility-affecting setting; do not retune it based on cluster size, traffic, or a background sweep.

- [ ] **Step 4: Run statistical suite repeatedly**

Run: `go test ./pkg/consistenthash -run 'Test(Distribution|Weighted|Movement)' -count=10`

Expected: PASS without flakes because key/member fixtures are deterministic.


### Task 9: Fuzz malformed membership and ring invariants

- [ ] **Step 1: Add fuzz targets in `fuzz_test.go`**

Fuzz member IDs, weights, input order, keys, and destination length. Invariants: no panic/hang, valid successful owner, deterministic permutation result, `LookupNInto` distinct IDs, and unsuccessful `Replace` preserves the old checksum.

- [ ] **Step 2: Seed important edge cases**

Include empty and very long IDs, embedded NUL bytes, Unicode, max weights, repeated IDs, empty keys, all-zero bytes, and max `uint64` prehash.

- [ ] **Step 3: Run bounded fuzz and race tests**

Run: `go test ./pkg/consistenthash -fuzz FuzzReplace -fuzztime=30s`

Run: `go test ./pkg/consistenthash -fuzz FuzzLookupN -fuzztime=30s`

Run: `go test -race ./pkg/consistenthash`

Expected: no panic, timeout, race, or invariant violation.


### Task 10: Run fair upstream benchmarks and enforce memory gates

- [ ] **Step 1: Add complete benchmark matrix**

Benchmark empty/singleton/8/32/128/512 equal members, 64/256/512 vnodes, both 16/32-bit owner layouts, `LookupHash`, byte lookup, `LookupNInto` for 3 and 5, serial and parallel lookup, replacement, and lookup during replacement. Add a retained-size estimator test for structural arrays: `8*len(points) + 2*len(owners16) + 4*len(owners32) + 4*len(prefix)`, excluding member table and allocator overhead. Assert empty/singleton retain zero point/index bytes. Benchmark allocation volume per `Replace` (`B/op`) separately from observed peak live heap (`HeapAlloc`) under repeated replacements with a reader holding an old snapshot; label the latter diagnostic, not a strict RSS guarantee.

- [ ] **Step 2: Build a separate comparison harness**

Under `benchmarks/consistenthashcompare`, create a nested module with a local `replace github.com/huynhanx03/go-common => ../..`. Pin exact groupcache, `buraksezer/consistent`, and go-zero versions in that module's `go.mod`/`go.sum`; do not add them to the production module. Give every implementation the same precomputed 64-bit key corpus, member IDs and member count; use XXH64 where its API permits. Run primary lookup separately from top-K and label `buraksezer`'s fixed-partition lookup as a different algorithmic contract, not a like-for-like ring winner. Record exact dependency commits, Go version, CPU, token/partition configuration and commands. Do not compare upstream README numbers as if they were equivalent workloads.

- [ ] **Step 3: Run stable benchmarks**

```bash
go test ./pkg/consistenthash -run '^$' -bench . -benchmem -count=10 > /tmp/consistenthash-new.txt
(cd benchmarks/consistenthashcompare && go test -run '^$' -bench . -benchmem -count=10) > /tmp/consistenthash-upstreams.txt
benchstat /tmp/consistenthash-new.txt
benchstat /tmp/consistenthash-upstreams.txt
```

Expected gates:

- hot lookups: `0 B/op, 0 allocs/op`;
- multi-member point/index memory: no more than `(8 + ownerWidth) * storedTokens + 4 * (2^prefixBits + 1)` bytes, with `ownerWidth=2` at ≤65,536 members and `4` above; separate member/ID memory and transient build allocation measurements, never label either a hard process-RSS cap;
- compare `uint16` and `uint32` lookup at the same member set and report the trade-off; if narrow storage causes an unacceptable regression under representative loads, revise the layout/dispatch and rerun rather than assuming it wins;
- no lookup latency cliff as membership scales;
- prefix acceleration improves large rings or auto-disables;
- primary lookup materially outperforms the lock/map/representation go-zero path under equal hashing;
- reader latency is not coupled to replacement duration as it is with `RWMutex` writers.

- [ ] **Step 4: Save reviewed results**

Create `docs/benchmarks/consistenthash-2026-09.md` with environment, exact commands, tables, interpretation, and raw-output links or fenced appendices. If a gate fails, optimize from profiler evidence and rerun; do not waive the gate silently.


### Task 11: Document usage and Judgify integration

- [ ] **Step 1: Write `docs/consistenthash.md`**

Include stable-ID rules, static weight units, deterministic compatibility, `owners16`/`owners32` and singleton memory formulas, logical token/member/ID-byte caps, transient rebuild allocations versus resident memory, and why generic `T`/GC prevents a strict RSS promise. Explain shrinking by authoritative `Replace`, no implicit goroutine/autoscaler or dynamic vnode count, concurrency/error semantics, and when HRW/Jump/fixed partitions are a better fit.

- [ ] **Step 2: Add a compile-tested Judgify example**

```go
type Worker struct {
    RoutingID string // provisioned logical identity; not PID/boot/address
    BootID string
    Address string
}

ring, _ := consistenthash.New[Worker](consistenthash.Options{})
_ = ring.Replace(discoveredWorkers) // ID must be persistent Worker.RoutingID

var candidates [3]consistenthash.Member[Worker]
n := ring.LookupNHashInto(hash.Sum64(job.ID), candidates[:])
for _, candidate := range candidates[:n] {
    // Future push/affinity path only: apply capability/health policy,
    // then claim in PostgreSQL with lease + fencing.
}
```

State explicitly that the current durable worker pull/claim path should not be gated by the ring. The database claim is authoritative and hashing is only preferred routing for a future evidence-backed affinity or dispatcher path. For a future partitioned Forge, document stable `key -> partition ID`, ring-planned `partition ID -> worker`, materialized direct owner table, and the need for separate offset/lease/fencing/data-handoff work; no Forge code is modified in this plan.

- [ ] **Step 3: Update root README catalog and run doc checks**

Run: `go test ./pkg/consistenthash`

Run: `go vet ./pkg/consistenthash`

Run: `go test ./...`

Expected: package and repository checks PASS; all public identifiers have useful Go documentation.


### Task 12: Final production verification and review

- [ ] **Step 1: Run formatting and static verification**

```bash
gofmt -w pkg/consistenthash
go vet ./pkg/consistenthash
go test ./pkg/consistenthash -count=10
go test -race ./pkg/consistenthash
```

Expected: all commands PASS.

- [ ] **Step 2: Run the repository suite**

Run: `go test ./...`

Expected: PASS. If unrelated pre-existing failures exist, capture exact commands/output and prove `pkg/consistenthash` remains green independently.

- [ ] **Step 3: Run final benchmarks and inspect generated code**

Run: `go test ./pkg/consistenthash -run '^$' -bench . -benchmem -count=10`

Run: `go test -gcflags='-m=2' ./pkg/consistenthash 2>&1 | grep -E 'Lookup|escapes'`

Expected: steady-state lookup remains allocation-free; no accidental interface/string conversion appears in hot methods.

- [ ] **Step 4: Request independent review**

Review specifically for concurrency linearizability, deterministic encoding, collision termination, integer overflow, actual owner-width dispatch, singleton mapping and 1↔2 transitions, member/ID limits, honest resident-vs-peak memory claims, golden compatibility, benchmark fairness, and misuse of hashing as Judgify/Forge ownership authority.

- [ ] **Step 5: Apply verified findings and rerun every gate**

Use `superpowers:receiving-code-review` before changing code from review feedback, then repeat Steps 1–3.

- [ ] **Step 6: Report verified results without committing**

Use `superpowers:verification-before-completion`, then report the exact commands, outcomes, benchmark environment, remaining risks, and changed files. Do not stage or commit anything. Do not claim production readiness from unit tests alone; include race, statistical, fuzz, compatibility, memory, and benchmark evidence.
