package rpc

import (
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/ttab/mage/internal"
)

// Lint checks the repository's own service declarations against buf's lint
// rules, with the pinned buf and the rules the workspace configuration
// names — buf's STANDARD set unless a hand-written buf.yaml says otherwise.
//
// Only the discovered declarations are checked. A vendored proto is compiled
// as an import, and its rules and its exemptions belong to the repository it
// was vendored out of.
func Lint() error {
	_, services, env, err := prepare()
	if err != nil {
		return err
	}

	err = buf(env, append([]string{"lint"}, pathArgs(services)...)...)
	if err != nil {
		return fmt.Errorf("run buf lint: %w", err)
	}

	return nil
}

// Breaking checks the repository's own service declarations against an
// earlier state of them, which is BreakingAgainst and by default the tip of
// this repository's main branch. RPC_BREAKING_AGAINST overrides it with any
// buf input for a single run, which is what a build comparing against a
// release tag or another branch uses:
//
//	RPC_BREAKING_AGAINST=.git#tag=v1.4.0 mage rpc:breaking
//
// A branch a git input names is resolved against the checkout it runs in: a
// checkout that has the branch only as a remote-tracking ref, which is what a
// CI job's is, compares against "origin/<branch>". What no checkout can do
// without is the commit itself, so a build that fetches nothing but the commit
// under test — actions/checkout's default depth of 1 — has to be told to fetch
// the history as well, with "fetch-depth: 0".
//
// The comparison reads the buf.yaml in the input as well as the one here, so
// the workspace configuration has to be committed for the two sides to name
// their files the same way. A repository whose proto root is under "rpc" and
// that has not committed one yet is told so rather than being handed buf's
// report that every file it has was deleted.
func Breaking() error {
	against := BreakingAgainst

	if v := os.Getenv(BreakingAgainstEnv); v != "" {
		against = v
	}

	if against == "" {
		return fmt.Errorf(
			"there is nothing to compare the declarations against: set"+
				" rpc.BreakingAgainst or %s to a buf input, such as %q",
			BreakingAgainstEnv, DefaultBreakingAgainst)
	}

	conf, services, env, err := prepare()
	if err != nil {
		return err
	}

	against, err = resolveBreakingAgainst(conf, against)
	if err != nil {
		return err
	}

	args := append([]string{"breaking", "--against", against},
		pathArgs(services)...)

	err = buf(env, args...)
	if err != nil {
		return fmt.Errorf("run buf breaking: %w", err)
	}

	return nil
}

// Format rewrites the repository's own service declarations in buf's
// formatting.
func Format() error {
	return format(true)
}

// FormatCheck reports the declarations buf would reformat, as a diff, and
// fails when there are any. It is what a CI job runs: Format rewrites the
// working tree, which is not an answer a build can act on.
func FormatCheck() error {
	return format(false)
}

func format(write bool) error {
	_, services, env, err := prepare()
	if err != nil {
		return err
	}

	args := []string{"format"}

	if write {
		args = append(args, "--write")
	} else {
		// The diff says which declarations and what about them, and
		// --exit-code is what turns the report into a failed build.
		args = append(args, "--diff", "--exit-code")
	}

	err = buf(env, append(args, pathArgs(services)...)...)
	if err != nil {
		if !write {
			return fmt.Errorf(
				"the service declarations are not formatted, run"+
					" \"mage rpc:format\": %w", err)
		}

		return fmt.Errorf("run buf format: %w", err)
	}

	return nil
}

// prepare is the opening of every check: the services to scope the run to,
// and the environment buf is run with.
//
// It writes the workspace configuration, which is not a side effect a check
// can do without. The module root is what buf checks a package name and
// resolves an import against, so a check run without it is checking something
// else.
func prepare() (config, []service, map[string]string, error) {
	conf, services, err := loadServices()
	if err != nil {
		return config{}, nil, nil, err
	}

	err = ensureBufConfig(conf)
	if err != nil {
		return config{}, nil, nil, err
	}

	// buf is run through "go run" like every plugin, so it wants the same
	// pinned toolchain and the same normalised GOFLAGS.
	env, err := generatorEnv()
	if err != nil {
		return config{}, nil, nil, err
	}

	return conf, services, env, nil
}

// gitInputPrefix is the buf input that names this repository's own git
// history.
const gitInputPrefix = ".git#"

// gitRemote is the remote a branch that is not a local one is looked for
// under. Having main as origin/main and not as a branch is the normal state of
// a checkout that is not a developer's: actions/checkout leaves a detached
// head, and "git clone -b feature" leaves the one branch it was asked for. A
// repository whose remote is called something else names the input itself,
// with rpc.BreakingAgainst or RPC_BREAKING_AGAINST.
const gitRemote = "origin"

// resolveBreakingAgainst returns the input to compare against, and answers for
// the ones buf would otherwise fail on halfway through a compilation.
//
// A branch is resolved against the checkout the target runs in, since the
// branch names a ref there and not on any server: a branch that is only a
// remote-tracking ref is compared against as "origin/<branch>", and one that
// is not in the checkout at all is refused with what the checkout is missing.
// The states a repository is genuinely in are all of them here: freshly
// created with no git history, cloned from a repository whose trunk is called
// something else, and checked out by a CI job that fetched one commit.
func resolveBreakingAgainst(conf config, against string) (string, error) {
	options, isGit := strings.CutPrefix(against, gitInputPrefix)
	if !isGit {
		// Any other input is a module reference or an archive, and
		// whether it resolves is between the caller and buf.
		return against, nil
	}

	exists, err := internal.FileExists(".git")
	if err != nil {
		return "", fmt.Errorf("check for the git repository: %w", err)
	}

	if !exists {
		return "", fmt.Errorf(
			"%q compares the declarations against this repository's own"+
				" git history, and there is no .git here: run the"+
				" target from the root of a git checkout, or name"+
				" another buf input with %s",
			against, BreakingAgainstEnv)
	}

	resolved, err := resolveGitBranch(against, options)
	if err != nil {
		return "", err
	}

	err = checkAgainstModuleRoot(conf, resolved)
	if err != nil {
		return "", err
	}

	return resolved, nil
}

// resolveGitBranch rewrites the branch a git input names into a ref the
// checkout has, and refuses the input when it has neither.
//
// buf clones the checkout to read the other side of the comparison, and a
// clone resolves a branch the way any other remote ref is resolved, so a
// branch that is not in the checkout is "couldn't find remote ref" out of the
// middle of buf rather than anything a build can act on.
func resolveGitBranch(against string, options string) (string, error) {
	branch, named := gitOption(options, "branch")
	if !named {
		// A tag or an explicit ref names itself, and an input with no
		// option at all is the checked out commit.
		return against, nil
	}

	if gitRefExists(branch) {
		return against, nil
	}

	tracking := gitRemote + "/" + branch

	if gitRefExists(tracking) {
		return gitInputPrefix + withBranch(options, tracking), nil
	}

	return "", fmt.Errorf(
		"the %q branch that %q compares against is in this checkout"+
			" neither as a branch nor as %q, so there is nothing to"+
			" compare with: a repository whose first commit is not"+
			" in yet has nothing to break; a build that fetched only"+
			" the commit under test, which is what actions/checkout"+
			" does unless it is given \"fetch-depth: 0\", has to"+
			" fetch the history as well; and a repository whose"+
			" trunk is called something else sets"+
			" rpc.BreakingAgainst or %s, as in"+
			" %s=.git#branch=master",
		branch, against, tracking, BreakingAgainstEnv, BreakingAgainstEnv)
}

// checkAgainstModuleRoot refuses a comparison against a state that has no
// workspace configuration, for a repository that needs one.
//
// The module root is what buf names a file relative to, so the side without a
// buf.yaml names every declaration from the repository root and this side
// names it from the proto root: nothing lines up, and buf reports every file
// in the repository as deleted. That is the state a repository is in on the
// commit that moves it to this module root, and no input helps, since every
// earlier state has the same problem.
func checkAgainstModuleRoot(conf config, against string) error {
	if conf.ProtoRoot == "." {
		// The repository root is the module root with no configuration
		// at all, so both sides name a file the same way whether the
		// file is there or not.
		return nil
	}

	options, isGit := strings.CutPrefix(against, gitInputPrefix)
	if !isGit {
		return nil
	}

	ref := gitInputCommit(options)
	if !gitRefExists(ref) {
		// Whether an input that is not a ref in this checkout resolves
		// is between the caller and buf.
		return nil
	}

	configFile := bufConfigName
	if subdir, ok := gitOption(options, "subdir"); ok {
		configFile = path.Join(subdir, configFile)
	}

	_, err := internal.OutputSilent("git", "cat-file", "-e", ref+":"+configFile)
	if err == nil {
		return nil
	}

	return fmt.Errorf(
		"%q has no %s, and this repository's proto root is %q: buf names"+
			" a file relative to the module root, so that side calls"+
			" the declarations %s/... and this side calls them what"+
			" they are, and every one of them is reported as"+
			" deleted — commit the %s the targets write, and compare"+
			" against a state that has it",
		against, bufConfigName, conf.ProtoRoot, conf.ProtoRoot,
		bufConfigName)
}

// gitRefExists reports whether a name resolves to a commit in the checkout,
// which is the same resolution buf's clone of it performs.
func gitRefExists(ref string) bool {
	_, err := internal.OutputSilent(
		"git", "rev-parse", "--verify", "--quiet", ref+"^{commit}")

	return err == nil
}

// gitInputCommit returns the ref a git input names. An input with no branch,
// tag or ref option is the checked out commit, which is HEAD.
func gitInputCommit(options string) string {
	for _, name := range []string{"branch", "tag", "ref"} {
		value, ok := gitOption(options, name)
		if ok {
			return value
		}
	}

	return "HEAD"
}

// gitOption returns the value of one of a git input's comma-separated
// options. An option with no value is the same as an option that is not
// there: buf has nothing to resolve either way.
func gitOption(options string, name string) (string, bool) {
	for o := range strings.SplitSeq(options, ",") {
		key, value, ok := strings.Cut(o, "=")
		if ok && key == name && value != "" {
			return value, true
		}
	}

	return "", false
}

// withBranch returns a git input's options with the branch replaced, leaving
// every other option buf takes where it was.
func withBranch(options string, branch string) string {
	parts := strings.Split(options, ",")

	for i, o := range parts {
		key, _, ok := strings.Cut(o, "=")
		if ok && key == "branch" {
			parts[i] = "branch=" + branch
		}
	}

	return strings.Join(parts, ",")
}
