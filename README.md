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
    //mage:import rpc
    _ "github.com/ttab/mage/rpc"
    //mage:import s3
    _ "github.com/ttab/mage/s3"
    //mage:import docs
    _ "github.com/ttab/mage/docs"
)
```

This will allow you to run the sql targets using: `mage sql:target-name`.

## RPC tasks

Compiles protobuf service declarations into Go, with [buf](https://buf.build/)
as the compiler. It replaces the `twirp` namespace, which ran protoc inside the
`elephant-twirptools` image: buf and every plugin run as
`go run <module>@<version>` with the versions pinned in the `rpc` package, so
generating needs no Docker, installs nothing, and takes nothing off `PATH`. A
generator moves when this module is bumped, and the regenerated files show up
in the bump's diff. No `buf.gen.yaml` is committed anywhere — the generation
template is passed to buf inline.

The targets discover services as `<proto root>/*/service.proto`, where the
proto root is `rpc` when that directory exists and the repository root
otherwise, and generate for every `.proto` file in a service's directory. A
file that declares no service is compiled to messages and nothing else.

Per service, into the service's own directory:

| File | Plugin |
|---|---|
| `service.pb.go` | `protoc-gen-go` |
| `<package>connect/service.connect.go` | `protoc-gen-connect-go` |
| `<package>connect/service.elephant.go` | `protoc-gen-elephant-rpc` |
| `service.rpc.go` | `protoc-gen-elephant-rpc`, when Twirp is off |
| `service.twirp.go` | `protoc-gen-twirp`, when Twirp is on |

Nothing but Go is generated. The `twirp` namespace also wrote an OpenAPI 3
specification per service; nobody consumed them and they described the Twirp
surface only, so the `rpc` namespace does not carry them forward, and a
repository adopting it deletes its `docs/*-openapi.json`.

`protoc-gen-elephant-rpc` emits the Connect adapters that put Connect on the
plain protobuf service interface: `New<Service>ServiceHandler`, which serves an
implementation with the `(ctx, *Request) (*Response, error)` signatures Twirp
has always generated, and `New<Service>ServiceClient`, which is a drop-in for
`New<Service>ProtobufClient`. The adapters take and return that interface, so
something has to declare it — `protoc-gen-twirp` does while a repository still
generates Twirp, and the plugin's own `interface` option does when it does not.
The option follows `rpc.Twirp`, which is what makes a Connect-only repository's
generated code compile with no configuration of its own;
`rpc.ElephantRPCOptions` overrides it.

The plugin is skipped until elephantine has tagged a release containing it, so
a repository generating today gets the messages and the Connect code and, where
it is turned on, Twirp.

### `rpc:generate`

Generate compiles the service declarations. It reads nothing but the sources,
so it works in a repository that has never been tagged. There is no
`rpc:release`: nothing is stamped with a version any more, so a release is a
plain git tag.

### `rpc:vendorProto` "module" "file"

VendorProto copies a `.proto` file out of a Go module and into the
repository's vendored proto root, `rpc/vendor`:

``` shell
mage rpc:vendorProto github.com/ttab/elephant-api newsdoc/newsdoc.proto
```

The compiler only sees the files in its workspace and a workspace cannot reach
outside the repository, which is what protoc was doing when it was handed a
dependency's module directory as a `--proto_path`. A vendored file keeps the
path it has in the repository it came from, so the `import` in the service's
own `.proto` does not change. The vendor directory becomes a module root of
its own in the buf workspace, which is the one thing that makes the repository
need a `buf.yaml`; the target writes it.

The vendored file is compiled but never generated for — its Go code comes from
the module it was vendored out of, which is where the service imports it from.

`newsdoc/newsdoc.proto` is the only file the fleet vendors. It is generated
from the `newsdoc` module, so the copy changes when that module does, and the
target is idempotent: run it in CI and let `git diff --exit-code` report the
drift.

### `rpc:stub` "application" "Service" "MethodName"

Stub generates a protobuf service stub in `[proto root]/[application]/service.proto`.

### Configuration

The exported variables of the `rpc` package are the configuration, set from
the importing magefile:

``` go
import (
    //mage:import rpc
    "github.com/ttab/mage/rpc"
)

func init() {
    // This repository still serves the /twirp/ paths.
    rpc.Twirp = true
}
```

| Variable | Environment | Default | Meaning |
|---|---|---|---|
| `rpc.Twirp` | `RPC_TWIRP` | off | Run `protoc-gen-twirp`. A new service is Connect only; an existing one turns it on for as long as it still serves the `/twirp/` paths. |
| `rpc.VendorDir` | `RPC_VENDOR_DIR` | `rpc/vendor` | The proto root `rpc:vendorProto` copies into. |
| `rpc.ExtraProtoRoots` | `RPC_EXTRA_PROTO_ROOTS` | none | Further directories to add to the buf workspace, for a repository that keeps protobuf sources outside the proto root. Their files are resolvable as imports and are not generated for. |
| `rpc.ElephantRPCOptions` | — | `interface` follows Twirp | Extra options for `protoc-gen-elephant-rpc`. The one to know about is `interface`, which decides whether the plugin emits the plain service interface itself, and which is set here only to override the default of leaving it to `protoc-gen-twirp` for as long as Twirp is generated. |

The environment variable overrides the variable for a single run, which is what
a CI job or a one-off regeneration uses rather than editing the magefile.

### Developing `protoc-gen-elephant-rpc`

`ELEPHANT_RPC_PLUGIN` replaces the pinned plugin command, and works whether or
not the pin is set — which it is not, until elephantine tags a release with the
plugin in it. Point it at a module checkout to generate a repository with a
plugin you are editing:

``` shell
ELEPHANT_RPC_PLUGIN=../elephantine mage rpc:generate
```

It also takes a `module@version`, for generating against a plugin version other
than the pinned one.

The same variable runs this module's own end-to-end test of the plugin, which
generates the fixture repository with it and checks that the emitted code
compiles. It is skipped when the variable is unset, since there is no released
version to fall back on:

``` shell
ELEPHANT_RPC_PLUGIN=../elephantine go test ./rpc
```

## Twirp tasks

Deprecated: use the `rpc` namespace above. These targets run protoc inside the
`elephant-twirptools` image, which is being retired, and they cannot generate
Connect code.

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
