# comxkit

`comxkit` scaffolds a Go service that uses Gin, Ent, PostgreSQL and go-common.
It generates application wiring and a starting resource shape; it does not
invent business fields, validation rules, authentication or authorization.

## Install and initialize

Install a released version of the command:

```sh
go install github.com/huynhanx03/go-common/cmd/comxkit@latest
comxkit init ./shop --module example.com/shop
```

In a terminal, `init` shows the available HTTP, ORM and database choices and
asks for a final review. The initial choices are Gin, Ent and PostgreSQL. To
run unattended, pass each choice explicitly:

```sh
comxkit init ./shop \
  --module example.com/shop \
  --http gin --orm ent --db postgres \
  --common-version v1.2.3 --yes
```

The installed release selects its matching go-common version. A development
build has no release version to pin, so it requires `--common-version`. Use
`--dry-run` to display the files without creating them. Use `--plain` for
sequential, accessible prompts. Initialization writes files only; it does not
start Docker, create a database or generate migrations.

## Run the empty service

```sh
cd shop
make setup
make run-local
```

`make setup` creates `.env` only when missing, generates Ent code when schemas
exist, and resolves the Go module. Compose starts PostgreSQL, applies checked-in
migrations, and then starts the API. Check `GET /live` for process liveness and
`GET /ready` for database readiness. `make stop` stops the containers and keeps
the database volume.

## Add a domain and resource

Run comxkit from the project root:

```sh
comxkit add domain catalog
comxkit add resource product \
  --domain catalog \
  --operations find,get,create,update,delete
```

The default operations are Find, Get, Create, Update and Delete. Optional
operations are `upsert`, `get-bulk`, `create-bulk`, `update-bulk`,
`upsert-bulk` and `delete-bulk`. Bulk writes use one transaction and have a
default maximum size of 100. `--bulk` adds bulk variants for the selected
operations that support them. `--soft-delete` adds the shared soft-delete
fields and behavior; `--modifier` adds actor audit fields. The initial schema
uses the shared UUIDv7 and timestamp mixins and has no business fields.

Generated resource files are the place to add domain-specific data and rules.
For a `product` with a required name, the usual edits are:

1. Add a `name` field to `ent/schema/product.go`.
2. Add `Name` to `Model`, `CreateInput`, and `UpdateInput` in the generated
   `model.go` and `request.go`. Keep model structs as plain data.
3. Validate required values in `service.go`'s `validateCreate` and
   `validateUpdate` functions.
4. Set the field in `repository.go`'s `prepareCreate` and `prepareUpdate`
   functions, and map the Ent value in `service.go`'s `modelFromEntity`.

For every writable field, decide explicitly whether Create, Update and Upsert
may change it. The generator does not infer that policy from the Ent schema.
Find filter/sort fields are also allowlisted in `service.go`; add custom fields
there only when they should be queryable. Empty updates are rejected by the
generated service.

Upsert starts with an explicit UUID `id` conflict target. Its generated default
creates a row or returns the existing row; it does not restore soft-deleted
rows. Add field validation and update mappings in the generated service and
repository if the domain needs additional upsert behavior. Generated bulk
operations reuse the corresponding single-item validation and mapping rules.

## Create and apply schema migrations

After adding or changing an Ent schema, explicitly generate and review a
migration:

```sh
make migrate-diff name=add-product
# Review migrations/*.sql and migrations/atlas.sum.
make migrate
make run-local
```

`migrate-diff` uses the pinned Atlas Community container and a separate
PostgreSQL database in the `tools` Compose profile. It never writes schema
changes into the application database. `make migrate` applies only checked-in
SQL and verifies its checksum; `make run-local` builds the service and runs
those migrations before API startup. It does not generate migrations, reset
the database or seed data automatically.

## Generated files and customization

`.comxkit.yaml` records stack selection, generator metadata, registrations and
hashes of files the generator owns. Resource implementation files are
project-owned and may be edited. Registration files ending in `_gen.go` and the
generated migration schema bridge are generator-owned; comxkit updates them
when resources are added. If an owned file has been edited, comxkit reports a
conflict instead of overwriting it. A pending operation journal also stops a
new mutation until recovery is reviewed. It does not claim power-loss atomicity.
Generated `go.mod` files use a released go-common version and never contain a
local `replace`; integration tests use a separate temporary workspace to test
an unreleased checkout.

The scaffold does not add authentication, tenant isolation, authorization
policies, business fields, seed data or production secrets. Add those according
to the service's requirements before exposing its routes in a real environment.
