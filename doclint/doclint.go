// Package doclint checks the links between the repository's markdown
// documents: that every relative link resolves to a file that exists, and
// that every "#anchor" names a heading the target document actually has.
//
// It is worth running wherever documentation is cross-referenced by
// section - design docs linking to each other, a README linking into them -
// because a renamed heading breaks an inbound link that nothing else would
// notice. Only relative links are checked; external URLs would make the
// check depend on the network and on other people's uptime.
//
// Anchors are resolved with GitHub's slug rules, since GitHub is where the
// documentation is read.
//
// Only the repository's own documentation is checked: in a git worktree the
// file list comes from git, so ignored files are left alone. Build output, a
// vendored dependency or a virtualenv under the repository root is full of
// markdown whose links are somebody else's to fix.
package doclint

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// Problem is a link that doesn't resolve.
type Problem struct {
	// File is the repository-relative path of the document holding the
	// link.
	File string
	// Line the link appears on, 1-indexed.
	Line int
	// Link is the target as written.
	Link string
	// Reason the link doesn't resolve.
	Reason string
}

func (p Problem) String() string {
	return fmt.Sprintf("%s:%d: %s: %s", p.File, p.Line, p.Link, p.Reason)
}

// skipDirs are never walked: nothing in them is documentation we own. They
// only matter outside a git worktree, where there are no ignore rules to go
// by.
var skipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
}

// CheckDir walks root for markdown files and checks the links in each,
// returning every problem found rather than stopping at the first. Paths in
// the returned problems are relative to root.
func CheckDir(root string) ([]Problem, error) {
	files, err := markdownFiles(root)
	if err != nil {
		return nil, fmt.Errorf("collect markdown files: %w", err)
	}

	c := checker{
		root:  root,
		cache: make(map[string]map[string]bool),
	}

	var problems []Problem

	for _, file := range files {
		p, err := c.checkFile(file)
		if err != nil {
			return nil, fmt.Errorf("check %s: %w", file, err)
		}

		problems = append(problems, p...)
	}

	return problems, nil
}

// markdownFiles returns the root-relative paths of the markdown files to
// check, letting git decide which files are the repository's own.
func markdownFiles(root string) ([]string, error) {
	entries, err := gitFiles(root)
	if err != nil {
		// Not a worktree, or no git to ask. Walking checks more files
		// than the listing would, never fewer, so falling back costs
		// noise rather than coverage.
		return walkMarkdownFiles(root)
	}

	var files []string

	for _, entry := range entries {
		if !strings.EqualFold(filepath.Ext(entry), ".md") {
			continue
		}

		// A tracked file can be absent from the worktree, and a
		// submodule is listed as a single entry that is a directory.
		info, err := os.Stat(filepath.Join(root, entry))
		if err != nil || !info.Mode().IsRegular() {
			continue
		}

		files = append(files, entry)
	}

	return files, nil
}

// gitFiles returns the paths git knows about under root, relative to root:
// the tracked files plus the untracked ones that no ignore rule covers. The
// ignore rules are git's own whole set, so a developer's global excludes count
// as well as the repository's .gitignore files.
func gitFiles(root string) ([]string, error) {
	cmd := exec.Command("git", "-C", root, "ls-files", "-z",
		"--cached", "--others", "--exclude-standard")

	out, err := cmd.Output()
	if err != nil {
		exit, ok := errors.AsType[*exec.ExitError](err)
		if ok {
			return nil, fmt.Errorf("list the files in %q: %w: %s",
				root, err, strings.TrimSpace(string(exit.Stderr)))
		}

		return nil, fmt.Errorf("list the files in %q: %w", root, err)
	}

	var files []string

	for entry := range strings.SplitSeq(string(out), "\x00") {
		if entry == "" {
			continue
		}

		files = append(files, filepath.FromSlash(entry))
	}

	return files, nil
}

func walkMarkdownFiles(root string) ([]string, error) {
	var files []string

	err := filepath.WalkDir(root, func(
		path string, d os.DirEntry, err error,
	) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("relativise %q: %w", path, err)
		}

		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}

			return nil
		}

		if strings.EqualFold(filepath.Ext(path), ".md") {
			files = append(files, rel)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %q: %w", root, err)
	}

	return files, nil
}

type checker struct {
	root string
	// cache holds the anchors of documents we have already parsed, keyed
	// by root-relative path. A document is both checked and linked to, so
	// without it every target is re-read once per inbound link.
	cache map[string]map[string]bool
}

func (c *checker) checkFile(file string) ([]Problem, error) {
	source, err := os.ReadFile(filepath.Join(c.root, file))
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	var problems []Problem

	for _, l := range parseLinks(string(source)) {
		reason, err := c.resolve(file, l.target)
		if err != nil {
			return nil, err
		}

		if reason == "" {
			continue
		}

		problems = append(problems, Problem{
			File:   file,
			Line:   l.line,
			Link:   l.target,
			Reason: reason,
		})
	}

	return problems, nil
}

// resolve returns the reason a link doesn't resolve, or an empty string when
// it does.
func (c *checker) resolve(from string, target string) (string, error) {
	if isExternal(target) {
		return "", nil
	}

	path, anchor, _ := strings.Cut(target, "#")

	// A bare "#anchor" is a link within the document itself.
	doc := from

	if path != "" {
		unescaped, err := url.PathUnescape(path)
		if err != nil {
			return fmt.Sprintf("undecodable path: %v", err), nil
		}

		doc = filepath.Join(filepath.Dir(from), unescaped)

		_, err = os.Stat(filepath.Join(c.root, doc))
		if os.IsNotExist(err) {
			return "no such file", nil
		}

		if err != nil {
			return "", fmt.Errorf("stat link target %q: %w", doc, err)
		}
	}

	// Anchors are only meaningful in documents we can read headings out
	// of; a fragment on anything else is somebody else's business.
	if anchor == "" || !strings.EqualFold(filepath.Ext(doc), ".md") {
		return "", nil
	}

	anchors, err := c.anchorsOf(doc)
	if err != nil {
		return "", err
	}

	if !anchors[anchor] {
		return "no such heading", nil
	}

	return "", nil
}

func (c *checker) anchorsOf(doc string) (map[string]bool, error) {
	if a, ok := c.cache[doc]; ok {
		return a, nil
	}

	source, err := os.ReadFile(filepath.Join(c.root, doc))
	if err != nil {
		return nil, fmt.Errorf("read link target %q: %w", doc, err)
	}

	a := parseAnchors(string(source))
	c.cache[doc] = a

	return a, nil
}

var externalScheme = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)

func isExternal(target string) bool {
	return externalScheme.MatchString(target) || strings.HasPrefix(target, "//")
}

type link struct {
	line   int
	target string
}

var (
	// linkPattern matches the inline link form, with an optional title:
	// [text](target) and [text](target "title").
	linkPattern = regexp.MustCompile(`\]\(\s*<?([^)<>\s]*)>?[^)]*\)`)
	// codeSpan matches an inline code span, which may contain anything
	// that looks like a link but is not one.
	codeSpan = regexp.MustCompile("`+[^`]*`+")
	// headingPattern matches an ATX heading, which may be indented by up
	// to three spaces.
	headingPattern = regexp.MustCompile(`^ {0,3}(#{1,6})\s+(.*?)\s*#*\s*$`)
	// fencePattern matches the opening or closing line of a fenced code
	// block.
	fencePattern = regexp.MustCompile("^ {0,3}(```|~~~)")
	// headingLink matches a markdown link inside a heading. GitHub renders
	// the heading before slugging it, so only the link text survives.
	headingLink = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	// htmlTag matches an HTML tag, which GitHub strips before slugging.
	htmlTag = regexp.MustCompile(`<[^>]*>`)
)

func parseLinks(source string) []link {
	var (
		links []link
		fence fenceState
		n     int
	)

	for line := range strings.SplitSeq(source, "\n") {
		n++

		if fence.step(line) {
			continue
		}

		line = codeSpan.ReplaceAllString(line, "")

		for _, m := range linkPattern.FindAllStringSubmatch(line, -1) {
			target := strings.TrimSpace(m[1])
			if target == "" {
				continue
			}

			links = append(links, link{
				line:   n,
				target: target,
			})
		}
	}

	return links
}

// parseAnchors returns the heading anchors of a document. Headings inside
// fenced code blocks are not headings - a shell comment in an example is the
// common case - and repeated headings are suffixed the way GitHub suffixes
// them.
func parseAnchors(source string) map[string]bool {
	var (
		anchors = make(map[string]bool)
		seen    = make(map[string]int)
		fence   fenceState
	)

	for line := range strings.SplitSeq(source, "\n") {
		if fence.step(line) {
			continue
		}

		m := headingPattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}

		s := Slug(m[2])

		if n, repeat := seen[s]; repeat {
			seen[s] = n + 1
			s = fmt.Sprintf("%s-%d", s, n)
		} else {
			seen[s] = 1
		}

		anchors[s] = true
	}

	return anchors
}

// fenceState tracks whether a line-by-line scan is currently inside a fenced
// code block. The zero value is outside one.
type fenceState struct {
	open   bool
	marker string
}

// step advances the state by one line and reports whether that line is code
// rather than prose - which the fence lines themselves are.
func (f *fenceState) step(line string) bool {
	m := fencePattern.FindStringSubmatch(line)
	if m == nil {
		return f.open
	}

	switch {
	case !f.open:
		f.open, f.marker = true, m[1]
	// A fence is only closed by one of its own kind, so a ``` inside a
	// ~~~ block stays content.
	case m[1] == f.marker:
		f.open, f.marker = false, ""
	}

	return true
}

// Slug renders a heading the way GitHub does when it generates the anchor to
// link to it: markdown and HTML are resolved away, the result is lowercased,
// everything that isn't a letter, a digit, an underscore, a hyphen or a
// space is dropped, and each remaining space becomes a hyphen.
//
// Note that spaces are replaced one for one rather than collapsed, so a
// heading with an em dash in it - "operations — the short version" - anchors
// with a double hyphen where the dash was.
func Slug(heading string) string {
	s := headingLink.ReplaceAllString(heading, "$1")
	s = htmlTag.ReplaceAllString(s, "")
	s = strings.ToLower(strings.TrimSpace(s))

	var b strings.Builder

	for _, r := range s {
		switch {
		case r == ' ':
			b.WriteRune('-')
		case r == '-' || r == '_' ||
			unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		}
	}

	return b.String()
}
