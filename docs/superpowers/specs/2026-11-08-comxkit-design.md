# comxkit

Status: approved for planning on 2026-11-08 after the user selected Cobra and requested the implementation plan. Implementation and dependency installation require a subsequent implementation request. Do not commit.

## Purpose

Provide an installable CLI inside go-common that creates an understandable Go project and adds domain/resource scaffolds. A developer unfamiliar with go-common can see available infrastructure choices, create a working server, and start writing application behavior without assembling infrastructure manually.

The CLI is named `comxkit`. The first supported stack is Gin + Ent + PostgreSQL. Later implementations extend the choices shown by the same CLI. Generated applications import go-common infrastructure packages and own their application code.

## Agreed requirements

- Install through `go install github.com/huynhanx03/go-common/cmd/comxkit@<tag>`; `@latest` is available for ordinary installation after release.
- `comxkit init` guides a new user and visibly lists HTTP, ORM and database choices, even while each category has only one implementation.
- Flags support unattended use with the same choices and validation as interactive mode.
- Separate commands add domains and resource CRUD scaffolds to existing generated projects.
- Operation names are `Find`, `Get`, `Create`, `Update`, `Upsert`, `Delete`, with the `Bulk` suffix for supported bulk operations.
- Starter Ent schemas contain shared mixins, not invented business fields or a separate fields DSL.
- Reuse `UUIDMixin` (UUIDv7), `TimeMixin`, and optional existing `ModifierMixin` / `SoftDeleteMixin`. Do not invent parallel ID/timestamp implementations.
- Data models and DTOs remain plain data. Validation and application decisions belong in services.
- Keep implementation and generated projects readable, with constructor DI and limited abstraction.
- Use Cobra for the command layer, as requested after reviewing CLI alternatives.

## Approaches considered

1. Copy a standalone project template: easy to begin, but copied infrastructure implementations diverge across projects.
2. Interpret all CRUD operations through a generic runtime framework: concise for simple entities, but domain differences require a growing callback/options API.
3. Generate concrete application code that calls go-common: application behavior stays editable, while infrastructure changes remain concentrated in the library.

The discussion selected approach 3. The generator creates ordinary source files; applications do not need comxkit at runtime.

## Proposed implementation decisions

The details below are design proposals, rather than additional requirements already agreed by the user.

### CLI presentation

Use Cobra for command/flag/help/completion handling and Huh for interactive forms. Construct commands with injected dependencies, use `RunE` and command contexts, and avoid package-global command variables or `init()` registration. Do not layer a custom command framework over Cobra. Both remain private CLI dependencies; released versions will be verified and pinned during implementation. Do not install or upgrade dependencies while reviewing this spec.

This favors an established command tree and an existing forms implementation, rather than the minimum possible binary size. Huh itself depends on Bubble Tea, Bubbles, Lip Gloss and other terminal packages; one directly imported library does not mean one total module. Do not claim the selected combination is the lightest without comparable measurements. Use Huh's built-in form/theme/accessibility features instead of adding another presentation framework or writing terminal redraw/navigation code. Generated applications do not import Cobra, Huh or the CLI implementation, although the parent go-common module still declares their module requirements.

Official references:

- https://github.com/spf13/cobra
- https://github.com/charmbracelet/huh
- https://github.com/charmbracelet/huh/blob/main/go.mod

Research comparison (2026-11-08):

| Alternative | Relevant capability | Decision for comxkit |
| --- | --- | --- |
| Cobra | Nested commands, flag inheritance, help, suggestions and shell completion | Selected command layer; fits future command growth and the user's explicit preference. |
| urfave/cli/v3 | Declarative commands/completion; documents zero external runtime dependencies | Strong alternative if runtime dependency count becomes the priority; no demonstrated need to replace the selected Cobra. |
| Kong | Commands defined through Go struct types/tags | Valid compact alternative; not selected over the requested explicit Cobra command tree. |
| Huh | Grouped forms, selection/multiselection, dynamic options, validation, accessibility | Selected interactive layer; directly fits stack selection and operation selection. |
| Promptui | Input and searchable select prompts | Narrower scope and a small declared dependency set, but would need more application orchestration for this wizard. Not a demonstrated binary-size winner. |
| PTerm | Broad terminal printers, interactive prompts, tables and progress displays | Valid presentation alternative, but its broader component set is not required by the planned forms. No proven size benefit. |
| Promptkit | Customizable terminal prompts | README marks its API unstable before v1; no clear benefit over selected Huh. |
| Survey | Interactive prompts | Repository archived in 2024; not a new dependency choice. |
| Fang | Styled help/errors and convenience features layered on Cobra | Not a Cobra replacement; README labels it experimental. Do not add this extra layer initially. |

Additional primary references:

- https://github.com/urfave/cli/blob/main/README.md
- https://cli.urfave.org/v3/binary-size/
- https://github.com/alecthomas/kong
- https://github.com/manifoldco/promptui
- https://github.com/pterm/pterm
- https://github.com/erikgeiser/promptkit
- https://github.com/AlecAivazis/survey
- https://github.com/charmbracelet/fang

Dependency counts in go.mod include test/tool modules and are not equivalent to runtime imports, binary bytes or startup latency. During implementation, inspect the selected dependency graph and record the release binary size using a fixed Go version, target platform and flags. Do not invent cross-library performance rankings from README claims. Existing generator use of text/template must also be considered before adopting any parser-specific template-removal optimization.

Appearance:

- Compact `comxkit` wordmark, no large ASCII banner.
- Terminal-native foreground/background; one adaptive accent, muted descriptions, green success and red errors.
- Do not convey state through color alone. Use text labels and focus markers.
- One logical step per form group; show progress and the selected stack before writing.
- Stack steps are Project, HTTP, ORM, Database, Review. Category headers and available choices remain visible even with one option.
- Arrow keys to move, Enter to choose, Escape to return, Ctrl+C to cancel.
- Narrow terminals wrap descriptions; plain/accessibility mode uses sequential text prompts without cursor redraws.
- Respect `NO_COLOR`, non-TTY output and `TERM=dumb`. Non-TTY input never starts a form or hangs waiting for input.
- Output concrete failures and actionable next steps; suppress stack traces for ordinary CLI errors.
- Prompts/progress/errors go to stderr; stdout contains the final result so redirected output remains useful.

Illustrative interactive screen, not an exact byte-for-byte contract:

```text
comxkit                              2 / 5
Create a Go project

HTTP framework
Select the HTTP layer for this project.

  > Gin
    Middleware and routing using Gin

  up/down select   enter continue   esc back
```

Review screen:

```text
comxkit                              5 / 5
Ready to create shop

  Module      github.com/example/shop
  Directory   ./shop
  HTTP        Gin
  ORM         Ent
  Database    PostgreSQL
  ID          UUIDv7

  > Create project
    Back
```

The final result lists the created location, stack, and the short setup/run commands. Do not show fake progress or advertise adapters that do not exist.

### Command surface

```sh
comxkit init
comxkit init shop --module github.com/example/shop --http gin --orm ent --db postgres
comxkit add domain catalog
comxkit add resource product --domain catalog
comxkit add resource product --domain catalog --soft-delete --modifier
comxkit version
comxkit help
```

`init [directory]`:

- Interactive mode asks for missing project name/module/stack values. Accepted flags prefill those values.
- In noninteractive mode, require `--module`; use implemented stack defaults for omitted stack flags and fail immediately on missing required values. `--yes` skips final confirmation after successful validation; it never permits overwriting files.
- `--plain` selects sequential accessible prompts on a TTY. Noninteractive mode accepts flags only.
- `--dry-run` reports selected choices and planned file changes without creating directories, downloading dependencies, or executing commands.
- Destination must be absent or empty. Never merge a new application into an arbitrary populated directory.
- Do not initialize Git, install tools, start Docker, connect to DB, or execute downloaded scripts implicitly.

`add domain <name>` creates the domain assembly boundary and registers it in the application. A domain can exist without persistence resources.

`add resource <name> --domain <name>`:

- Interactive mode asks for operations and optional mixins. Flags select them directly for unattended use.
- Default operations: Find, Get, Create, Update, Delete. Offer Upsert, GetBulk, CreateBulk, UpdateBulk, UpsertBulk and DeleteBulk explicitly.
- `--operations` takes the same operation identifiers used by the interactive selection; `--bulk` adds bulk counterparts of selected eligible operations. Find has no bulk counterpart.
- `--upsert-on id` is the supported initial conflict target. Reject another target before writes rather than guessing a business uniqueness rule.
- Domain must already exist; report the `add domain` command if absent.
- Resource names are singular Go identifiers with validated filesystem-safe names. Derive a route name through one naming helper; allow `--route` to override irregular plurals. Reject duplicate domain, resource, route and Ent schema names before writing.

### Stack registry

One registry owns stable IDs, display labels, descriptions, defaults, compatibility declarations and template selection. Forms, flags, help and validation all consume it.

Start with registered implementations `gin`, `ent`, `postgres`. A future adapter adds its descriptor, templates and compatibility tests; it does not require editing separate hardcoded UI and flags lists.

Resolve stack combinations before rendering files. Interactive dependent choices explain incompatibility; noninteractive commands return a clear error. Keep this as a small compile-time registry, without plugin discovery, `init()` registration, reflection or empty future adapters.

## Project output

Proposed initial structure:

```text
shop/
  cmd/server/main.go
  cmd/migrate/main.go
  cmd/migrate/schema_gen.go
  internal/app/client.go
  internal/app/config.go
  internal/app/http.go
  internal/app/domains_gen.go
  ent/schema/.gitkeep
  ent/generate.go
  migrations/.gitkeep
  infrastructure/docker/postgres.yml
  docker-compose.yml
  Dockerfile
  .dockerignore
  .env.example
  .gitignore
  .comxkit.yaml
  atlas.hcl
  Makefile
  go.mod
  README.md
```

- `main` handles process entry and cancellation; `app` composes and owns initialized dependencies.
- Reuse the existing HTTP server, logger, middleware, health and lifecycle helpers. Project config composes relevant settings rather than copying every go-common settings field.
- Initialize and close each owned dependency once. Request cancellation and graceful drain use lifecycle budgets.
- A project with no domains has a working server and database readiness probe. It uses the shared Ent SQL driver, without importing a nonexistent generated Ent client. Adding the first resource introduces the generated Ent client and updates only owned assembly code.
- Health/liveness does not depend on DB; readiness checks the configured DB with a bounded context. Do not run schema changes on server startup.
- Read configuration from environment. The project's dotenv loading is explicit in its local run path; normal deployed processes consume their environment directly. Secrets do not enter generator metadata.
- Import the existing request-ID middleware; do not invent a fallback request ID or silently assume Envoy is installed.
- Initial generated CRUD is a local development scaffold, without an identity provider or automatic business authorization. Expose a clear application wiring point for the existing common checkers; do not claim authentication is configured.
- Compose publishes local development ports on loopback. Container bind addresses and host port mappings remain project configuration.
- Reuse the current canonical PostgreSQL container defaults/image when packaging templates; avoid independently maintained image strings across the root Docker examples and kit. The implementation may add a deterministic asset-packaging step and a drift check instead of duplicating that configuration manually.
- Generated go.mod pins a released go-common version matched to the kit release. A development binary requires an explicit resolvable common version if its build metadata cannot identify one. Never emit an external filesystem `replace` or an unresolved placeholder version.

### Local developer commands

Provide a short, explicit Makefile surface:

- `make setup`: create `.env` from `.env.example` only when absent, resolve pinned Go dependencies, and generate Ent code if schemas exist. Preserve existing config.
- `make generate`: Ent generation using the module's pinned Ent version; no action is required for an empty schema directory.
- `make run-local`: Compose builds and starts PostgreSQL and API, with ordered DB readiness and a migration process before API startup.
- `make stop`: stop the local stack without deleting data.
- `make migrate-diff name=...`: create a versioned SQL migration from schemas using an explicitly configured, separate development database.
- `make migrate`: apply only checked-in migration files.
- `make test`: run application unit tests.

Atlas tooling is version-pinned, invoked consistently through the generated Makefile/container wrapper, and is not assumed to be on PATH. The exact released Atlas version is an implementation choice. No automatic migration generation or database reset occurs during run-local. With no SQL files, the migrate process reports no work and succeeds. There is no seed data until the application defines it.

The generated workflow uses Atlas Community, which does not support `external_schema`. `make migrate-diff` therefore runs the generated `migrate schema` command to a temporary SQL file, mounts that file read-only into the Atlas container, and uses it as the desired schema. The temporary file is removed after the command; no Atlas Pro account or service is required.

## Domain and resource output

```text
internal/catalog/
  module.go
  resources_gen.go
  product/
    model.go
    request.go
    service.go
    repository.go
    http.go
ent/schema/product.go
```

`module.go` is project-owned domain assembly/customization; `resources_gen.go` contains only generated resource registration. `app/domains_gen.go` contains only generated domain registration. `cmd/migrate/schema_gen.go` is the owned bridge from generated Ent migration tables to the schema-authoring command, initially empty when no resource exists. Generated registration never relies on side-effect imports or scanning directories at application startup.

Per resource:

- `model.go`: ordinary fields; no database operations, validation methods, or status transitions.
- `request.go`: typed input/query/result structures, without behavior. Do not expose writable system metadata through request binding.
- `service.go`: business boundary, validation and a small consumer-owned repository interface. Accept dependencies by constructor. Do not force the existing broad `database.Repository` interface onto every resource or change that public interface as part of this feature.
- `repository.go`: concrete Ent mapping and persistence. Reuse shared transaction/error helpers; do not include a second ORM or generic SQL builder.
- `http.go`: routing, request binding and response conversion using existing common wrappers. Service code is independent of Gin contexts.

Ent schemas use `UUIDMixin` and `TimeMixin`. Optional modifier uses `ModifierMixin` plus the application actor resolver integration point. Optional soft delete creates the schema bridge required by `SoftDeleteHook`, installs traversal interception, and enables the corresponding Ent generation features.

Mixins are the initial schema content. The user adds business fields, DTO fields, validation and assignments in ordinary Go code. Do not parse arbitrary future Ent schemas to synthesize business rules, mass-assign fields, or require a parallel resource-fields YAML definition.

This means a fresh resource is deliberately a schema scaffold with real base-ID persistence, not a finished business feature. Create can create an ID/timestamp-only row. Get/Find/Delete work on those base fields. Update rejects empty changes with a validation error until editable fields are defined; it never returns a fake success or a `not implemented` panic. The template shows a concise comment at the intended field-assignment locations.

### CRUD contracts

- All operations accept `context.Context`.
- Find owns filter/search/sort/pagination. Inputs are bounded and filter/sort fields are allowlisted per resource. Default page size is 10, maximum 100; stable ordering includes ID as a tie-breaker. Do not add a parallel Search/List/Page method family.
- Get returns one record or the common not-found error.
- Create returns the persisted resource, including ID/time fields, or a classified conflict/validation error.
- Update requires the ID and explicit mutable changes; absent targets return not found. The service checks the input and the repository persists only explicitly mapped fields.
- Upsert is opt-in, atomic in PostgreSQL, and initially uses an explicitly supplied UUID ID as the conflict target. It returns the persisted resource; it must not resurrect soft-deleted records. With no mutable business fields, an existing live row is an unchanged result. No implicit timestamp touch is needed to simulate business updates.
- Delete returns not found when the target is absent. Soft-delete mode performs the existing hook behavior; normal reads exclude deleted records. Restore/hard-delete routes are not generated in this release.
- Use existing common application error categories and HTTP response helpers; do not create a competing response envelope. New errors, if required, use package errors files and no `package:` prefix.

Proposed HTTP paths preserve explicit Find and the existing operation vocabulary:

| Operation | HTTP |
| --- | --- |
| Find | `POST /api/v1/catalog/products/find` |
| Get | `GET /api/v1/catalog/products/:id` |
| Create | `POST /api/v1/catalog/products` |
| Update | `PATCH /api/v1/catalog/products/:id` |
| Upsert | `PUT /api/v1/catalog/products/upsert` |
| GetBulk | `POST /api/v1/catalog/products/get-bulk` |
| CreateBulk | `POST /api/v1/catalog/products/create-bulk` |
| UpdateBulk | `POST /api/v1/catalog/products/update-bulk` |
| UpsertBulk | `POST /api/v1/catalog/products/upsert-bulk` |
| DeleteBulk | `POST /api/v1/catalog/products/delete-bulk` |
| Delete | `DELETE /api/v1/catalog/products/:id` |

Static routes are registered correctly alongside `:id`. Only selected operations have handlers and routes.

### Bulk contracts

- Default maximum 100 items, configurable in application settings; reject empty/oversized requests at the service boundary.
- GetBulk returns records in requested ID order; duplicate IDs or any missing record are validation/not-found failures, rather than silently dropping entries.
- Bulk writes use one transaction. Any failed item rolls back the request; no per-item partial-success mode or background dispatch is included.
- UpdateBulk accepts per-ID changes, not an unbounded update-by-filter command. Duplicate target IDs are rejected for update/upsert/delete.
- CreateBulk assigns fresh UUIDv7 IDs per item and returns persisted records in request order. UpsertBulk requires explicit IDs.
- DeleteBulk validates the entire ID set and affects the expected number of live rows in the same transaction; a concurrent disappearance cannot produce a misleading success.
- The adapter uses actual Ent batch operations where they preserve these semantics; otherwise use bounded transactional statements. Bulk does not imply per-item goroutines.

## Metadata, ownership and repeatability

`.comxkit.yaml` contains schema version, generator release, chosen stack and generated domain/resource registrations/options. It contains no credentials or duplicate business schema fields. Source dependencies remain pinned by go.mod. The metadata allows deterministic registration updates without editing arbitrary user source with string replacements.

Two explicit ownership classes:

1. Project-owned source/config: starter files, models, services, HTTP handlers, repositories and Ent schemas. Never overwrite on a repeated add command.
2. Generator-owned metadata and `*_gen.go` registration files: may be regenerated only if their last recorded content hashes still match. A hand edit causes a conflict with the affected paths; no force-overwrite escape hatch is required initially.

Generation pipeline: resolve input -> validate stack/names/options -> produce an in-memory file plan -> format Go files -> detect conflicts -> review -> apply.

- Rendering is pure and separate from filesystem mutation and terminal UI.
- Paths must remain within the project root, including symlink checks. Module paths and names never become shell command text.
- Stage new project files in a sibling temporary directory, then publish only after successful rendering and validation.
- For add commands, stage new files and owned replacements; keep a rollback journal and restore the previous state on detected write failures or cancellation. This is not a promise of atomic multi-file transactions across power loss.
- A recoverable interruption state must stop the next mutation and explain the affected files; do not silently continue after a partial installation.
- A repeated identical add is a no-op with a clear existing-resource result. Conflicting options fail without mutations.
- Metadata and generated registration updates participate in the same operation as resource files.
- Existing edited project source remains untouched. No generic AST rewriting of user service/main files.
- No automatic project template upgrade in this delivery. Future upgrades require an explicit diff/review workflow.

## Private implementation layout

```text
cmd/comxkit/main.go
internal/kit/
  client.go          # entry/config and dependency-injected Cobra commands
  model.go           # plain inputs, descriptors, metadata and file plan data
  registry.go        # implemented stack choices and compatibility
  project.go         # project planning
  domain.go          # domain/resource planning and registration composition
  generate.go        # embedded templates and source formatting
  files.go           # conflict detection, staging and rollback
  terminal.go        # interactive forms and output
  constants.go
  errors.go
  templates/
```

Start flat and split only when code justifies it. Entry points depend on private interfaces for prompts/filesystem/command execution where behavior tests need them; do not build a public generic generation framework or add one-file interface packages. Template assets ship in the binary through `embed.FS`; users do not download templates separately or require Git access.

## Meaningful verification

Test through CLI/generator entry points using temporary project roots. Lower-level tests are justified only for logic difficult to exercise from those flows.

- Interactive selections and equivalent flags produce equivalent application configuration/output.
- Single-option categories are visible; adding a test registry adapter changes both selectable choices and accepted flags through the same registry.
- Non-TTY missing-input failures, cancellation, incompatible stack choices, duplicate names/routes and existing-user-file conflicts leave the project unchanged.
- Traversal/symlink rejection, failure during a staged add and rollback preserve existing files and metadata.
- Generate init-only and domain-only projects, resolve their pinned dependencies in an explicit integration flow, and compile/test them with no invented sample entity.
- Generate resources using selected operation/mixin combinations, run Ent generation and compile the resulting project. Exercise top-level HTTP -> service -> real PostgreSQL for CRUD, pagination allowlisting, soft-delete visibility and atomic bulk rollback.
- Apply only checked-in migration files. Verify empty migration directories and DB readiness/startup failures have defined behavior.
- Test an edited service surviving a subsequent resource add and a modified `*_gen.go` producing a conflict rather than overwrite.
- Test no-op repeats and registration of multiple domains/resources without import cycles or duplicate route registration.
- Keep unit tests independent of Docker/network; put generated-project compilation/backend scenarios behind the integration tag. Do not add tests only for exact UI strings, constructor field assignment, model serialization or template whitespace.

## Initial scope boundaries

No additional HTTP framework, ORM, DB adapter, Redis/Temporal/Kafka bootstrap, identity provider, tenancy enforcement, frontend generator, deployment platform, restore route, fields DSL, dynamic plugin loading, or automatic template upgrade is implemented in this release. Existing go-common capabilities remain available for applications to wire through DI.

Tenant scope must not be approximated by an optional tenant_id column. Full tenant scaffolding requires a separate agreed contract covering membership, scoped queries, authorization and uniqueness.

## Acceptance

A new user can install comxkit, inspect the available stack, initialize a project, follow the short setup/run instructions, and get a healthy local server. They can then add a domain and a mixin-only CRUD resource with explicit operations, generate Ent code, create/apply a versioned migration and exercise the base API. They can edit business code and add another resource without losing their changes. Stack descriptions, options and compatibility rules have one maintained source.
