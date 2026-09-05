# Changelog

Everything from v0.10.0 onwards is documented here; earlier releases are not
reconstructed. The entries are derived from the release tags, and the linked
pull requests hold the detail.

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
