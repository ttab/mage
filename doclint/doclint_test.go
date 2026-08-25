package doclint_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ttab/mage/doclint"
)

func TestSlug(t *testing.T) {
	// The expectations are GitHub's, checked against rendered anchors
	// rather than derived from the implementation.
	cases := []struct {
		heading string
		want    string
	}{
		{"Erasure", "erasure"},
		{"Tiering: the lifecycle job", "tiering-the-lifecycle-job"},
		{
			"Overlay, re-materialization and rollups",
			"overlay-re-materialization-and-rollups",
		},
		{
			"13. Retention keeps the row and drops the body",
			"13-retention-keeps-the-row-and-drops-the-body",
		},
		// Backticks are punctuation and go, underscores stay.
		{"The `partition_anchor` field", "the-partition_anchor-field"},
		// A dash between spaces leaves both spaces behind, and each one
		// becomes a hyphen of its own.
		{"elephant-distribution — operations", "elephant-distribution--operations"},
		// A heading is rendered before it is slugged, so a link in one
		// contributes its text and not its target.
		{"See [the spec](https://example.com/x)", "see-the-spec"},
		{"A <em>tag</em> is stripped", "a-tag-is-stripped"},
		{"  Trailing and leading  ", "trailing-and-leading"},
		{"Non-ASCII: räksmörgås", "non-ascii-räksmörgås"},
	}

	for _, c := range cases {
		t.Run(c.heading, func(t *testing.T) {
			got := doclint.Slug(c.heading)
			if got != c.want {
				t.Errorf("Slug(%q) = %q, want %q",
					c.heading, got, c.want)
			}
		})
	}
}

func TestCheckDir(t *testing.T) {
	dir := t.TempDir()

	write(t, dir, "README.md", `# Title

[fine](docs/other.md#a-heading)
[fine, own document](#title)
[fine, repeated heading](docs/other.md#a-heading-1)
[missing file](docs/nope.md)
[missing heading](docs/other.md#not-a-heading)
[missing local heading](#nowhere)
[external, unchecked](https://example.com/nope#nope)
[fenced links are not links](docs/nope.md)

`+"```"+`
[in a fence](docs/nope.md)
`+"```"+`

Inline `+"`[in code](docs/nope.md)`"+` is not a link either.
`)

	write(t, dir, "docs/other.md", `# Other

## A heading

`+"```"+`sh
# Not a heading, a shell comment
`+"```"+`

## A heading
`)

	problems, err := doclint.CheckDir(dir)
	if err != nil {
		t.Fatalf("check dir: %v", err)
	}

	want := []string{
		"README.md:6: docs/nope.md: no such file",
		"README.md:7: docs/other.md#not-a-heading: no such heading",
		"README.md:8: #nowhere: no such heading",
		"README.md:10: docs/nope.md: no such file",
	}

	if len(problems) != len(want) {
		t.Fatalf("got %d problems, want %d:\n%s",
			len(problems), len(want), format(problems))
	}

	for i, w := range want {
		if got := problems[i].String(); got != w {
			t.Errorf("problem %d = %q, want %q", i, got, w)
		}
	}
}

// TestCheckDirShellCommentIsNotAnAnchor pins the trap the README walks into:
// its dev-setup blocks are full of "# Once: ..." comments, and treating them
// as headings would invent anchors that GitHub does not have.
func TestCheckDirShellCommentIsNotAnAnchor(t *testing.T) {
	dir := t.TempDir()

	write(t, dir, "README.md", "# Title\n\n"+
		"[link](#once-database-and-archive-bucket)\n\n"+
		"```\n# Once: database and archive bucket.\n```\n")

	problems, err := doclint.CheckDir(dir)
	if err != nil {
		t.Fatalf("check dir: %v", err)
	}

	if len(problems) != 1 {
		t.Fatalf("got %d problems, want 1:\n%s",
			len(problems), format(problems))
	}
}

// TestCheckDirSkipsIgnoredFiles pins the reason the file list comes from git:
// a virtualenv or a vendored dependency under the repository root brings its
// own markdown, whose broken links are not ours to fix.
func TestCheckDirSkipsIgnoredFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}

	dir := t.TempDir()

	write(t, dir, ".gitignore", ".venv/\n")
	write(t, dir, "README.md", "# Title\n\n[broken](nope.md)\n")
	write(t, dir, ".venv/vendored/README.md", "# Vendored\n\n[broken](nope.md)\n")

	gitInit(t, dir)

	problems, err := doclint.CheckDir(dir)
	if err != nil {
		t.Fatalf("check dir: %v", err)
	}

	if len(problems) != 1 || problems[0].File != "README.md" {
		t.Fatalf("got %d problems, want only the one in README.md:\n%s",
			len(problems), format(problems))
	}
}

func gitInit(t *testing.T, dir string) {
	t.Helper()

	// The commands are the minimum that makes ls-files work: a worktree,
	// and nothing that needs a user identity.
	for _, args := range [][]string{
		{"init"},
		{"add", "."},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)

		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v: %s",
				strings.Join(args, " "), err, out)
		}
	}
}

func write(t *testing.T, dir string, name string, content string) {
	t.Helper()

	path := filepath.Join(dir, name)

	err := os.MkdirAll(filepath.Dir(path), 0o700)
	if err != nil {
		t.Fatalf("create directory for %s: %v", name, err)
	}

	err = os.WriteFile(path, []byte(content), 0o600)
	if err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func format(problems []doclint.Problem) string {
	var s strings.Builder

	for _, p := range problems {
		s.WriteString(p.String())
		s.WriteString("\n")
	}

	return s.String()
}
