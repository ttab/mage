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
// The targets auto-discover services as "<proto root>/*/service.proto",
// where the proto root is "rpc" when that directory exists and the
// repository root otherwise, and generate for every .proto file in a
// service's directory. Per service, into the service's own directory:
//
//   - service.pb.go, the messages (protoc-gen-go).
//   - <package>connect/service.connect.go, the Connect client and handler
//     (protoc-gen-connect-go).
//   - <package>connect/service.elephant.go, the adapters that put Connect on
//     the plain protobuf service interface (protoc-gen-elephant-rpc, skipped
//     until it has a release).
//   - service.rpc.go, the plain service interface itself, when the same
//     plugin runs and Twirp is not generating that interface.
//   - service.twirp.go, when Twirp generation is on.
//
// A .proto file that declares no service is compiled to messages and
// nothing else; the service plugins emit no file for it.
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
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ttab/mage/internal"
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

	root, err := protoRoot()
	if err != nil {
		return config{}, err
	}

	conf.ProtoRoot = root

	return conf, nil
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

// protoRoot returns the directory the protobuf sources are rooted in, which
// is "rpc" where that directory exists and the repository root otherwise.
// Both layouts are in use, and the import paths inside the .proto files are
// written against the repository root either way.
func protoRoot() (string, error) {
	rpcRooted, err := internal.DirectoryExists("rpc")
	if err != nil {
		return "", fmt.Errorf("check for './rpc' directory: %w", err)
	}

	if rpcRooted {
		return "rpc", nil
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
			"no %s/*/service.proto files to generate from", conf.ProtoRoot)
	}

	return generateCode(conf, services)
}

// service is one generated service: a directory holding a service.proto and
// whatever message files it is accompanied by.
type service struct {
	// Name is the directory name.
	Name string
	// Dir is the directory, relative to the repository root and slash
	// separated, since that is how buf and protobuf name files.
	Dir string
}

func discoverServices(conf config) ([]service, error) {
	matches, err := filepath.Glob(
		filepath.Join(conf.ProtoRoot, "*", "service.proto"))
	if err != nil {
		return nil, fmt.Errorf("glob for proto services: %w", err)
	}

	var services []service

	for _, p := range matches {
		dir := filepath.ToSlash(filepath.Dir(p))

		// The vendored protos are compiled as imports, never generated
		// for: their Go code belongs to the module they came from.
		if dir == conf.VendorDir || strings.HasPrefix(dir, conf.VendorDir+"/") {
			continue
		}

		services = append(services, service{
			Name: filepath.Base(dir),
			Dir:  dir,
		})
	}

	return services, nil
}
