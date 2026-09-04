// Package libschema handles tern migrations that a library ships and a
// service has to apply.
//
// The problem it solves: both `mage sql:migrate` and elephant-platform's
// `setup db migrate` apply exactly the tern files in a service's ./schema,
// and neither knows anything about a migration embedded in one of the
// service's dependencies. A library that needs a table of its own therefore
// has no way to get it created — the service's own migration set is the only
// contract either tool honours.
//
// So the library's migrations are copied into the service's ./schema, and
// this package is what keeps the copies honest: Vendor adds the ones that are
// missing, and Check fails when a declared library migration is not covered.
// The deploy path needs no knowledge of any of it.
package libschema

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/ttab/mage/internal"
)

// ConfigName is the file in the schema directory that declares which
// libraries a service takes migrations from.
const ConfigName = "vendor.json"

// ternSeparator divides the "up" half of a tern migration from the "down"
// half. Everything above it is what a flattened schema keeps.
const ternSeparator = "---- create above / drop below ----"

// Config is the contents of schema/vendor.json. A service has to say which
// libraries it takes schema from: inferring it from the module graph would
// mean a dependency could add a table to a database without anybody in the
// service agreeing to it.
type Config struct {
	Libraries []Library `json:"libraries"`
}

// Library is one dependency's migration directory.
type Library struct {
	// Module is the Go module path, e.g. "github.com/ttab/howdah".
	Module string `json:"module"`
	// Dir is the migration directory within the module, e.g.
	// "tokenstore/pgstore/schema".
	Dir string `json:"dir"`
}

func (l Library) String() string {
	return l.Module + " " + l.Dir
}

// LoadConfig reads the vendoring declaration from a schema directory. A
// missing file is not an error: it means the service vendors nothing, which
// is the normal case.
func LoadConfig(schemaDir string) (Config, error) {
	var conf Config

	data, err := os.ReadFile(filepath.Join(schemaDir, ConfigName))
	if errors.Is(err, os.ErrNotExist) {
		return conf, nil
	} else if err != nil {
		return conf, fmt.Errorf("read %s: %w", ConfigName, err)
	}

	err = json.Unmarshal(data, &conf)
	if err != nil {
		return conf, fmt.Errorf("parse %s: %w", ConfigName, err)
	}

	for i, lib := range conf.Libraries {
		if lib.Module == "" || lib.Dir == "" {
			return conf, fmt.Errorf(
				"library %d in %s needs both a module and a dir",
				i, ConfigName)
		}
	}

	return conf, nil
}

// AddLibrary declares a library's migration directory in a schema
// directory's vendor.json, creating the file if this is the first one.
//
// It resolves the module and reads the directory before writing anything, so
// a typo fails here rather than in the next person's check run. The return
// value says whether it added the library; an entry that is already there is
// not an error, so the call is safe to repeat.
//
// It does not copy anything — run Vendor for that.
func AddLibrary(schemaDir string, lib Library) (bool, error) {
	return addLibrary(schemaDir, lib, GoModuleDir)
}

func addLibrary(
	schemaDir string, lib Library, moduleDir func(string) (string, error),
) (bool, error) {
	if lib.Module == "" || lib.Dir == "" {
		return false, errors.New("both a module and a dir are needed")
	}

	// The dir is relative to the module root and slash-separated: it names a
	// path inside the read-only module cache, and it is read back on
	// whatever platform the next developer runs.
	lib.Dir = path.Clean(filepath.ToSlash(lib.Dir))

	if path.IsAbs(lib.Dir) || lib.Dir == ".." ||
		strings.HasPrefix(lib.Dir, "../") {
		return false, fmt.Errorf(
			"%q is not a path within the module", lib.Dir)
	}

	info, err := os.Stat(schemaDir)
	if err != nil {
		return false, fmt.Errorf(
			"read %s: %w (run this from the service root)", schemaDir, err)
	}

	if !info.IsDir() {
		return false, fmt.Errorf("%s is not a directory", schemaDir)
	}

	conf, err := LoadConfig(schemaDir)
	if err != nil {
		return false, err
	}

	for _, existing := range conf.Libraries {
		if existing.Module == lib.Module && existing.Dir == lib.Dir {
			return false, nil
		}
	}

	_, err = readSources(Config{Libraries: []Library{lib}}, moduleDir)
	if err != nil {
		return false, err
	}

	conf.Libraries = append(conf.Libraries, lib)

	data, err := json.MarshalIndent(conf, "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshal %s: %w", ConfigName, err)
	}

	confPath := filepath.Join(schemaDir, ConfigName)

	err = os.WriteFile(confPath, append(data, '\n'), 0o600)
	if err != nil {
		return false, fmt.Errorf("write %s: %w", confPath, err)
	}

	return true, nil
}

// source is one migration a library ships.
type source struct {
	lib     Library
	name    string
	content []byte
	sum     string
}

func (s source) ref() string {
	return s.lib.Module + " " + filepath.Join(s.lib.Dir, s.name)
}

// local is one migration file in the service's own schema directory.
type local struct {
	path   string
	number int
	body   []byte

	// vendoredFrom and vendoredSum are the provenance of a file this
	// package wrote.
	vendoredFrom string
	vendoredSum  string

	// covers holds the source refs a human has asserted this file already
	// does the work of. It is how a service that hand-wrote the DDL years
	// ago passes the check without the file having to match byte for byte
	// — which matters because some of them buried it in a migration that
	// does half a dozen other things.
	covers []string
}

var (
	vendoredFromRe = regexp.MustCompile(`(?m)^--\s*vendored-from:\s*(\S+)\s+(\S+)\s*$`)
	vendoredSumRe  = regexp.MustCompile(`(?m)^--\s*vendored-sha256:\s*([0-9a-f]{64})\s*$`)
	coversRe       = regexp.MustCompile(`(?m)^--\s*covers:\s*(\S+)\s+(\S+)\s*$`)
	migrationRe    = regexp.MustCompile(`^(\d+)_.*\.sql$`)
)

func sum(content []byte) string {
	h := sha256.Sum256(content)

	return hex.EncodeToString(h[:])
}

// readLocal reads the service's own migration files.
func readLocal(schemaDir string) ([]local, error) {
	entries, err := os.ReadDir(schemaDir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", schemaDir, err)
	}

	var locals []local

	for _, e := range entries {
		if e.IsDir() {
			continue
		}

		m := migrationRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}

		number, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, fmt.Errorf("parse the number of %q: %w", e.Name(), err)
		}

		path := filepath.Join(schemaDir, e.Name())

		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}

		l := local{path: path, number: number, body: data}

		if fm := vendoredFromRe.FindSubmatch(data); fm != nil {
			l.vendoredFrom = string(fm[1]) + " " + string(fm[2])
		}

		if sm := vendoredSumRe.FindSubmatch(data); sm != nil {
			l.vendoredSum = string(sm[1])
		}

		for _, cm := range coversRe.FindAllSubmatch(data, -1) {
			l.covers = append(l.covers,
				string(cm[1])+" "+string(cm[2]))
		}

		locals = append(locals, l)
	}

	return locals, nil
}

// readSources reads what the declared libraries ship, resolving each module
// to its directory on disk.
func readSources(conf Config, moduleDir func(string) (string, error)) ([]source, error) {
	var sources []source

	for _, lib := range conf.Libraries {
		dir, err := moduleDir(lib.Module)
		if err != nil {
			return nil, fmt.Errorf("locate %s: %w", lib.Module, err)
		}

		migrationsDir := filepath.Join(dir, lib.Dir)

		entries, err := os.ReadDir(migrationsDir)
		if err != nil {
			return nil, fmt.Errorf(
				"read the migrations of %s: %w", lib, err)
		}

		var found int

		for _, e := range entries {
			if e.IsDir() || !migrationRe.MatchString(e.Name()) {
				continue
			}

			data, err := os.ReadFile(filepath.Join(migrationsDir, e.Name()))
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", e.Name(), err)
			}

			sources = append(sources, source{
				lib:     lib,
				name:    e.Name(),
				content: data,
				sum:     sum(data),
			})

			found++
		}

		if found == 0 {
			return nil, fmt.Errorf(
				"%s declares no migrations — check the dir", lib)
		}
	}

	return sources, nil
}

// ProblemKind says what is wrong, because the fixes are not the same and one
// of them is emphatically not "run vendor again".
type ProblemKind string

const (
	// NotVendored is the ordinary case: a library shipped a migration this
	// service has not taken yet. `mage sql:vendor` fixes it.
	NotVendored ProblemKind = "not vendored"

	// LocalCopyEdited means somebody edited a vendored file. The copy is
	// managed, so the edit belongs upstream in the library.
	LocalCopyEdited ProblemKind = "vendored copy edited"

	// LibraryHistoryEdited means the library changed a migration this
	// service has already vendored, and very likely already applied.
	//
	// This one must NOT be fixed by re-vendoring. tern records a migration
	// as applied by number, so rewriting the file changes nothing in any
	// database that has already run it — the change would silently never
	// land, which is worse than the current state. The library has to add a
	// new migration instead.
	LibraryHistoryEdited ProblemKind = "library rewrote an applied migration"

	// SourceGone means a vendored file names a library migration that no
	// longer exists. Also the library's problem: a migration that has been
	// applied somewhere cannot be withdrawn.
	SourceGone ProblemKind = "vendored from a migration that no longer exists"
)

// Problem is one thing the check found.
type Problem struct {
	Kind ProblemKind
	// Ref is the library migration concerned, as "<module> <path>".
	Ref string
	// Path is the file in the service's schema directory, where there is
	// one.
	Path string
	// Detail carries what to do about it.
	Detail string
}

func (p Problem) String() string {
	s := string(p.Kind) + ": " + p.Ref

	if p.Path != "" {
		s += " (" + p.Path + ")"
	}

	if p.Detail != "" {
		s += "\n    " + p.Detail
	}

	return s
}

// Check reports every declared library migration that the service's schema
// directory does not cover. An empty result means the deploy path will build
// every table the libraries need.
func Check(schemaDir string) ([]Problem, error) {
	return check(schemaDir, GoModuleDir)
}

func check(
	schemaDir string, moduleDir func(string) (string, error),
) ([]Problem, error) {
	conf, err := LoadConfig(schemaDir)
	if err != nil {
		return nil, err
	}

	if len(conf.Libraries) == 0 {
		return nil, nil
	}

	sources, err := readSources(conf, moduleDir)
	if err != nil {
		return nil, err
	}

	locals, err := readLocal(schemaDir)
	if err != nil {
		return nil, err
	}

	byRef := make(map[string]source, len(sources))
	for _, s := range sources {
		byRef[s.ref()] = s
	}

	var problems []Problem

	// A vendored file whose source has gone, or been rewritten under it.
	for _, l := range locals {
		if l.vendoredFrom == "" {
			continue
		}

		src, ok := byRef[l.vendoredFrom]
		if !ok {
			problems = append(problems, Problem{
				Kind: SourceGone,
				Ref:  l.vendoredFrom,
				Path: l.path,
				Detail: "the library no longer ships it. A migration that" +
					" has been applied cannot be withdrawn — restore it" +
					" upstream, or drop this file only if no database has" +
					" run it.",
			})

			continue
		}

		if l.vendoredSum != src.sum {
			problems = append(problems, Problem{
				Kind: LibraryHistoryEdited,
				Ref:  l.vendoredFrom,
				Path: l.path,
				Detail: "do NOT re-vendor. tern records a migration as" +
					" applied by number, so rewriting this file changes" +
					" nothing in a database that has already run it. The" +
					" library has to add a new migration instead.",
			})

			continue
		}

		if body := vendoredBody(l.body); body != string(src.content) {
			problems = append(problems, Problem{
				Kind: LocalCopyEdited,
				Ref:  l.vendoredFrom,
				Path: l.path,
				Detail: "this copy is managed. Make the change in the" +
					" library as a new migration and vendor that.",
			})
		}
	}

	// A library migration nothing covers.
	covered := make(map[string]bool)

	for _, l := range locals {
		if l.vendoredFrom != "" {
			covered[l.vendoredFrom] = true
		}

		for _, ref := range l.covers {
			covered[ref] = true
		}
	}

	for _, s := range sources {
		if covered[s.ref()] {
			continue
		}

		problems = append(problems, Problem{
			Kind: NotVendored,
			Ref:  s.ref(),
			Detail: "run `mage sql:vendor`. If a migration here already does" +
				" this work by hand, say so in that file instead, with a" +
				"\n    -- covers: " + s.ref() + "\n    comment.",
		})
	}

	return problems, nil
}

// headerEnd terminates the provenance header.
//
// The header cannot be recognised as "the comments before the first
// statement": a library's migration very often opens with a comment of its
// own explaining the table, and stripping that too made the copy differ from
// its source the moment it was written. A marker is unambiguous, and removing
// it is itself an edit.
const headerEnd = "-- vendored-end"

// vendoredBody returns a vendored file's contents with the provenance header
// stripped, which is what has to match the library's own file.
func vendoredBody(data []byte) string {
	_, body, found := strings.Cut(string(data), headerEnd+"\n")
	if !found {
		return ""
	}

	return strings.TrimPrefix(body, "\n")
}

// GoModuleDir resolves a module path to its directory on disk, which for a
// dependency is the read-only module cache. Only ever read from.
func GoModuleDir(module string) (string, error) {
	out, err := internal.OutputSilent("go", "list", "-m", "-f", "{{.Dir}}", module)
	if err != nil {
		return "", fmt.Errorf(
			"go list -m %s: %w (is it a dependency of this module?)",
			module, err)
	}

	dir := strings.TrimSpace(out)
	if dir == "" {
		return "", fmt.Errorf(
			"%s resolves to no directory — run go mod download", module)
	}

	return dir, nil
}

// Added is one file Vendor wrote.
type Added struct {
	Ref  string
	Path string
}

// Vendor copies every declared library migration the schema directory does
// not yet cover into it, numbered after the migrations already there.
//
// It only ever adds. A file it has written before is left alone even when the
// library's copy has changed, because rewriting an applied migration is a
// silent no-op in every database that has run it — Check reports that case
// and the fix is a new migration upstream.
func Vendor(schemaDir string) ([]Added, error) {
	return vendor(schemaDir, GoModuleDir)
}

func vendor(
	schemaDir string, moduleDir func(string) (string, error),
) ([]Added, error) {
	conf, err := LoadConfig(schemaDir)
	if err != nil {
		return nil, err
	}

	if len(conf.Libraries) == 0 {
		return nil, nil
	}

	sources, err := readSources(conf, moduleDir)
	if err != nil {
		return nil, err
	}

	locals, err := readLocal(schemaDir)
	if err != nil {
		return nil, err
	}

	covered := make(map[string]bool)
	next := 1

	for _, l := range locals {
		if l.vendoredFrom != "" {
			covered[l.vendoredFrom] = true
		}

		for _, ref := range l.covers {
			covered[ref] = true
		}

		if l.number >= next {
			next = l.number + 1
		}
	}

	var added []Added

	for _, s := range sources {
		if covered[s.ref()] {
			continue
		}

		name := fmt.Sprintf("%03d_%s_%s",
			next, moduleSlug(s.lib.Module), trimNumber(s.name))
		path := filepath.Join(schemaDir, name)

		header := fmt.Sprintf(
			"-- vendored-from: %s\n"+
				"-- vendored-sha256: %s\n"+
				"--\n"+
				"-- Copied from a library so that this service's own migration\n"+
				"-- set is complete: `mage sql:migrate` and elephant-platform's\n"+
				"-- `setup db migrate` both apply exactly ./schema, and neither\n"+
				"-- knows about a migration inside a dependency.\n"+
				"--\n"+
				"-- Managed by `mage sql:vendor`. Do not edit: change it in the\n"+
				"-- library, as a new migration.\n"+
				headerEnd+"\n\n",
			s.ref(), s.sum)

		err := os.WriteFile(path, append([]byte(header), s.content...), 0o600)
		if err != nil {
			return added, fmt.Errorf("write %s: %w", path, err)
		}

		added = append(added, Added{Ref: s.ref(), Path: path})
		next++
	}

	return added, nil
}

// moduleSlug turns a module path into something usable in a filename.
func moduleSlug(module string) string {
	parts := strings.Split(module, "/")

	return strings.NewReplacer(".", "_", "-", "_").Replace(parts[len(parts)-1])
}

// trimNumber drops a library migration's own number, since the copy is
// numbered by the service's sequence rather than the library's.
func trimNumber(name string) string {
	if m := migrationRe.FindStringSubmatch(name); m != nil {
		return strings.TrimPrefix(name, m[1]+"_")
	}

	return name
}

// generatedHeader marks a flattened schema so that nobody edits it by hand.
//
// It deliberately names no path. The header used to carry the migration
// directory, which made the output depend on where the command was run from:
// `mage sql:librarySchema pg/joblock/schema pg/joblock/schema.sql` at a
// repository root and CheckFlattened("schema", "schema.sql") from inside the
// package are the same operation on the same files, and they disagreed.
const generatedHeader = "-- Generated from this package's tern migrations by\n" +
	"-- `mage sql:librarySchema`. Do not edit.\n\n"

// Flatten writes the "create above" halves of a migration directory into a
// single schema file, in migration order.
//
// It is for a library that ships migrations and also needs a flat schema for
// sqlc to read. Without it the library holds the same DDL twice with nothing
// keeping the copies together, which is the drift this package exists to stop
// — one level further in.
func Flatten(migrationsDir, out string) error {
	schema, err := flatten(migrationsDir)
	if err != nil {
		return err
	}

	err = os.WriteFile(out, []byte(schema), 0o600)
	if err != nil {
		return fmt.Errorf("write %s: %w", out, err)
	}

	return nil
}

// CheckFlattened reports whether the schema file still matches the
// migrations. Wire it into a test so a new migration cannot be added without
// the schema sqlc reads following it.
func CheckFlattened(migrationsDir, out string) error {
	want, err := flatten(migrationsDir)
	if err != nil {
		return err
	}

	got, err := os.ReadFile(out)
	if err != nil {
		return fmt.Errorf("read %s: %w", out, err)
	}

	// Compare the SQL rather than the whole file, so that a change to the
	// header wording does not read as schema drift in every library that
	// has generated one.
	if statements(string(got)) != statements(want) {
		return fmt.Errorf(
			"%s no longer matches the migrations in %s: run `mage sql:librarySchema`",
			out, migrationsDir)
	}

	return nil
}

// statements drops the generated header, leaving what actually has to match.
func statements(schema string) string {
	lines := strings.SplitAfter(schema, "\n")

	for i, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "--") {
			continue
		}

		return strings.Join(lines[i:], "")
	}

	return ""
}

func flatten(migrationsDir string) (string, error) {
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", migrationsDir, err)
	}

	names := make([]string, 0, len(entries))

	for _, e := range entries {
		if !e.IsDir() && migrationRe.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}

	if len(names) == 0 {
		return "", fmt.Errorf("no migrations in %s", migrationsDir)
	}

	// os.ReadDir sorts by filename, and a zero-padded number sorts in
	// migration order, which is the order the statements have to appear in.
	var b strings.Builder

	b.WriteString(generatedHeader)

	for i, name := range names {
		data, err := os.ReadFile(filepath.Join(migrationsDir, name))
		if err != nil {
			return "", fmt.Errorf("read %s: %w", name, err)
		}

		up, _, found := strings.Cut(string(data), ternSeparator)
		if !found {
			return "", fmt.Errorf(
				"%s has no %q separator, so its up half cannot be told from"+
					" its down half", name, ternSeparator)
		}

		if i > 0 {
			b.WriteString("\n")
		}

		b.WriteString(strings.TrimSpace(up))
		b.WriteString("\n")
	}

	return b.String(), nil
}
