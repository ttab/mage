// Package rpc compiles protobuf service declarations into Go, with buf as
// the compiler and every plugin pinned to a version in this package.
//
// It replaces the twirp package, which drove protoc inside the
// elephant-twirptools image. Nothing is installed and nothing is taken off
// PATH: buf and the plugins run as "go run <module>@<version>", so a
// generator moves when this module is bumped, and the regenerated files show
// up in the bump's diff. No buf.gen.yaml is committed anywhere; the
// generation template is passed to buf inline.
//
// Import it from magefiles/magefile.go:
//
//	import (
//		//mage:import rpc
//		_ "github.com/ttab/mage/rpc"
//	)
//
// # What is generated
//
// The targets auto-discover services in either layout,
// "<proto root>/<application>/service.proto" and
// "<proto root>/<application>/<version>/service.proto", where the proto root
// is "rpc" when that directory exists and the repository root otherwise, and
// generate for every .proto file in a service's directory. The versioned
// layout is buf's convention and what rpc:stub scaffolds; a repository can
// hold both. Per service, into the service's own directory:
//
//   - service.pb.go, the messages (protoc-gen-go).
//   - <package>connect/service.connect.go, the Connect client and handler
//     (protoc-gen-connect-go).
//   - <package>connect/service.elephant.go, the adapters that put Connect on
//     the plain protobuf service interface (protoc-gen-elephant-rpc).
//   - service.rpc.go, the plain service interface itself, when the same
//     plugin runs and Twirp is not generating that interface.
//   - service.twirp.go, when Twirp generation is on.
//
// A .proto file that declares no service is compiled to messages and
// nothing else; the service plugins emit no file for it.
//
// Whichever of service.rpc.go and service.twirp.go is not generated is
// removed if it is there from an earlier configuration, since two
// declarations of the same interface in one package do not compile. Only a
// file carrying the generator's header is removed.
//
// # What generation needs
//
// The network, or a warm module cache. buf and the plugins run as separate
// modules, and every version query goes through the module proxy, so
// GOPROXY=off fails even with everything already downloaded. A -mod flag in
// GOFLAGS is dropped for the generator invocations — the generators are not
// in a repository's vendor directory — and GOTOOLCHAIN is set to
// GeneratorToolchain, which is downloaded when the machine has another one.
//
// # Configuration
//
// The exported variables below are the configuration, set from the
// importing magefile:
//
//	import (
//		//mage:import rpc
//		"github.com/ttab/mage/rpc"
//	)
//
//	func init() {
//		// This repository still serves the /twirp/ paths.
//		rpc.Twirp = true
//	}
//
// Each has an environment variable that overrides it for a single run, which
// is what a CI job or a one-off regeneration uses rather than editing the
// magefile.
//
// # Developing protoc-gen-elephant-rpc
//
// ELEPHANT_RPC_PLUGIN replaces the pinned plugin command, and works whether
// or not the pin is set. Point it at a module checkout to generate a
// repository with a plugin you are editing:
//
//	ELEPHANT_RPC_PLUGIN=../elephantine mage rpc:generate
//
// It also takes a "<module>@<version>", for generating against a plugin
// version other than the pinned one.
package rpc

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Configuration for the generation targets. Set these from the importing
// magefile; every one of them can be overridden for a single run with the
// environment variable named in its documentation.
var (
	// Twirp turns protoc-gen-twirp on. It is off by default: a new service
	// is Connect-only, and an existing one turns it on for as long as it
	// still serves the /twirp/ paths. Override: RPC_TWIRP.
	Twirp = false

	// VendorDir is the proto root that VendorProto copies into, relative to
	// the repository root. Its contents are compiled but never generated
	// for: a vendored file's Go code comes from the module it was vendored
	// out of. Override: RPC_VENDOR_DIR.
	VendorDir = "rpc/vendor"

	// ExtraProtoRoots are further directories to add to the buf workspace,
	// for a repository that keeps protobuf sources outside the proto root.
	// Their files are resolvable as imports, and are not generated for.
	// Override: RPC_EXTRA_PROTO_ROOTS, separated by the platform's path
	// list separator.
	ExtraProtoRoots []string

	// ElephantRPCOptions are extra options for protoc-gen-elephant-rpc, on
	// top of paths=source_relative, the Go import path mappings and the
	// interface option.
	//
	// The interface option follows Twirp, and setting it here is what
	// overrides that. The plugin's adapters take and return the plain
	// service interface, so something has to declare it: it is generated
	// with "interface=true" when Twirp is off, and left to protoc-gen-twirp
	// when Twirp is on. A repository that wants the generated interface
	// while it still serves the /twirp/ paths sets "interface=true" here,
	// and one that declares the interface itself sets "interface=false".
	ElephantRPCOptions []string
)

// Environment variables that override the configuration above for a single
// run.
const (
	TwirpEnv           = "RPC_TWIRP"
	VendorDirEnv       = "RPC_VENDOR_DIR"
	ExtraProtoRootsEnv = "RPC_EXTRA_PROTO_ROOTS"
)

// config is the effective configuration for a run: the package variables
// with the environment applied on top.
type config struct {
	Twirp           bool
	VendorDir       string
	ExtraProtoRoots []string
	ProtoRoot       string
}

func loadConfig() (config, error) {
	twirp, err := boolFromEnv(TwirpEnv, Twirp)
	if err != nil {
		return config{}, err
	}

	conf := config{
		Twirp:           twirp,
		VendorDir:       filepath.ToSlash(VendorDir),
		ExtraProtoRoots: ExtraProtoRoots,
	}

	if v := os.Getenv(VendorDirEnv); v != "" {
		conf.VendorDir = filepath.ToSlash(v)
	}

	if v := os.Getenv(ExtraProtoRootsEnv); v != "" {
		conf.ExtraProtoRoots = filepath.SplitList(v)
	}

	root, err := protoRoot(conf.VendorDir)
	if err != nil {
		return config{}, err
	}

	conf.ProtoRoot = root

	err = checkInterfaceOwner(conf)
	if err != nil {
		return config{}, err
	}

	return conf, nil
}

// checkInterfaceOwner refuses the one configuration that cannot compile:
// protoc-gen-twirp and protoc-gen-elephant-rpc both writing the plain service
// interface.
func checkInterfaceOwner(conf config) error {
	if !conf.Twirp {
		return nil
	}

	value, ok := optionValue(ElephantRPCOptions, interfaceOption)
	if !ok {
		return nil
	}

	on, err := strconv.ParseBool(value)
	if err != nil {
		return fmt.Errorf(
			"parse the protoc-gen-elephant-rpc %q option %q as a boolean: %w",
			interfaceOption, value, err)
	}

	if !on {
		return nil
	}

	return fmt.Errorf(
		"twirp generation is on (rpc.Twirp or %s) and rpc.ElephantRPCOptions"+
			" asks protoc-gen-elephant-rpc for %s=true, but both write the"+
			" plain service interface — protoc-gen-twirp into service.twirp.go"+
			" and the plugin into service.rpc.go — so the generated package"+
			" would declare it twice and would not compile: turn one of them off",
		TwirpEnv, interfaceOption)
}

func boolFromEnv(name string, fallback bool) (bool, error) {
	v := os.Getenv(name)
	if v == "" {
		return fallback, nil
	}

	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("parse %s=%q as a boolean: %w", name, v, err)
	}

	return b, nil
}

// rpcDir is the directory a repository keeps its protobuf sources in when it
// does not keep them in the repository root.
const rpcDir = "rpc"

// protoRoot returns the directory the protobuf sources are rooted in, which
// is "rpc" where that directory holds anything but the vendored protos, and
// the repository root otherwise. Both layouts are in use, and the import
// paths inside the .proto files are written against the repository root
// either way.
//
// The vendored protos are the exception because they live under "rpc" by
// default: a repository that keeps its services in the root and vendors an
// import gets an rpc/vendor directory, and that must not be read as the
// repository having moved its sources. An empty rpc directory does count as
// the root, so that a repository can choose the layout before it has a
// service to put in it.
func protoRoot(vendorDir string) (string, error) {
	entries, err := os.ReadDir(rpcDir)
	if errors.Is(err, fs.ErrNotExist) {
		return ".", nil
	}

	if err != nil {
		return "", fmt.Errorf("read the %s directory: %w", rpcDir, err)
	}

	if len(entries) == 0 {
		return rpcDir, nil
	}

	for _, e := range entries {
		p := path.Join(rpcDir, e.Name())

		if p == vendorDir || strings.HasPrefix(vendorDir, p+"/") {
			continue
		}

		return rpcDir, nil
	}

	return ".", nil
}

// Generate compiles the service declarations in the repository. It reads
// nothing but the sources, so it runs in a repository that has never been
// tagged, which is where a new service starts.
func Generate() error {
	conf, err := loadConfig()
	if err != nil {
		return err
	}

	services, err := discoverServices(conf)
	if err != nil {
		return err
	}

	if len(services) == 0 {
		return fmt.Errorf(
			"no %[1]s/*/service.proto or %[1]s/*/v*/service.proto files to generate from",
			conf.ProtoRoot)
	}

	return generateCode(conf, services)
}

// service is one generated service: a directory holding a service.proto and
// whatever message files it is accompanied by.
type service struct {
	// Name is the application name: the directory the declaration lives
	// in, or its parent in the versioned layout.
	Name string
	// Dir is the directory, relative to the repository root and slash
	// separated, since that is how buf and protobuf name files.
	Dir string
}

// versionExp matches the version element of the versioned proto layout, in
// buf's spelling: v1, v2, v1alpha1, v2beta1.
var versionExp = regexp.MustCompile(`^v[0-9]+(?:[a-z]+[0-9]+)?$`)

// discoverServices finds the service declarations under the proto root, in
// either layout: "<root>/<application>/service.proto", which is what the fleet
// has, and "<root>/<application>/<version>/service.proto", which is buf's
// convention and what a new service is scaffolded into. A repository can hold
// both, which is how one moves from the first to the second one service at a
// time.
func discoverServices(conf config) ([]service, error) {
	layouts := []struct {
		pattern []string
		// name is the element of the matched directory that names the
		// application, counted from the end.
		nameFromEnd int
	}{
		{pattern: []string{"*", "service.proto"}, nameFromEnd: 1},
		{pattern: []string{"*", "v*", "service.proto"}, nameFromEnd: 2},
	}

	var services []service

	for _, l := range layouts {
		matches, err := filepath.Glob(filepath.Join(
			append([]string{conf.ProtoRoot}, l.pattern...)...))
		if err != nil {
			return nil, fmt.Errorf("glob for proto services: %w", err)
		}

		for _, p := range matches {
			dir := filepath.ToSlash(filepath.Dir(p))

			// The vendored protos are compiled as imports, never
			// generated for: their Go code belongs to the module they
			// came from.
			if dir == conf.VendorDir ||
				strings.HasPrefix(dir, conf.VendorDir+"/") {
				continue
			}

			elements := strings.Split(dir, "/")

			// A directory that only looks like a version — "vendor",
			// "views" — is somebody else's, and its service.proto
			// belongs to the flat layout one level up.
			if l.nameFromEnd == 2 &&
				!versionExp.MatchString(elements[len(elements)-1]) {
				continue
			}

			services = append(services, service{
				Name: elements[len(elements)-l.nameFromEnd],
				Dir:  dir,
			})
		}
	}

	slices.SortFunc(services, func(a service, b service) int {
		return strings.Compare(a.Dir, b.Dir)
	})

	return services, nil
}
