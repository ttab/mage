package rpc

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ttab/mage/internal"
)

// The generator versions. Every tool is run with "go run <module>@<version>",
// so nothing is installed, nothing is taken off PATH, and the versions in
// this file are the versions that produced the generated files in every
// repository that generates through this package. Moving a generator is a
// ttab/mage bump, which is what makes the regenerated output show up as a
// diff in the bump's pull request.
const (
	// GeneratorToolchain pins the Go toolchain the generators are compiled
	// and run with, and is set in the environment of every generator
	// invocation. The toolchain is part of what a generator writes:
	// protoc-gen-twirp embeds a gzipped file descriptor, compress/flate
	// changed its output between Go 1.26 and Go 1.27, and the same
	// declaration therefore gave a different service.twirp.go on two
	// machines. A "toolchain" directive in a go.mod would not have been
	// enough, since that is a floor and the go command prefers a newer local
	// toolchain over it.
	//
	// It is an exact version, so generating downloads that toolchain on a
	// machine that has another one. Keep it on the fleet's Go floor.
	GeneratorToolchain = "go1.27.1"

	// BufVersion pins the protobuf compiler.
	BufVersion = "v1.72.0"

	// ProtocGenGoVersion pins the message and enum generator.
	ProtocGenGoVersion = "v1.36.12"

	// ConnectGoVersion pins the Connect generator. It has to stay in step
	// with the connectrpc.com/connect version the services build against:
	// the generated code asserts a minimum runtime version.
	ConnectGoVersion = "v1.20.0"

	// TwirpVersion pins the Twirp generator, used while a repository still
	// serves the /twirp/ paths. It is not run with "go run
	// <module>@<version>": protoc-gen-twirp has no go.mod, so that would
	// resolve its dependencies afresh on every run. It is run out of the
	// module in twirpgen instead, which requires this version and carries a
	// complete go.sum.
	TwirpVersion = "v8.1.3"

	// ElephantRPCVersion pins protoc-gen-elephant-rpc, the plugin that
	// emits the plain protobuf service interface and the Connect adapters
	// around it. It is the elephantine release that ships the plugin. An
	// empty version is an error rather than a skipped plugin;
	// ELEPHANT_RPC_PLUGIN overrides it for developing the plugin against a
	// repository.
	ElephantRPCVersion = "v0.29.0"
)

const (
	bufModule          = "github.com/bufbuild/buf/cmd/buf"
	protocGenGoModule  = "google.golang.org/protobuf/cmd/protoc-gen-go"
	connectGoModule    = "connectrpc.com/connect/cmd/protoc-gen-connect-go"
	elephantRPCModule  = "github.com/ttab/elephantine/cmd/protoc-gen-elephant-rpc"
	elephantRPCCommand = "./cmd/protoc-gen-elephant-rpc"
)

// ElephantRPCPluginEnv names the environment variable that replaces the
// pinned protoc-gen-elephant-rpc command.
const ElephantRPCPluginEnv = "ELEPHANT_RPC_PLUGIN"

// interfaceOption is the protoc-gen-elephant-rpc option that makes it emit
// the plain service interface itself.
const interfaceOption = "interface"

// elephantRPCOptions returns the options protoc-gen-elephant-rpc is run with:
// whatever the magefile set, plus "interface=true" when nothing else emits
// the plain service interface.
//
// The adapters take and return that interface, so something has to declare
// it: protoc-gen-twirp does while a repository still generates Twirp, and
// protoc-gen-elephant-rpc does when it does not. Following Twirp here is what
// makes a Connect-only repository's generated code compile with no
// configuration of its own. A magefile that sets the option itself keeps its
// value, which is how a repository generates the interface before it stops
// generating Twirp, or leaves it to a hand-written declaration.
func elephantRPCOptions(conf config) []string {
	options := append([]string{}, ElephantRPCOptions...)

	if conf.Twirp || hasOption(options, interfaceOption) {
		return options
	}

	return append(options, interfaceOption+"=true")
}

// hasOption reports whether a plugin option is set, as "name" or "name=value".
func hasOption(options []string, name string) bool {
	_, ok := optionValue(options, name)

	return ok
}

// interfaceEnabled reports whether protoc-gen-elephant-rpc writes the plain
// service interface on this run. A value that is not a boolean is left for
// the plugin to complain about.
func interfaceEnabled(conf config) bool {
	value, ok := optionValue(elephantRPCOptions(conf), interfaceOption)
	if !ok {
		return false
	}

	on, err := strconv.ParseBool(value)

	return err == nil && on
}

// optionValue returns the value of a plugin option and whether it was set at
// all. A bare "name" is the same as "name=true", which is how protoc plugin
// options are written.
func optionValue(options []string, name string) (string, bool) {
	for _, o := range options {
		key, value, hasValue := strings.Cut(o, "=")
		if key != name {
			continue
		}

		if !hasValue {
			return "true", true
		}

		return value, true
	}

	return "", false
}

// generatorEnv returns the environment overrides every generator invocation
// runs with. buf passes its own environment on to the plugins it spawns, so
// setting it on buf is what reaches all of them.
//
// GOTOOLCHAIN pins the compiler, because the toolchain decides some of the
// bytes a generator writes. GOFLAGS has -mod dropped: a repository that
// vendors its dependencies has -mod=vendor in the environment or in "go env",
// and the generators are separate modules run out of the module cache rather
// than out of that vendor directory, so every one of them fails with "cannot
// query module" until the flag is out of the way.
func generatorEnv() (map[string]string, error) {
	flags, err := internal.OutputSilent("go", "env", "GOFLAGS")
	if err != nil {
		return nil, fmt.Errorf("read the Go build flags: %w", err)
	}

	return map[string]string{
		"GOTOOLCHAIN": GeneratorToolchain,
		"GOFLAGS":     withoutModFlag(flags),
	}, nil
}

// withoutModFlag returns a GOFLAGS value with any -mod flag removed.
func withoutModFlag(flags string) string {
	var kept []string

	for f := range strings.FieldsSeq(flags) {
		if strings.HasPrefix(f, "-mod=") {
			continue
		}

		kept = append(kept, f)
	}

	return strings.Join(kept, " ")
}

// goRun returns the command that runs a pinned tool without installing it.
func goRun(module string, version string) []string {
	return goRunArgs(module + "@" + version)
}

// goRunArgs returns a "go run" invocation with the given arguments.
func goRunArgs(args ...string) []string {
	return append([]string{"go", "run"}, args...)
}

// elephantRPCPlugin resolves the command that runs protoc-gen-elephant-rpc.
//
// There is no way to skip the plugin. The adapters it writes are what puts
// Connect on the plain service interface, so a run without it leaves whatever
// was generated last time in place and reports success, which is how a
// repository ends up shipping adapters for a declaration it no longer has.
// A missing pin is an error.
//
// ELEPHANT_RPC_PLUGIN overrides the pin, and works whether or not
// ElephantRPCVersion is set. It is either "<module>@<version>", or the
// directory of a module checkout, which is how the plugin is developed
// against a repository that generates with it:
//
//	ELEPHANT_RPC_PLUGIN=../elephantine mage rpc:generate
//
// Setting it to an empty value is an error rather than the same thing as
// leaving it unset, so that nothing can quietly unset the plugin.
func elephantRPCPlugin() ([]string, error) {
	value, set := os.LookupEnv(ElephantRPCPluginEnv)

	override := strings.TrimSpace(value)

	if set && override == "" {
		return nil, fmt.Errorf(
			"%s is set to an empty value, which names no"+
				" protoc-gen-elephant-rpc to run: unset it to use the"+
				" pinned %s, or point it at a module checkout",
			ElephantRPCPluginEnv, ElephantRPCVersion)
	}

	if override == "" {
		if ElephantRPCVersion == "" {
			return nil, fmt.Errorf(
				"protoc-gen-elephant-rpc is not pinned, so the"+
					" Connect adapters cannot be generated: set"+
					" ElephantRPCVersion in github.com/ttab/mage/rpc,"+
					" or name a plugin with %s",
				ElephantRPCPluginEnv)
		}

		return goRun(elephantRPCModule, ElephantRPCVersion), nil
	}

	isDir, err := internal.DirectoryExists(override)
	if err != nil {
		return nil, fmt.Errorf(
			"check whether %s=%q names a directory: %w",
			ElephantRPCPluginEnv, override, err)
	}

	if isDir {
		dir, err := filepath.Abs(override)
		if err != nil {
			return nil, fmt.Errorf(
				"resolve the %s directory %q: %w",
				ElephantRPCPluginEnv, override, err)
		}

		return goRunArgs("-C", dir, elephantRPCCommand), nil
	}

	if !strings.Contains(override, "@") {
		return nil, fmt.Errorf(
			"%s=%q is neither a directory nor a \"module@version\"",
			ElephantRPCPluginEnv, override)
	}

	return goRunArgs(override), nil
}
