# Password hashing

`password.Hasher` produces Argon2id PHC strings and verifies only the closed
algorithm set `argon2id` and bounded legacy `bcrypt`.

Production defaults:

| Parameter | Value |
|---|---:|
| Memory | 64 MiB |
| Iterations | 3 |
| Parallelism | 2 |
| Salt | 16 bytes |
| Key | 32 bytes |
| Maximum password | 1,024 bytes |
| Maximum encoded hash | 512 bytes |
| Concurrent hashes per `Hasher` | 2 |

Encoded work factors and lengths are validated before base64 decoding or
Argon2 allocation. A valid bcrypt hash is returned with `NeedsRehash=true`;
unknown algorithms and bcrypt costs outside 10–14 are rejected before an
expensive comparison.

The concurrency semaphore bounds memory owned by one `Hasher`. Context
cancellation is honored before admission. Argon2 itself is not interruptible
once started, so callers must also size request admission and shutdown
deadlines around the configured worst-case runtime.

## Initial benchmark

Captured 2026-07-18 on the current single-host development machine:

```text
Go:          1.26.4
OS/arch:     darwin/arm64
CPU:         Apple M5
Policy:      DefaultPolicy (64 MiB, t=3, p=2)
Median:      73.36 ms/hash across five isolated 1x runs
Allocation:  approximately 67.1 MB/op
Max RSS:     90,914,816 bytes in a one-hash process
```

Command:

```bash
go test ./pkg/security/password -run '^$' \
  -bench '^BenchmarkArgon2id$' -benchtime=1x -benchmem
```

Re-run this benchmark on the release host before changing policy. Tune the
memory/concurrency budget together; do not reduce work factors merely to hide
over-admission elsewhere in the service.
