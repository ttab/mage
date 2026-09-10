# Changelog

Everything from v0.10.0 onwards is documented here; earlier releases are not
reconstructed. The entries are derived from the release tags, and the linked
pull requests hold the detail.

## [v0.15.0] - Unreleased

**Breaking (repositories whose protobuf sources live under `rpc`):** the buf
module root moves from the repository root to the proto root, because buf
checks a file's package against its directory relative to the module root and
resolves an `import` against the same root — `elephant.collab.v1` in
`rpc/elephant/collab/v1` only passes `PACKAGE_DIRECTORY_MATCH` from a module
rooted in `rpc`. Two things follow for such a repository, and both of them
have to be dealt with in the commit that bumps this module. An `import` inside
a `.proto` is now written relative to the proto root, so
`import "rpc/greeter/types.proto"` becomes `import "greeter/types.proto"`;
generation fails with `imported file does not exist` until it is. And buf now
names the file `greeter/service.proto` rather than `rpc/greeter/service.proto`,
which is part of what `protoc-gen-go` writes: the descriptor variable is
`File_greeter_service_proto` where it was `File_rpc_greeter_service_proto`, and
the same rename runs through the generated file's unexported symbols, so
`rpc:generate` produces a large diff and anything naming a file descriptor
directly has to follow it — `service.twirp.go` embeds the same descriptor, so
it changes too. None of that reaches the service's callers: a file's name is
independent of its `package`, so the message and service full names, the RPC
paths and the encoding are untouched, and the only exported symbol that moves
is the descriptor variable. The cost is a regeneration diff, not a release
coordinated with anybody calling the service. The generated files themselves
stay exactly where they were, next to the declaration they came from. The `buf.yaml` the targets write is
part of the bump and has to be committed: `rpc:breaking` compares against it
on both sides. That comparison cannot pass on the bump commit itself, since
the state being compared against has no `buf.yaml` and names its files from
the repository root, so buf reports every declaration in the repository as
deleted; no other input helps, every earlier state has the same problem, and
`rpc:breaking` says so rather than reporting the deletions. Skip that check on
the bump's pull request. A repository whose proto root *is* the repository
root, which is elephant-api, is unaffected: it needs no `buf.yaml` and still
gets none.

**Behaviour change (what a versioned declaration generates):** the layout now
says which shape a service is. A declaration in the flat layout,
`<proto root>/<application>/service.proto`, is dual stack and generates exactly
what it did before. A declaration whose own directory is a version is native:
`protoc-gen-go` and `protoc-gen-connect-go` only, so no
`<package>connect/service.elephant.go`, no `service.rpc.go` and no
`service.twirp.go`, and it may declare streaming methods, which a dual-stack
service cannot. A service already in the versioned layout that still serves the
`/twirp/` paths therefore loses its adapters and its plain interface on the
next `rpc:generate` unless it is named in the new `rpc.DualStack`, which forces
the old behaviour whatever the layout. Regeneration removes the files that are
no longer generated, the adapters included, since they take and return an
interface that would no longer be declared.

**Behaviour change (`rpc:stub`):** a stub is written to
`<proto root>/elephant/<application>/v1/service.proto` with the package
`elephant.<application>.v1`, where it was `<proto root>/<application>/v1` with
the package `ttab.<application>.v1`, and the service gets the `Service` suffix
if the caller left it out. The old layout failed `buf lint` on
`PACKAGE_DIRECTORY_MATCH` and `SERVICE_SUFFIX`; a fresh stub now passes the
`STANDARD` rules with no exemptions.

**Behaviour change (a committed `buf.yaml` this package wrote):** the comment
header the generated `buf.yaml` carries has been rewritten, so the next
`rpc:generate` or `rpc:vendorProto` in a repository that already commits one —
elephant-public-api and elephant-tt-api — rewrites the file. The modules it
declares, and the generated Go, are unchanged.

Changes:

- Three new targets, all running the pinned buf and all scoped to the
  repository's own declarations, so a vendored proto is compiled as an import
  and never checked against rules that belong to the repository it came from.
  `rpc:lint` runs `buf lint`. `rpc:breaking` runs `buf breaking` against
  `rpc.BreakingAgainst`/`RPC_BREAKING_AGAINST`, which defaults to
  `.git#branch=main`, and reports a repository with no git history or a
  differently named trunk by name rather than as a buf failure — note that
  `buf.yaml` has to be committed for the two sides of the comparison to name
  their files the same way. The branch is resolved against the checkout the
  target runs in and compared against as `origin/<branch>` when that is all the
  checkout has, which is what a CI checkout is; the history still has to be
  there, so a workflow that runs the target checks out with
  `fetch-depth: 0`. `rpc:format` rewrites the declarations in buf's
  formatting and `rpc:formatCheck` reports what it would rewrite without
  touching anything, which is what a CI job runs.
- `rpc.DualStack`, with the `RPC_DUAL_STACK` override, names the service
  directories that generate dual stack whatever their layout, for a legacy
  service that moves to the versioned layout before its Twirp callers are gone.
  An entry that names no discovered service is an error rather than a setting
  with no effect.
- Service discovery finds a `service.proto` at any depth under the proto root
  rather than only one or two directories down, which is what a declaration
  mirroring a package with a prefix in it — `elephant.<app>.v1` in
  `rpc/elephant/<app>/v1` — needs. It skips `vendor`, `node_modules` and
  `testdata` directories and anything beginning with a dot, so neither a
  dependency's checkout nor a test fixture is mistaken for the repository's own
  declaration. A declaration two directories down whose own directory is not a
  version — `rpc/hub/views/service.proto` — used to be skipped entirely and is
  now generated for, as a flat service named after its directory.
- Generation is two buf runs when a repository holds both service shapes, since
  the two plugin lists differ and buf takes one template per run. A shape with
  no services in it is not run at all.
- `buf.yaml` is now written for any repository with a proto root under `rpc`,
  not only for one that has vendored a proto. A hand-written `buf.yaml` is
  still left alone, and is now also checked for declaring the proto root as the
  module root: one that declares `- path: .` for a repository whose sources are
  under `rpc` generated fine before and is now refused, by `rpc:generate` and
  by every check, until it names the proto root instead.
- The README documents both service shapes, the layout rule and the new
  targets.

## [v0.14.0] - 2026-09-07

**Behaviour change (generated code):** the sqltools image moves from v0.1.3 to
v0.2.1, which carries sqlc v1.31.1 where v0.1.3 carried v1.25.0 — six minor
versions — so the next `sql:generate` in a project produces a diff beyond the
version stamp. Three changes in that range alter or block generation: a model
whose name ends in `metadata` was named incorrectly and is now renamed
(v1.28.0), `xid8` columns map to `pgtype.Uint64` under pgx/v5 (v1.31.0), and an
invalid column reference in `ON CONFLICT DO UPDATE` is rejected instead of
silently generating code (v1.31.0). Neither the `metadata` suffix nor `xid8`
occurs in the elephant repositories today, but `ON CONFLICT DO UPDATE` is
widespread, so regenerate deliberately in each project rather than discovering
the diff mid-feature. `sql:generate` also stops passing `--experimental` to
sqlc: the flag was deprecated and read by nothing in v1.25.0 and removed
outright in v1.26.0, so the new image fails the invocation with `unknown flag:
--experimental`.

**Behaviour change (migrations):** the first `sql:migrate` against a database
that an earlier image migrated alters its version table. From tern v2.3.6 the
migrator checks `public.schema_version` for a primary key at startup and issues
`alter table public.schema_version add primary key (version)` when there is
none, whether or not there are migrations to apply. The table holds a single
row, so the `ACCESS EXCLUSIVE` lock is taken and released immediately and no
maintenance window is needed — but it is DDL issued by the migrator rather than
by a migration file, and `sql:migrate` goes wherever `CONN_STRING` points, not
only to a local development database.

Changes:

- tern upgraded from v2.1.1 to v2.4.3. Beyond the version table, it splits
  statements correctly when a migration contains dollar-quoted blocks (v2.2.1),
  which previously broke function and trigger bodies, and fixes connection
  setting precedence (v2.4.2) and port handling when `sslmode` is unset
  (v2.2.2).
- `CONN_STRING` can use `sslmode=verify-full&sslrootcert=system` for
  `sql:migrate` and `sql:rollback`. The pgx in the old image predated
  `sslrootcert=system` and read `system` as a filename, failing with `unable to
  read CA file: open system`, and it stopped at the first resolved address
  rather than falling back to the next, so a host with no IPv6 route could not
  reach an AAAA-first provider at all. A connection string that was downgraded
  to `sslmode=require` to work around this can move back.
- sqlc upgraded from v1.25.0 to v1.31.1. The image itself is built on Debian 13
  (trixie) rather than Debian 12, and its arm64 build is cross-compiled rather
  than emulated.

## [v0.13.1] - 2026-09-06

Changes:

- `protoc-gen-elephant-rpc` is pinned to elephantine v0.29.0, the release that
  ships it, instead of a pre-release commit of its feature branch. A repository
  that generated with v0.13.0 regenerates to the same output, since the plugin
  did not change between that commit and the tag.

## [v0.13.0] - 2026-09-06

**Breaking (Go 1.27):** the module's `go` directive is 1.27.1, so a repository
that imports these targets needs a Go 1.27 toolchain to build its magefile. The
`rpc` targets go one step further and pin the toolchain they compile the
protobuf generators with, `rpc.GeneratorToolchain`, downloading it when the
machine has another one: the toolchain decides some of the bytes a generator
writes, and `protoc-gen-twirp` embeds a gzipped file descriptor whose encoding
changed between Go 1.26 and Go 1.27, so the same declaration produced two
different `service.twirp.go` files depending on who ran the generator.

**New namespace (rpc):** `rpc:generate` is the protobuf generation path from
here on, and it generates Connect code alongside the messages. It compiles with
buf rather than protoc in the `elephant-twirptools` image, and runs buf and
every plugin out of a module pinned in the `rpc` package — `go run
<module>@<version>` for all of them but `protoc-gen-twirp` — so generating
needs no Docker, installs nothing and takes nothing off `PATH`. No `buf.gen.yaml` is committed anywhere; the generation
template is passed to buf inline. Adopting it in a repository is this bump, a
`//mage:import rpc` in the magefile and, for a repository that still serves the
`/twirp/` paths, `rpc.Twirp = true` in an `init` — Twirp generation is off by
default, because a new service is Connect only. Generation reads nothing but
the sources, so a repository that has not been tagged yet — which is where a
new service starts — can run `rpc:generate`. Services are discovered in either
layout, `<proto root>/*/service.proto` and `<proto root>/*/v*/service.proto`,
and `rpc:stub` scaffolds the versioned one, so a repository can move to it a
service at a time. Two configurations are refused rather than generated:
`rpc.Twirp` together with the plugin's `interface=true` option, since both
write the plain service interface and the package would declare it twice, and
a `go_package` that names an import path other than the one the generated code
lands under.

**Removed (OpenAPI):** the `rpc` namespace does not generate the OpenAPI 3
specifications the `twirp` namespace wrote to `docs/<service>-openapi.json`.
Nobody consumed them, they described the Twirp surface only, and the generator
cannot run under buf at all (it advertises editions support without a minimum
edition, which protoc tolerates and buf rejects). A repository adopting the
namespace deletes its `docs/*-openapi.json` and the links to them. With nothing
left to stamp a version into there is no `rpc:release` either: a release is a
plain git tag, and `git describe` is no longer consulted by any target.

**Behaviour change (the first regeneration):** the Go code is generated by
protoc-gen-go v1.36.12 where the image pinned v1.36.2, so the first
`rpc:generate` in a repository rewrites the embedded descriptors, adds an
`unsafe` import to every `.pb.go`, and records `protoc (unknown)` in the file
header, since buf is the compiler and reports no protoc version. The
`service.twirp.go` files change only in their gzipped descriptor blob. Expect
one large, mechanical diff per repository and nothing behind it.

**Behaviour change (imported protos):** a `.proto` file imported from another
module is vendored into the repository with `rpc:vendorProto` instead of being
reached through the dependency's module directory, which buf cannot do — a
workspace cannot reach outside its root. The vendored file keeps the path it
has in the repository it came from, so the `import` in the service's own
`.proto` does not change, and the vendor directory becomes a module root of its
own in a generated `buf.yaml`. `newsdoc/newsdoc.proto` is the only file the
fleet vendors. A `buf.yaml` this namespace did not write is still left alone —
buf's lint and breaking change rules live in that file — but it has to declare
the vendored root and exclude it from the module rooted in the repository, and
both targets now fail naming what is missing instead of reporting success and
leaving a workspace buf cannot resolve the import in.

**Behaviour change (generating needs the network):** buf and every plugin run
as a module of their own, which means a version query on every run, so
`GOPROXY=off` fails even with a warm module cache and generating offline does
not work. The `elephant-twirptools` image at least ran from a local pull;
"installs nothing" bought that at the price of the network, which is worth
knowing before a CI job is written to expect otherwise. `GOFLAGS` and
`GOTOOLCHAIN` are normalised for the generator invocations rather than
inherited: any `-mod` flag is dropped, since a repository that vendors its
dependencies would otherwise send the generators looking for themselves in its
`vendor` directory, and `GOTOOLCHAIN` is set to the pin.

Changes:

- New targets `rpc:generate`, `rpc:vendorProto` and `rpc:stub`. Configuration
  is the exported variables of the `rpc` package — `rpc.Twirp`,
  `rpc.VendorDir`, `rpc.ExtraProtoRoots` and
  `rpc.ElephantRPCOptions` — each with an environment variable that overrides
  it for a single run, which is what a CI job or a one-off regeneration uses
  rather than editing the magefile.
- `protoc-gen-elephant-rpc`, which emits the Connect adapters that put Connect
  on the plain protobuf service interface, is part of the plugin set, pinned
  to a pre-release commit of elephantine until that work is tagged.
  `ELEPHANT_RPC_PLUGIN` overrides the pin with a `module@version` or a module
  checkout, which is how the plugin is developed against a repository that
  generates with it. Its `interface`
  option, which makes it write the plain service interface itself to
  `service.rpc.go`, follows `rpc.Twirp`: `protoc-gen-twirp` owns that interface
  for as long as it is generated and the plugin takes it over when it is not,
  so a Connect-only repository's adapters compile with no configuration of its
  own. `rpc.ElephantRPCOptions` overrides that in either direction, except that
  asking for `interface=true` while Twirp is generated is refused. The plugin
  cannot be turned off: an empty pin is an error rather than a run that quietly
  leaves the adapters as they were.
- Whichever of `service.rpc.go` and `service.twirp.go` is not generated this
  time is deleted when an earlier configuration left it behind. They are the
  same declaration, so turning Twirp off used to leave a package with two of
  them, which elephant-public-api fixed by hand. Only a file carrying the
  generator's `// Code generated by` header is removed.
- `protoc-gen-twirp` is run out of a small module this package carries, written
  into a working directory for the length of a run, rather than as
  `go run <module>@<version>`. It is a `+incompatible` module with no `go.mod`
  of its own, so that resolved its dependencies afresh on every run.
- A `.proto` file that declares no service is compiled to messages and nothing
  else, and a `go_package` written as a relative path — which the `twirp:stub`
  template produced — no longer has to be edited: the Go import path of the
  generated package is derived from the module path and passed to the plugins.
  Connect generates into a subpackage and has to import the message package,
  which a relative path cannot be turned into. A declaration that does name an
  import path has to name that one, and generation says so rather than letting
  the mistake surface as uncompilable code. `rpc:stub` writes the full import
  path, and writes it into `[proto root]/[application]/v1/`, with the protobuf
  package `ttab.[application].v1` and a `go_package` that names the Go package
  separately from the path, since the last element of that path is the version.
- A repository that keeps its protos in the repository root can vendor an
  import: the `rpc/vendor` directory `rpc:vendorProto` creates no longer makes
  the targets believe the sources have moved to `rpc/`. The proto root is `rpc`
  only when that directory holds something other than the vendored protos.
- The `twirp` namespace is deprecated. It is otherwise unchanged and keeps
  working for as long as the image exists.
- Dependency upgrades: minio-go to v7.3.0, klauspost/compress to v1.20.0, and
  the golang.org/x modules.

## [v0.12.0] - 2026-09-05

**Behaviour change (schema dumps):** `sql:dumpSchema` now takes the dump with
Postgres 18's `pg_dump` regardless of the server it reads, and strips the
`-- Dumped from database version` and `-- Dumped by pg_dump version` header
lines. A project's `postgres/schema.sql` no longer depends on which Postgres
produced it, so the same DDL dumped from a 17 server and from an 18 server
gives a byte-identical file and moving a service to 18 is not a schema diff.
The first dump after upgrading drops those two header lines, which is one churn
commit per project.

**Behaviour change (local Postgres):** the local instances all publish port
5432 and no major version can read another's data directory, so `sql:postgres`
and `sql:postgres18` now stop whichever instance is running before starting
theirs. Previously `sql:postgres` only stopped the instance of the same name
and a second one failed on the port.

Changes:

- New target `sql:postgres18`, which starts Postgres 18 from
  `pgvector/pgvector:pg18`. It takes no name, unlike `sql:postgres`: the
  instance is `postgres18` and its data lives in `tt-mage/postgres18` under the
  platform data directory. Nothing else changes when you switch — the
  connection string, `sql:db`, `sql:migrate` and `sql:dumpSchema` are the same
  against both versions, and the databases in an instance are its own, so a
  project that moves recreates them there.
- Restarting a container no longer races its own removal. A container started
  with `--rm` keeps its name for a moment after `docker stop` returns, so the
  `docker run` that followed could fail with a name conflict. This affected
  `s3:minio` as much as the Postgres targets.
- The `covers` and `sql:librarySchema` examples point at
  `pg/joblock/schema`, following elephantine v0.28.0 moving the job lock
  migration there.

## [v0.11.3] - 2026-09-01

Changes:

- New target `sql:vendorAdd`, which declares a library's migration directory in
  `schema/vendor.json`. It resolves the module and reads the directory before
  writing anything, so a mistyped module path fails at the point of the mistake
  rather than as somebody else's `sql:vendorCheck` failure a couple of bumps
  later.
- `docs/schema-vendoring.md` is the overview the vendoring targets were
  missing: the problem, the copy path, the service workflow, what each check
  verdict means and what to do about it, and the rules for a library that ships
  migrations.

## [v0.11.2] - 2026-09-01

**Behaviour change (vendored migrations):** the provenance header a vendored
migration carries now ends with a `-- vendored-end` marker, and the body is
everything after it. The header used to be recognised as the comment lines
before the first statement, which cannot be told apart from a comment the
library migration itself opens with — so those lines were stripped, the copy
differed from its source the instant it was written, and `sql:vendorCheck`
reported an edit nobody had made. Run `sql:vendor` again to rewrite headers
written by an earlier version. (#16)

## [v0.11.1] - 2026-09-01

Changes:

- `sql:librarySchema` no longer writes the migration directory into the
  generated header, so the same operation on the same files no longer disagrees
  with itself depending on the path it was given, and
  `libschema.CheckFlattened` compares the SQL rather than the whole file. The
  check used to fail on a file that was perfectly current, which is the worst
  kind of failure for a drift check to have. (#15)

## [v0.11.0] - 2026-09-01

Changes:

- New targets `sql:vendor`, `sql:vendorCheck` and `sql:librarySchema`, which
  copy a library's tern migrations into a service's `./schema` and keep the
  copies honest. Both `sql:migrate` and elephant-platform's `setup db migrate`
  apply exactly the files in `./schema` and neither looks inside a dependency,
  so a migration embedded in a module is invisible to both — and the failure is
  quiet, since the service builds, tests and deploys before failing at runtime
  on a table nobody created. `sql:vendor` adds the migrations that are missing,
  numbering them after what is already there, and `sql:vendorCheck` fails when
  a declared library migration is not covered. The check is public as
  `libschema.Check` so a project can assert it from a test instead.

## [v0.10.1] - 2026-08-25

Changes:

- `docs:links` takes its file list from `git ls-files` inside a worktree, so
  ignore rules and a developer's global excludes are respected and a virtualenv
  or a vendored dependency no longer makes the target fail on links that are
  not ours to fix. Outside a worktree, or with no `git` available, it still
  walks the directory.

## [v0.10.0] - 2026-08-06

Changes:

- New target `docs:links`, which resolves every relative link and heading
  anchor in the repository's markdown and reports all of the problems rather
  than the first. Documentation that cross-references itself by section breaks
  quietly: a renamed heading leaves every inbound `#anchor` pointing at nothing
  and nothing in a build notices. Anchors are slugged GitHub's way, and
  headings inside fenced code blocks are not treated as headings. The checker
  is public as `doclint` so a project can run it from a test.
