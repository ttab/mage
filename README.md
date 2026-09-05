# Mage tasks

Reusable [mage](https://magefile.org/) tasks.

Import in your "magefiles/magefile.go" to add the tasks:

``` go
//go:build mage
// +build mage

package main

import (
    //mage:import sql
    _ "github.com/ttab/mage/sql"
    //mage:import twirp
    _ "github.com/ttab/mage/twirp"
    //mage:import s3
    _ "github.com/ttab/mage/s3"
    //mage:import docs
    _ "github.com/ttab/mage/docs"
)
```

This will allow you to run the sql targets using: `mage sql:target-name`.

## Twirp tasks

### `twirp:stub` "application" "Service" "MethodName"

Stub generates a protobuf service stub in `rpc/[application]/service.proto`.

### `twirp:generate`

Generate auto-discovers all `rpc/*/service.proto` files, runs protoc to compile the service declarations, and generates openapi3 specifications. The version is resolved from the last ancestor git tag.

### `twirp:release` "version"

Release runs the same protoc compilation and openapi3 generation as `twirp:generate`, but uses the provided version string instead of resolving it from git tags.

## SQL tasks

Vendoring a library's tern migrations into a service spans several of these
targets; [docs/schema-vendoring.md](docs/schema-vendoring.md) is the overview.

### `sql:generate`

Generate uses sqlc to compile the SQL queries in postgres/queries.sql to Go, adding the default sqlc.yaml file if necessary.

### `sql:sqlcConfig`

SqlcConfig adds the default sqlc.yaml configuration file.

### `sql:postgres` "name"

Postgres creates a local Postgres 17 instance using docker. Data will be stored under the platform data directory (e.g. `~/.local/share/tt-mage/postgres-[name]` on Linux, `~/Library/tt-mage/postgres-[name]` on macOS). Override with the `STATE_DIR` environment variable.

### `sql:postgres18`

Postgres18 creates the local Postgres 18 instance, storing its data in `tt-mage/postgres18` under the same data directory. It takes no name: one instance is shared by all projects, since the per-project data directories of `sql:postgres` were never anything we made use of.

Both versions publish port 5432, and no major version can read another's data directory, so only one instance runs at a time — starting either stops whichever of them is running. Nothing else changes when you switch: the connection string, `sql:db`, `sql:migrate` and `sql:dumpSchema` are the same against both.

The databases in an instance are its own, so a project that moves to 18 recreates them there with `sql:db` and `sql:migrate`.

### `sql:db`

DB calls DBWithName using the current directory name as the database name.

### `sql:dbWithName` "name"

Creates a local database and login role with the same name and the password 'pass'.

### `sql:dropDB`

DropDB calls DropDBWithName using the current directory name as the database name.

### `sql:dropDBWithName` "name"

Drops the database and login role with the given name.

### `sql:migrate`

Migrate the database to the latest version using the migrations in "./schema".

### `sql:vendorAdd` "module" "dir"

Declares a library's tern migration directory in `schema/vendor.json`, so that
`sql:vendor` starts copying its migrations in:

``` shell
mage sql:vendorAdd github.com/ttab/howdah tokenstore/pgstore/schema
```

See [Declaring the library](docs/schema-vendoring.md#declaring-the-library-sqlvendoradd).

### `sql:vendor`

Copies the tern migrations declared in `schema/vendor.json` out of their
libraries and into `./schema`, which is the only place either `sql:migrate` or
elephant-platform's `setup db migrate` looks — neither of them sees a migration
inside a dependency.

It only ever adds files, and it numbers them after the migrations already
there. See [Making the copies](docs/schema-vendoring.md#making-the-copies-sqlvendor).

### `sql:vendorCheck`

Fails when a library migration declared in `schema/vendor.json` is not covered
by the service's own migrations. Wire it into lint or a test: the failure it
prevents is quiet, since a service that bumps a library past a new migration
builds, tests and deploys before failing at runtime on a table nobody created.

It distinguishes four cases, because the fixes differ and one of them is
emphatically not "run vendor again". See [What the check
reports](docs/schema-vendoring.md#what-the-check-reports), and [DDL the service
already wrote by
hand](docs/schema-vendoring.md#ddl-the-service-already-wrote-by-hand) for the
`-- covers:` escape hatch.

### `sql:librarySchema` "migrationsDir" "out"

For a library that ships migrations and also needs a flat schema for sqlc to
read. Writes the "create above" halves of the migrations into one file, in
migration order, so the library does not hold the same DDL twice with nothing
keeping the copies together.

See [The flat schema sqlc
reads](docs/schema-vendoring.md#the-flat-schema-sqlc-reads-sqllibraryschema).

### `sql:rollback` N

Rollback to a specific schema version:

``` shell
mage sql:rollback 1
```

### `sql:connString`

Prints the connection string for use with psql:

``` shell
psql $(mage sql:connString)
```

### `sql:dumpSchema`

DumpSchema writes the current database schema to "./postgres/schema.sql".

The dump is always taken with Postgres 18's `pg_dump`, which reads a 17 server perfectly well, and the `-- Dumped from database version` / `-- Dumped by pg_dump version` header is stripped along with the `\restrict` directives. Between them, a project's schema.sql stops depending on which Postgres produced it: the same DDL dumped from a 17 server and from an 18 server gives a byte-identical file, so moving a project over is not a schema diff.

The first dump after upgrading mage drops those two header lines, so expect one churn commit per project.

### `sql.GrantReporting`

GrantReporting is a reusable function (not a standalone target) that grants SELECT on the provided tables to a reporting role. It prompts interactively for the role name and connection string. Wrap it in your magefile to expose it as a target:

``` go
func GrantReporting(ctx context.Context) error {
    return sql.GrantReporting(ctx, []string{"my_table", "other_table"})
}
```

## S3 tasks

### `s3:minio`

Minio creates a local minio instance using docker. Data will be stored under the platform data directory (e.g. `~/.local/share/tt-mage/local-minio` on Linux, `~/Library/tt-mage/local-minio` on macOS).

Exposes an S3 compatible endpoint on http://localhost:9000 and a web GUI on http://localhost:9001.

Use minioadmin/minioadmin to log in, or as access key/secret for the API.

### `s3:bucket` "name"

Creates a bucket in the local minio instance.

## Documentation tasks

### `docs:links`

Checks that every relative link and heading anchor in the repository's markdown files resolves: that the path names a file that exists, and that a `#anchor` names a heading the target document actually has. Run from the repository root.

Only relative links are checked — following external URLs would make the check depend on the network and on other people's uptime. Anchors are resolved with GitHub's slug rules, since GitHub is where the documentation is read, and headings inside fenced code blocks are not treated as headings.

Only the repository's own documentation is checked. In a git worktree the file list comes from `git ls-files`, so anything an ignore rule covers — build output, a vendored dependency, a virtualenv under the repository root — is left alone, as are files ignored by a developer's global excludes. Outside a worktree, or with no `git` on the path, the directory is walked instead.

Worth wiring into CI wherever documentation is cross-referenced by section, since a renamed heading breaks inbound links that nothing else notices:

``` yaml
- name: Check documentation links
  run: go run github.com/magefile/mage docs:links
```

The checker itself is `github.com/ttab/mage/doclint`, which can be called directly from a test if you would rather have it fail there than in a mage target.
