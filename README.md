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

The module needs Go 1.27, which is the fleet's floor. The `rpc` targets also
pin the toolchain they compile the protobuf generators with, in
`rpc.GeneratorToolchain`, and download it when the machine has another one.

## RPC tasks

Compiles protobuf service declarations into Go, with [buf](https://buf.build/)
as the compiler. It replaces the `twirp` namespace, which ran protoc inside the
`elephant-twirptools` image: buf and every plugin run as
`go run <module>@<version>` with the versions pinned in the `rpc` package, so
generating needs no Docker, installs nothing, and takes nothing off `PATH`. A
generator moves when this module is bumped, and the regenerated files show up
in the bump's diff. No `buf.gen.yaml` is committed anywhere — the generation
template is passed to buf inline.

Generation needs the module proxy, or a warm module cache. Running a tool as a
separate module means a version query on every run, so `GOPROXY=off` fails even
with everything already downloaded — the twirptools image at least ran from a
local pull, and "installs nothing" bought that at the price of the network. Two
environment variables are normalised for the generator invocations rather than
inherited: `GOFLAGS` has any `-mod` flag dropped, since a repository that
vendors its dependencies would otherwise send the generators looking for
themselves in its `vendor` directory, and `GOTOOLCHAIN` is set to
`rpc.GeneratorToolchain` and downloaded if the machine has another one. The
toolchain is pinned because it decides some of the bytes: `protoc-gen-twirp`
embeds a gzipped file descriptor, `compress/flate` changed its output between
Go 1.26 and Go 1.27, and the same declaration produced a different
`service.twirp.go` on two machines until it was.

The targets discover every `service.proto` under the proto root, at any depth,
where the proto root is `rpc` when that directory exists and the repository
root otherwise, and generate for every `.proto` file in a service's directory.
A file that declares no service is compiled to messages and nothing else. The
walk skips `vendor`, `node_modules` and `testdata` directories, and anything
beginning with a dot: a dependency's checkout is not this repository's
declaration, and a fixture belongs to the test that reads it.

**The layout says which shape a service is.** A declaration in the flat
layout, `<proto root>/<application>/service.proto`, is a *dual-stack* service:
it implements the plain protobuf interface, serves Connect through the
generated adapters, and serves the `/twirp/` paths while `rpc.Twirp` is set.
That is what the fleet grew up with, and nothing new is written into it.

A declaration whose own directory is a version — `<proto root>/<application>/v1`,
and `<proto root>/elephant/<application>/v1` for a package with a prefix in it —
is a *native* service. It implements connect-go's own handler interface, gets
`protoc-gen-go` and `protoc-gen-connect-go` output and nothing else, and may
declare streaming methods, which a dual-stack service cannot: both
`protoc-gen-elephant-rpc` and `protoc-gen-twirp` fail generation on a stream.
That is the shape `rpc:stub` scaffolds and the shape a new service has. A
repository can hold both while it moves one service at a time, and
`rpc.DualStack` names the service directories that stay dual stack whatever
their layout, for a legacy service that moves to the versioned layout before
its Twirp callers are gone.

A `go_package` that names an import path has to name the one the generated code
lands under, `<module path>/<directory>`, and generation refuses one that does
not: `protoc-gen-go` only reads the last element of the declaration, so the
mistake is invisible until the Connect adapters try to import the message
package. Writing it as a relative path, or leaving it out, is still fine — the
import path is derived.

Per service, into the service's own directory:

| File | Plugin | Shape |
|---|---|---|
| `service.pb.go` | `protoc-gen-go` | both |
| `<package>connect/service.connect.go` | `protoc-gen-connect-go` | both |
| `<package>connect/service.elephant.go` | `protoc-gen-elephant-rpc` | dual stack |
| `service.rpc.go` | `protoc-gen-elephant-rpc`, when Twirp is off | dual stack |
| `service.twirp.go` | `protoc-gen-twirp`, when Twirp is on | dual stack |

`service.rpc.go` and `service.twirp.go` are the same declaration, so exactly
one of them is written, and whichever of the two is not generated this time is
deleted if an earlier configuration left it behind. Two declarations of one
interface in a package do not compile, which is what turning Twirp off used to
leave behind. A native service generates neither, so both go, and so do the
adapters in its connect package: they take and return an interface that is no
longer declared. Only a file carrying the generator's `// Code generated by`
header is removed; anything else in the directory is somebody's source,
whatever it is called.

Generation is two buf runs when a repository holds both shapes, since the two
plugin lists are different and buf takes one template per run. A shape with no
services in it is not run at all.

#### The buf module root is the proto root

A repository whose protobuf sources live under `rpc` gets a `buf.yaml` that
roots the buf module there. buf checks a file's package against the directory
it is in *relative to the module root*, so `elephant.collab.v1` in
`rpc/elephant/collab/v1` only passes `PACKAGE_DIRECTORY_MATCH` from a module
rooted in `rpc`. A repository whose proto root is the repository root, which is
what elephant-api has, needs no configuration and gets none.

The module root is also what a file is named relative to, and that reaches two
things. An `import` inside a `.proto` is written relative to the proto root —
`import "greeter/types.proto"`, not `import "rpc/greeter/types.proto"` — and
the name buf gives the file is part of what `protoc-gen-go` writes, so the
descriptor is `File_greeter_service_proto` where a module rooted in the
repository would have given `File_rpc_greeter_service_proto`. That rename does
not reach the service's callers — a file's name is independent of its
`package`, so the message and service full names, the RPC paths and the
encoding are untouched — but it is a large diff the next `rpc:generate`
produces. The generated files themselves still land next to the declaration
they came from.

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
`rpc.ElephantRPCOptions` overrides it in either direction. Asking for
`interface=true` while Twirp is still generated is refused, naming both: both
plugins would write the interface and the package would not compile.

The plugin is pinned to the elephantine release that ships it, and a `ttab/mage`
bump is what moves it, as with every other generator. It cannot be turned off: an empty pin is an error rather than a run that quietly
leaves the adapters as they were.

`protoc-gen-twirp` is the one generator that is not run as
`go run <module>@<version>`. It is a `+incompatible` module with no `go.mod`, so
that would resolve its dependencies afresh on every run; it runs out of a small
module this package carries instead, written into a working directory for the
length of the run, which requires the pinned version and carries a complete
`go.sum`.

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
its own in the buf workspace, which is one of the two things that make a
repository need a `buf.yaml` — the other is a proto root under `rpc`, which has
to be the module root; the target writes it.

A `buf.yaml` that this package did not write is left alone — buf's lint and
breaking change rules live in the same file — but it has to declare the
vendored root as a module and exclude it from the module rooted in the proto
root, and both `rpc:vendorProto` and `rpc:generate` fail naming what is
missing if it does not. A workspace buf cannot resolve the import in is the
failure the target exists to prevent.

The vendored file is compiled but never generated for — its Go code comes from
the module it was vendored out of, which is where the service imports it from.

`newsdoc/newsdoc.proto` is the only file the fleet vendors. It is generated
from the `newsdoc` module, so the copy changes when that module does, and the
target is idempotent: run it in CI and let `git diff --exit-code` report the
drift.

### `rpc:stub` "application" "Service" "MethodName"

Stub generates a protobuf service stub in
`[proto root]/elephant/[application]/v1/service.proto`, with the protobuf
package `elephant.[application].v1` and a `go_package` that names the Go
package separately from the import path, since the last element of that path
is the version. The service gets the `Service` suffix if the caller left it
out.

That is a native service, and the directory mirrors the package because buf
checks one against the other. A stub passes `buf lint` with the `STANDARD`
rules and no exemptions as it is written, which is what `rpc:lint` reports.
The flat layout is still compiled; nothing new is written into it.

### `rpc:lint`

Runs `buf lint` over the repository's own service declarations, with the rules
the workspace configuration names — buf's `STANDARD` set unless a hand-written
`buf.yaml` says otherwise. A vendored proto is compiled as an import and never
linted: its rules, and its exemptions, belong to the repository it came from.

### `rpc:breaking`

Compares the declarations against an earlier state of them with
`buf breaking`. The default is `.git#branch=main`, the tip of the repository's
own main branch, and `rpc.BreakingAgainst` or `RPC_BREAKING_AGAINST` names any
other buf input:

``` shell
RPC_BREAKING_AGAINST=.git#tag=v1.4.0 mage rpc:breaking
```

The branch a git input names is resolved against the checkout the target runs
in, since it names a ref there and not on any server. A branch that is only a
remote-tracking ref — which is the normal state of a checkout that is not a
developer's, since `actions/checkout` leaves a detached head and
`git clone -b feature` leaves the one branch it was asked for — is compared
against as `origin/<branch>`. **The history has to be in the checkout**: a build that
fetched only the commit under test, which is what `actions/checkout` does
unless it is given `fetch-depth: 0`, has nothing to compare with, and is told
that rather than being handed buf's `couldn't find remote ref main` from the
middle of a compilation. So is a repository with no git history at all, and one
whose trunk is called something else.

The `buf.yaml` has to be committed along with the declarations: the module root
decides what buf calls a file, so the two sides of the comparison have to agree
on it. That has a one-time cost for a repository whose proto root is under
`rpc` and that is only now getting a `buf.yaml`, on the commit that moves it to
this version of the module: the state being compared against names its files
from the repository root, this one names them from the proto root, and buf
reports every declaration in the repository as deleted. No other input helps,
since every earlier state has the same problem — the target says so, and the
check is one to skip on that pull request.

### `rpc:format` and `rpc:formatCheck`

`rpc:format` rewrites the declarations in buf's formatting. `rpc:formatCheck`
prints the diff it would have applied and fails when there is one, which is
what a CI job runs — a target that rewrites the working tree is not an answer a
build can act on.

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
| `rpc.DualStack` | `RPC_DUAL_STACK` | none | Service directories, relative to the repository root, that generate dual stack whatever their layout. For a legacy service that moves to the versioned layout before its Twirp callers are gone. An entry that names no discovered service is an error. |
| `rpc.BreakingAgainst` | `RPC_BREAKING_AGAINST` | `.git#branch=main` | The buf input `rpc:breaking` compares against. A branch is resolved against the checkout, `origin/<branch>` included, so a CI job needs the history: `actions/checkout` with `fetch-depth: 0`. |
| `rpc.VendorDir` | `RPC_VENDOR_DIR` | `rpc/vendor` | The proto root `rpc:vendorProto` copies into. |
| `rpc.ExtraProtoRoots` | `RPC_EXTRA_PROTO_ROOTS` | none | Further directories to add to the buf workspace, for a repository that keeps protobuf sources outside the proto root. Their files are resolvable as imports and are not generated for. |
| `rpc.ElephantRPCOptions` | — | `interface` follows Twirp | Extra options for `protoc-gen-elephant-rpc`. The one to know about is `interface`, which decides whether the plugin emits the plain service interface itself, and which is set here only to override the default of leaving it to `protoc-gen-twirp` for as long as Twirp is generated. |

The environment variable overrides the variable for a single run, which is what
a CI job or a one-off regeneration uses rather than editing the magefile.

### Developing `protoc-gen-elephant-rpc`

`ELEPHANT_RPC_PLUGIN` replaces the pinned plugin command. Point it at a module
checkout to generate a repository with a plugin you are editing:

``` shell
ELEPHANT_RPC_PLUGIN=../elephantine mage rpc:generate
```

It also takes a `module@version`, for generating against a plugin version other
than the pinned one.

The same variable makes this module's own end-to-end test of the plugin, which
generates the fixture repository and checks that the emitted code compiles, run
against the checkout instead of the pinned version:

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
