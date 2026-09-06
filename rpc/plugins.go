package rpc

import (
	"fmt"
	"os"
	"path/filepath"
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
	// BufVersion pins the protobuf compiler.
	BufVersion = "v1.72.0"

	// ProtocGenGoVersion pins the message and enum generator.
	ProtocGenGoVersion = "v1.36.12"

	// ConnectGoVersion pins the Connect generator. It has to stay in step
	// with the connectrpc.com/connect version the services build against:
	// the generated code asserts a minimum runtime version.
	ConnectGoVersion = "v1.20.0"

	// TwirpVersion pins the Twirp generator, used while a repository still
	// serves the /twirp/ paths.
	TwirpVersion = "v8.1.3"

	// ElephantRPCVersion pins protoc-gen-elephant-rpc, the plugin that
	// emits the plain protobuf service interface and the Connect adapters
	// around it. It is set to the elephantine release that ships the
	// plugin; an empty version means the plugin is skipped, which is where
	// it stands until that release is tagged. Set ELEPHANT_RPC_PLUGIN to
	// run it before then.
	ElephantRPCVersion = ""
)

const (
	bufModule          = "github.com/bufbuild/buf/cmd/buf"
	protocGenGoModule  = "google.golang.org/protobuf/cmd/protoc-gen-go"
	connectGoModule    = "connectrpc.com/connect/cmd/protoc-gen-connect-go"
	twirpModule        = "github.com/twitchtv/twirp/protoc-gen-twirp"
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
	for _, o := range options {
		key, _, _ := strings.Cut(o, "=")
		if key == name {
			return true
		}
	}

	return false
}

// goRun returns the command that runs a pinned tool without installing it.
func goRun(module string, version string) []string {
	return goRunArgs(module + "@" + version)
}

// goRunArgs returns a "go run" invocation with the given arguments.
func goRunArgs(args ...string) []string {
	return append([]string{"go", "run"}, args...)
}

// elephantRPCPlugin resolves the command that runs protoc-gen-elephant-rpc,
// returning nil when the plugin is to be skipped.
//
// ELEPHANT_RPC_PLUGIN overrides the pin, and works whether or not
// ElephantRPCVersion is set. It is either "<module>@<version>", or the
// directory of a module checkout, which is how the plugin is developed
// against a repository that generates with it:
//
//	ELEPHANT_RPC_PLUGIN=../elephantine mage rpc:generate
func elephantRPCPlugin() ([]string, error) {
	override := strings.TrimSpace(os.Getenv(ElephantRPCPluginEnv))
	if override == "" {
		if ElephantRPCVersion == "" {
			return nil, nil
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
