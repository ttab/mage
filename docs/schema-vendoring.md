# Schema vendoring

A library that owns a table has a problem: nothing in the deploy path will
create it.

This document explains why that is, what vendoring does about it, and how the
`sql:vendor*` and `sql:librarySchema` targets fit together. Read [The
problem](#the-problem) and [How it works](#how-it-works) once; after that the
per-target sections are the reference.

## The problem

Both `mage sql:migrate` and elephant-platform's `setup db migrate` apply
exactly the tern migrations in a service's `./schema`, and neither of them
looks inside a dependency. A service's own migration set is the only contract
either tool honours.

So a library that needs a table of its own has no way to get it created. It
cannot migrate the database itself either: a service must never migrate its
own schema at startup, since that turns every restart, scale-up and rollback
into a schema change.

The consequence is a failure that arrives late and quietly. A service bumps
the library past a new migration; it compiles, its tests pass, it deploys, and
then it fails at runtime on a table nobody created.

## How it works

The library's migrations are copied into the service's `./schema`, where every
path that migrates a database already looks. As far as tern is concerned they
are the service's own migrations, and the deploy path needs no knowledge of
any of it.

```
library repo                        service repo
------------                        ------------
tokenstore/pgstore/schema/          schema/
  001_session.sql  ---------+         001_app.sql
  002_handles.sql  -------+ |         002_more.sql
                          | |         vendor.json    <- declares the library
                          | +-----> 003_howdah_session.sql   <- vendored
                          +-------> 004_howdah_handles.sql   <- vendored
                                             |
                                             v
                            mage sql:migrate / setup db migrate
```

A copy is a thing that can rot, so the copies are managed rather than pasted.
Each one carries a header naming the library migration it came from and that
file's SHA-256, and there are three targets around them:

| Target | Job |
|---|---|
| [`sql:vendorAdd`](#declaring-the-library-sqlvendoradd) | declare which library a service takes migrations from |
| [`sql:vendor`](#making-the-copies-sqlvendor) | copy in every declared migration that is not covered yet |
| [`sql:vendorCheck`](#guarding-it-sqlvendorcheck) | fail the build when a copy is missing, edited, or stale |

Nothing is inferred from the module graph. Taking schema from a library is a
decision the service makes, recorded in
[`schema/vendor.json`](#schemavendorjson) — otherwise a dependency could add a
table to a service's database because somebody bumped a version.

## Taking a library's migrations into a service

Four steps, the last of them once per repository.

### Declaring the library: `sql:vendorAdd`

``` shell
go get github.com/ttab/howdah
mage sql:vendorAdd github.com/ttab/howdah tokenstore/pgstore/schema
```

The two arguments are the Go module path and the migration directory within
that module. The library's own documentation says which directory that is.

`sql:vendorAdd` writes the declaration into `schema/vendor.json`, creating the
file if this is the first library. It copies nothing. Run it from the service
root, where `./schema` is.

The module has to be a dependency already: the directory is resolved with `go
list -m` and read before anything is written, so a mistyped module path or a
directory that ships no migrations fails here rather than turning into
somebody else's `sql:vendorCheck` failure a bump or two later. Re-running it
for a library that is already declared says so and changes nothing.

### Making the copies: `sql:vendor`

``` shell
mage sql:vendor
```

For every migration a declared library ships that the service's `./schema`
does not cover yet, this writes a copy with a provenance header. The copies
are numbered after the migrations already in `./schema` rather than after the
library's own numbering, and named for the library:
`003_howdah_session.sql`. The whole file is copied, both halves of the tern
separator, so `mage sql:rollback` works over a vendored migration like any
other.

It only ever adds files. It never renumbers, never reorders, and never
rewrites a file it wrote before — not even when the library's copy has since
changed. That last one is deliberate: see [library rewrote an applied
migration](#library-rewrote-an-applied-migration).

### Applying and committing them

``` shell
mage sql:migrate
git add schema && git commit
```

A vendored migration that is not applied is its own trap, and an unapplied one
that is not committed is worse — the deploy path reads `./schema` from the
repository, so the copies have to be in the commit. `sql:vendor` prints the
reminder.

### Guarding it: `sql:vendorCheck`

``` shell
mage sql:vendorCheck
```

This fails when a library migration declared in `schema/vendor.json` is not
covered by the service's own migrations. Wire it into lint or CI once, and the
late runtime failure described in [The problem](#the-problem) becomes a build
failure at the commit that bumps the library:

``` yaml
- name: Check vendored migrations
  run: go run github.com/magefile/mage sql:vendorCheck
```

It reports [four distinct problems](#what-the-check-reports), because the fixes
differ and only one of them is `mage sql:vendor`.

## What a vendored migration looks like

``` sql
-- vendored-from: github.com/ttab/howdah tokenstore/pgstore/schema/001_session.sql
-- vendored-sha256: 4f2c…
--
-- Copied from a library so that this service's own migration
-- set is complete: `mage sql:migrate` and elephant-platform's
-- `setup db migrate` both apply exactly ./schema, and neither
-- knows about a migration inside a dependency.
--
-- Managed by `mage sql:vendor`. Do not edit: change it in the
-- library, as a new migration.
-- vendored-end

-- One row per session. The id is a hash of the handle, so a
-- database dump yields nothing anybody can log in with.
CREATE TABLE howdah_session(id bytea PRIMARY KEY);

---- create above / drop below ----

DROP TABLE howdah_session;
```

Everything above `-- vendored-end` is the header; everything below it is the
library's file, byte for byte. `sql:vendorCheck` compares both — the recorded
SHA-256 against what the library ships now, and the body against the library's
file — so it can tell an edited copy from a rewritten source.

The header is terminated by a marker rather than by "the comments before the
first statement", because a library migration very often opens with a comment
of its own explaining the table, as the one above does. Stripping that too
would make the copy differ from its source the moment it was written.
Removing the marker is itself an edit, and reads as one.

Do not edit a vendored file. It is managed, and the change belongs in the
library.

## What the check reports

### not vendored

A library shipped a migration this service has not taken yet. The ordinary
case, and the one every library bump produces.

Run `mage sql:vendor`. If the service already has the DDL by hand, see [DDL
the service already wrote by hand](#ddl-the-service-already-wrote-by-hand)
instead.

### vendored copy edited

Somebody changed a vendored file. The copy is managed, so whatever the change
was, it belongs upstream in the library as a new migration — an edit here is
lost the next time anybody looks at the provenance, and it means two services
running the same library disagree about what its tables look like.

Revert the file, make the change in the library, release it, and vendor the
new migration.

### library rewrote an applied migration

The library changed a migration this service has already vendored, and very
likely already applied.

**Do not fix this by re-vendoring, and do not "refresh" the copy by hand.**
tern records a migration as applied by number. Rewriting the file changes
nothing in any database that has already run it, so the change would silently
never land — a worse state than the one you started in, because the schema
now disagrees with the file that claims to define it.

The fix is upstream: the library adds a *new* migration that alters what the
old one created, and every service vendors that. Then this problem goes away
on its own, because the library's history stops being rewritten.

### vendored from a migration that no longer exists

A vendored file names a library migration the library no longer ships. Also
the library's problem, and for the same reason: a migration that has been
applied somewhere cannot be withdrawn.

Restore it upstream. Drop the local file only if you can establish that no
database has run it.

## DDL the service already wrote by hand

Plenty of services created a library's table themselves, years before the
library shipped a migration for it. Vendoring the library's copy on top of
that would fail on a table that already exists.

Such a service says so in the file that did the work:

``` sql
-- covers: github.com/ttab/elephantine pg/joblock/schema/001_job_lock.sql
```

The reference is the module path and the migration's path within the module,
exactly as the `vendored-from` header spells it. `sql:vendorCheck` treats the
migration as covered and `sql:vendor` skips it.

An assertion is the only thing that can work here, because the statement is
usually one of several in a migration that does half a dozen other things, so
there is nothing for a byte comparison to compare. That also means the
assertion is only as good as the person making it: check that the columns,
types and indexes really do match what the library expects before writing the
comment.

A file may carry as many `-- covers:` lines as it needs.

## Upgrading a library that ships migrations

``` shell
go get -u github.com/ttab/howdah
mage sql:vendor      # copies in whatever is new
mage sql:migrate     # applies it locally
go test ./...
git add schema && git commit
```

`sql:vendorCheck` in CI is what makes the second line non-optional. Without
it, the bump merges and the missing table is discovered in production.

## Shipping migrations from a library

If you are writing the library rather than consuming it:

* Put the migrations in a directory of their own, tern-numbered
  (`001_session.sql`), each with the `---- create above / drop below ----`
  separator. Document the path, since consumers pass it to `sql:vendorAdd`.
* Embed the directory so the library's own tests can migrate a scratch
  database with `eltest`.
* Generate the flat schema sqlc reads from those same migrations, rather than
  maintaining it separately.

### The flat schema sqlc reads: `sql:librarySchema`

A library that ships migrations usually also needs a flat schema file for sqlc
to read. Holding the same DDL twice, with nothing keeping the copies together,
is exactly the drift vendoring exists to stop — one level further in.

``` shell
mage sql:librarySchema pg/joblock/schema pg/joblock/schema.sql
```

That writes the "create above" halves of every migration in
`pg/joblock/schema` into `pg/joblock/schema.sql`, in migration order, under a
header saying it is generated. A migration without the separator is an error,
since its up half cannot be told from its down half.

Wire the same comparison into a test so a new migration cannot land without
the schema sqlc reads following it:

``` go
func TestSchemaMatchesMigrations(t *testing.T) {
	err := libschema.CheckFlattened("schema", "schema.sql")
	if err != nil {
		t.Fatal(err)
	}
}
```

The comparison is on the SQL, not the whole file, so a change to the generated
header's wording does not read as schema drift.

### Rules for a library's migrations

The library does not control where its migrations land in a consumer's
sequence, and cannot see the consumer's schema. Two things follow:

* **A migration must not assume anything about the service's own schema.** Its
  position in the sequence differs per service, and depends on when that
  service adopted the library. The library's own migrations do keep their
  relative order, so a later one may build on an earlier one.
* **Prefix the objects.** `howdah_session`, not `session`. The tables land in
  the service's database next to the service's own, and a collision surfaces
  as a migration that fails to apply in somebody else's repository.

And one thing that follows from tern recording migrations by number:

* **A released migration is immutable.** Never edit one, never renumber one,
  never delete one. Change what it created by adding a new migration. The
  [check](#library-rewrote-an-applied-migration) reports a library that breaks
  this, in every consuming service at once.

## `schema/vendor.json`

``` json
{
  "libraries": [
    {
      "module": "github.com/ttab/howdah",
      "dir": "tokenstore/pgstore/schema"
    }
  ]
}
```

`module` is the Go module path and `dir` is the migration directory within
that module. Both are required. `sql:vendorAdd` maintains the file, but it is
plain enough to edit by hand.

A missing file means the service vendors nothing, which is the normal case and
not an error. Commit the file: it is the record of what the service has agreed
to take, and both `sql:vendor` and `sql:vendorCheck` read it and nothing else.

## Calling it from Go instead

The targets are thin wrappers around
[`libschema`](https://pkg.go.dev/github.com/ttab/mage/libschema), which is
pure stdlib plus `go list` — no Docker, nothing to install. A service that
would rather fail in `go test` than in a separate lint step can call it
directly:

``` go
func TestVendoredMigrations(t *testing.T) {
	problems, err := libschema.Check("schema")
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range problems {
		t.Error(p)
	}
}
```

`libschema.Vendor`, `libschema.AddLibrary`, `libschema.Flatten` and
`libschema.CheckFlattened` are the rest of it.
