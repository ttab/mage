package libschema_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ttab/mage/libschema"
)

const libMigration = `CREATE TABLE howdah_session(id bytea PRIMARY KEY);

---- create above / drop below ----

DROP TABLE howdah_session;
`

// fixture lays out a service schema directory and a library that ships one
// migration, and returns a module resolver for the library.
func fixture(t *testing.T, appFiles map[string]string) (string, func(string) (string, error)) {
	t.Helper()

	root := t.TempDir()

	schemaDir := filepath.Join(root, "schema")
	libRoot := filepath.Join(root, "lib")
	libDir := filepath.Join(libRoot, "tokenstore", "pgstore", "schema")

	for _, d := range []string{schemaDir, libDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	write := func(path, content string) {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	write(filepath.Join(libDir, "001_session.sql"), libMigration)
	write(filepath.Join(schemaDir, libschema.ConfigName),
		`{"libraries":[{"module":"github.com/ttab/howdah","dir":"tokenstore/pgstore/schema"}]}`)

	for name, content := range appFiles {
		write(filepath.Join(schemaDir, name), content)
	}

	return schemaDir, func(module string) (string, error) {
		if module != "github.com/ttab/howdah" {
			t.Fatalf("unexpected module %q", module)
		}

		return libRoot, nil
	}
}

func kinds(problems []libschema.Problem) []libschema.ProblemKind {
	var out []libschema.ProblemKind
	for _, p := range problems {
		out = append(out, p.Kind)
	}

	return out
}

func TestCheckReportsAnUnvendoredMigration(t *testing.T) {
	dir, mod := fixture(t, map[string]string{
		"001_app.sql": "CREATE TABLE thing();\n",
	})

	problems, err := libschema.CheckWith(dir, mod)
	if err != nil {
		t.Fatalf("check: %v", err)
	}

	if len(problems) != 1 || problems[0].Kind != libschema.NotVendored {
		t.Fatalf("got %v, want one libschema.NotVendored", kinds(problems))
	}

	// The message has to name the escape hatch, because for a service that
	// wrote the DDL by hand years ago running vendor is the wrong fix.
	if !strings.Contains(problems[0].Detail, "-- covers:") {
		t.Errorf("the detail does not mention the covers comment: %q",
			problems[0].Detail)
	}
}

func TestVendorThenCheckIsClean(t *testing.T) {
	dir, mod := fixture(t, map[string]string{
		"001_app.sql":  "CREATE TABLE thing();\n",
		"007_more.sql": "CREATE TABLE other();\n",
	})

	added, err := libschema.VendorWith(dir, mod)
	if err != nil {
		t.Fatalf("vendor: %v", err)
	}

	if len(added) != 1 {
		t.Fatalf("added %d files, want 1", len(added))
	}

	// Numbered after what is already there, not after the library's own
	// numbering.
	if base := filepath.Base(added[0].Path); !strings.HasPrefix(base, "008_") {
		t.Errorf("vendored as %q, want it numbered 008", base)
	}

	problems, err := libschema.CheckWith(dir, mod)
	if err != nil {
		t.Fatalf("check: %v", err)
	}

	if len(problems) != 0 {
		t.Fatalf("got %v, want none", kinds(problems))
	}

	// The copy has to carry the library's SQL verbatim, or tern applies
	// something other than what the library tested.
	data, err := os.ReadFile(added[0].Path)
	if err != nil {
		t.Fatalf("read the vendored file: %v", err)
	}

	if !strings.HasSuffix(string(data), libMigration) {
		t.Error("the vendored file does not end with the library's migration verbatim")
	}
}

func TestVendorIsIdempotent(t *testing.T) {
	dir, mod := fixture(t, nil)

	if _, err := libschema.VendorWith(dir, mod); err != nil {
		t.Fatalf("first vendor: %v", err)
	}

	added, err := libschema.VendorWith(dir, mod)
	if err != nil {
		t.Fatalf("second vendor: %v", err)
	}

	if len(added) != 0 {
		t.Errorf("the second run added %d files, want 0", len(added))
	}
}

func TestCoversSatisfiesTheCheck(t *testing.T) {
	// The case that makes the annotation necessary rather than convenient:
	// the DDL is one statement among several in a migration that does other
	// work, so no content comparison could ever find it.
	dir, mod := fixture(t, map[string]string{
		"001_messages.sql": `-- covers: github.com/ttab/howdah tokenstore/pgstore/schema/001_session.sql

CREATE TABLE "user"();
CREATE TABLE message();
CREATE TABLE howdah_session(id bytea PRIMARY KEY);
`,
	})

	problems, err := libschema.CheckWith(dir, mod)
	if err != nil {
		t.Fatalf("check: %v", err)
	}

	if len(problems) != 0 {
		t.Fatalf("got %v, want none", kinds(problems))
	}
}

func TestLibraryRewritingAnAppliedMigrationIsNotAVendorProblem(t *testing.T) {
	dir, mod := fixture(t, nil)

	if _, err := libschema.VendorWith(dir, mod); err != nil {
		t.Fatalf("vendor: %v", err)
	}

	// The library edits a migration this service has already taken, and
	// very likely already applied.
	libFile := filepath.Join(
		filepath.Dir(dir), "lib", "tokenstore", "pgstore", "schema", "001_session.sql")

	err := os.WriteFile(libFile,
		[]byte("CREATE TABLE howdah_session(id bytea PRIMARY KEY, extra text);\n\n"+
			"---- create above / drop below ----\n\nDROP TABLE howdah_session;\n"), 0o644)
	if err != nil {
		t.Fatalf("rewrite the library migration: %v", err)
	}

	problems, err := libschema.CheckWith(dir, mod)
	if err != nil {
		t.Fatalf("check: %v", err)
	}

	if len(problems) != 1 || problems[0].Kind != libschema.LibraryHistoryEdited {
		t.Fatalf("got %v, want one libschema.LibraryHistoryEdited", kinds(problems))
	}

	// Telling somebody to re-vendor here would be telling them to do a
	// silent no-op: tern will not re-run a migration it has recorded.
	if !strings.Contains(problems[0].Detail, "do NOT re-vendor") {
		t.Errorf("the detail does not warn against re-vendoring: %q",
			problems[0].Detail)
	}

	// And vendor must leave the file alone rather than rewriting history.
	added, err := libschema.VendorWith(dir, mod)
	if err != nil {
		t.Fatalf("vendor after the rewrite: %v", err)
	}

	if len(added) != 0 {
		t.Errorf("vendor rewrote %d file(s), want 0", len(added))
	}
}

func TestEditingAVendoredCopyIsReported(t *testing.T) {
	dir, mod := fixture(t, nil)

	added, err := libschema.VendorWith(dir, mod)
	if err != nil {
		t.Fatalf("vendor: %v", err)
	}

	data, err := os.ReadFile(added[0].Path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	err = os.WriteFile(added[0].Path,
		[]byte(strings.Replace(string(data), "id bytea", "id text", 1)), 0o644)
	if err != nil {
		t.Fatalf("edit the copy: %v", err)
	}

	problems, err := libschema.CheckWith(dir, mod)
	if err != nil {
		t.Fatalf("check: %v", err)
	}

	if len(problems) != 1 || problems[0].Kind != libschema.LocalCopyEdited {
		t.Fatalf("got %v, want one libschema.LocalCopyEdited", kinds(problems))
	}
}

func TestNoConfigMeansNothingToCheck(t *testing.T) {
	dir := t.TempDir()

	problems, err := libschema.CheckWith(dir, func(string) (string, error) {
		t.Fatal("the resolver must not be called without a config")

		return "", nil
	})
	if err != nil {
		t.Fatalf("check: %v", err)
	}

	if len(problems) != 0 {
		t.Errorf("got %v, want none", kinds(problems))
	}
}

func TestFlattenTakesTheUpHalvesInOrder(t *testing.T) {
	dir := t.TempDir()

	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	write("001_first.sql", "CREATE TABLE a();\n\n"+libschema.TernSeparator+"\n\nDROP TABLE a;\n")
	write("002_second.sql", "CREATE TABLE b();\n\n"+libschema.TernSeparator+"\n\nDROP TABLE b;\n")

	out := filepath.Join(dir, "schema.sql")

	if err := libschema.Flatten(dir, out); err != nil {
		t.Fatalf("flatten: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	got := string(data)

	if strings.Contains(got, "DROP TABLE") {
		t.Error("the flattened schema carries a down half")
	}

	if a, b := strings.Index(got, "CREATE TABLE a"), strings.Index(got, "CREATE TABLE b"); a < 0 || b < 0 || a > b {
		t.Errorf("statements are missing or out of migration order:\n%s", got)
	}

	if err := libschema.CheckFlattened(dir, out); err != nil {
		t.Errorf("the schema it just wrote does not check out: %v", err)
	}

	// A new migration has to make the check fail, or nothing keeps the two
	// in step.
	write("003_third.sql", "CREATE TABLE c();\n\n"+libschema.TernSeparator+"\n\nDROP TABLE c;\n")

	if err := libschema.CheckFlattened(dir, out); err == nil {
		t.Error("a new migration left the flattened schema checking out")
	}
}

func TestFlattenRefusesAMigrationWithoutASeparator(t *testing.T) {
	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "001_no_sep.sql"),
		[]byte("CREATE TABLE a();\n"), 0o644)
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := libschema.Flatten(dir, filepath.Join(dir, "out.sql")); err == nil {
		t.Error("flatten accepted a migration with no up/down separator")
	}
}
