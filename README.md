# go-common

`go-common` is a production-oriented Go library of reusable infrastructure
primitives. It provides bounded, context-aware building blocks for security,
correlation, lifecycle, health, transports, persistence integration,
concurrency, and messaging while leaving business policy to each consuming
application.

The module is intentionally domain-neutral. It does not define application
entities, event schemas, worker names, authorization vocabulary, database
tables, or service orchestration.

## Install

Release consumers should depend on a semantic tag:

```text
go get github.com/huynhanx03/go-common@v1
```

Import only the focused packages a component needs. Local multi-module
development can use `go.work`; a published module must not depend on a
filesystem `replace` outside this repository.

## Library boundaries

The public surface is split by capability rather than by one application's
architecture:

| Capability | Packages |
|---|---|
| Process foundation | `pkg/common/lifecycle`, `pkg/common/health`, `pkg/common/observability`, `pkg/logger`, `pkg/settings`, `pkg/correlation` |
| Security | `pkg/security/authentication`, `pkg/security/authorization`, `pkg/security/password`, `pkg/oauth` |
| Transports | `pkg/common/http/...`, `pkg/common/grpc/...`, `pkg/common/websocket` |
| Data boundaries | `pkg/common/tx`, `pkg/database/...`, `pkg/cdc` |
| Messaging | `pkg/mq/forge`, `pkg/mq/outbox`, `pkg/mq/batcher`, `pkg/mq/kafka` |
| Reusable core | `pkg/encoding/...`, `pkg/dto`, `pkg/unique`, `pkg/datastructs/...`, `pkg/pool/...`, `pkg/consistenthash` |

Application repositories remain responsible for:

- business models, use cases, schemas, routes, and event names;
- concrete authorization resources/actions and administration workflows;
- application-specific persistence adapters and transaction composition;
- workers, process wiring, configuration values, rollout policy, and capacity
  choices.

Shared packages own generic contracts, validation, bounds, concurrency,
failure classification, and lifecycle behavior: mechanisms, not application
policy. Integrations cross the
boundary through package-owned interfaces and neutral metadata.

## Design contract

Production-facing packages follow the same rules:

- accept `context.Context` for bounded or cancelable work;
- expose stable sentinels or typed errors instead of requiring error-string
  matching;
- declare goroutine, buffer, data ownership, and shutdown semantics;
- reject unsafe or unbounded input at trust boundaries;
- preserve correlation metadata without treating it as identity;
- keep secrets and opaque payloads out of logs;
- avoid hidden global initialization and application imports.

## Documentation

- [v0 to v1 migration](docs/migration-v0-to-v1.md)
- [Security model](docs/security.md)
- [Lifecycle and health](docs/lifecycle.md)
- [Observability primitives](docs/observability.md)
- [gRPC server](docs/grpc.md)
- [WebSocket operations](docs/websocket.md)
- [Durable outbox relay](docs/outbox.md)
- [Benchmark operations](docs/benchmarks.md)
- [Consistent-hash placement](docs/consistenthash.md)
- [Forge embedded queue](pkg/mq/forge/README.md)

## Repository layout

```text
go-common/
├── pkg/       # Public capability packages
├── internal/  # Reserved for future private shared implementation
├── cmd/       # Reserved for future executable commands
├── docs/      # Compatibility and operating contracts
├── scripts/   # Deterministic local/CI entry points
└── .github/   # Continuous-integration workflows
```

## Verification

The default verification path is deterministic and does not require external
services:

```text
make verify
make coverage
make test-race
```

`make verify` checks formatting, vet, module integrity, workflow syntax, unit
behavior, fuzz seed corpora, and documentation examples.

Container-backed database and broker checks are opt-in so a single development
machine can run the ordinary suite predictably:

```text
make test-integration
```

Release automation also runs reachable-vulnerability analysis and captures a
repeatable benchmark artifact. Performance results are evidence for regression
review, not a cross-machine service-level guarantee.
