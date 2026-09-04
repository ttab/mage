package sql

import (
	"fmt"
	"os"
	"strings"

	"github.com/ttab/mage/libschema"
)

// schemaDir is where both `mage sql:migrate` and elephant-platform's
// `setup db migrate` look for a service's tern migrations. That agreement is
// what makes vendoring work: a copy placed here is applied by every path,
// with no tooling change anywhere.
const schemaDir = "schema"

// Vendor copies the migrations declared in schema/vendor.json out of their
// libraries and into ./schema, so that the service's own migration set is
// complete.
//
// It only ever adds files. See VendorCheck for the case it deliberately will
// not fix.
func Vendor() error {
	added, err := libschema.Vendor(schemaDir)
	if err != nil {
		return fmt.Errorf("vendor library migrations: %w", err)
	}

	if len(added) == 0 {
		_, _ = fmt.Fprintln(os.Stdout, "nothing to vendor")
	}

	for _, a := range added {
		_, _ = fmt.Fprintf(os.Stdout, "vendored %s\n     to %s\n", a.Ref, a.Path)
	}

	// Adding a migration and not applying it is its own trap, so say what
	// is left rather than leaving it to be discovered on deploy.
	if len(added) > 0 {
		_, _ = fmt.Fprintln(os.Stdout,
			"\nRun `mage migrate` (or `mage sql:migrate`) to apply them, and"+
				" commit them — the deploy path reads ./schema from the repository.")
	}

	return nil
}

// VendorAdd declares a library's migration directory in schema/vendor.json,
// so that `mage sql:vendor` starts copying its migrations in.
//
// For a library whose migrations live in "tokenstore/pgstore/schema", that is:
//
//	mage sql:vendorAdd github.com/ttab/howdah tokenstore/pgstore/schema
//
// The declaration is explicit rather than inferred from the module graph: a
// dependency must not be able to add a table to a service's database without
// somebody in the service agreeing to it.
func VendorAdd(module string, dir string) error {
	lib := libschema.Library{Module: module, Dir: dir}

	added, err := libschema.AddLibrary(schemaDir, lib)
	if err != nil {
		return fmt.Errorf("declare %s: %w", lib, err)
	}

	if !added {
		_, _ = fmt.Fprintf(os.Stdout,
			"%s is already declared in %s/%s\n",
			lib, schemaDir, libschema.ConfigName)

		return nil
	}

	_, _ = fmt.Fprintf(os.Stdout,
		"added %s to %s/%s\n\nRun `mage sql:vendor` to copy its migrations into ./%s.\n",
		lib, schemaDir, libschema.ConfigName, schemaDir)

	return nil
}

// VendorCheck fails when a library migration declared in schema/vendor.json
// is not covered by this service's own migrations.
//
// Wire it into lint or a test. The failure it exists to prevent is quiet: a
// service that bumps a library past a new migration builds, tests, deploys
// and then fails at runtime on a table nobody created, because neither
// `sql:migrate` nor `setup db migrate` looks inside a dependency.
func VendorCheck() error {
	problems, err := libschema.Check(schemaDir)
	if err != nil {
		return fmt.Errorf("check vendored migrations: %w", err)
	}

	if len(problems) == 0 {
		return nil
	}

	var b strings.Builder

	fmt.Fprintf(&b, "%d problem(s) with the vendored migrations:\n\n",
		len(problems))

	for _, p := range problems {
		fmt.Fprintf(&b, "  %s\n\n", p)
	}

	fmt.Fprint(os.Stderr, b.String())

	return fmt.Errorf("%d vendored migration problem(s)", len(problems))
}

// LibrarySchema writes the flat schema sqlc reads from a library's own tern
// migrations, so the library does not hold the same DDL twice.
//
// For a library whose migrations live in "pg/joblock/schema" and whose sqlc
// input is "pg/joblock/schema.sql", that is:
//
//	mage sql:librarySchema pg/joblock/schema pg/joblock/schema.sql
func LibrarySchema(migrationsDir, out string) error {
	err := libschema.Flatten(migrationsDir, out)
	if err != nil {
		return fmt.Errorf("write the library schema: %w", err)
	}

	_, _ = fmt.Fprintf(os.Stdout,
		"wrote %s from the migrations in %s\n", out, migrationsDir)

	return nil
}
