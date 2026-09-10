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
// The targets auto-discover every service.proto under the proto root, which
// is "rpc" when that directory exists and the repository root otherwise, and
// generate for every .proto file in a service's directory. The walk skips
// vendor, node_modules and testdata directories, and anything beginning with
// a dot. What is generated for a declaration depends on which shape it is,
// and the layout is what says so.
//
// A declaration in the flat layout, "<proto root>/<application>/service.proto",
// is a dual-stack service: it serves Connect and, while Twirp is on, the
// /twirp/ paths as well. Into the service's own directory:
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
// A declaration in the versioned layout, whose directory is a version — v1,
// v2beta1 — is a native service: it implements connect-go's own handler
// interface and never serves Twirp. It gets protoc-gen-go and
// protoc-gen-connect-go output and nothing else: no adapters, no plain
// service interface, no Twirp, and, because nothing of ours has to fit a
// stream into a signature that returns one response, streaming methods are
// allowed. That is the shape rpc:stub scaffolds, and the one a new service
// has; nothing new goes into the flat layout.
//
// DualStack overrides the rule for a named service directory, for a legacy
// service that moves to the versioned layout while it still has Twirp
// callers.
//
// A .proto file that declares no service is compiled to messages and
// nothing else; the service plugins emit no file for it.
//
// Whichever of service.rpc.go and service.twirp.go is not generated is
// removed if it is there from an earlier configuration, since two
// declarations of the same interface in one package do not compile. A native
// service generates neither, so both are removed, along with the adapters in
// its connect package. Only a file carrying the generator's header is
// removed; anything else in the directory is somebody's source, whatever it
// is called.
//
// # The buf module root is the proto root
//
// A repository whose protobuf sources live under "rpc" gets a buf.yaml that
// roots the buf module there, since buf checks a package name against the
// file's directory relative to the module root: "elephant.collab.v1" only
// matches "rpc/elephant/collab/v1" from a module rooted in "rpc". A
// repository whose proto root is the repository root needs no configuration
// and gets none.
//
// The module root is also what a file is named relative to, and that reaches
// two things. An import inside a .proto is written relative to the proto root
// — "import \"greeter/types.proto\"", not "import \"rpc/greeter/types.proto\"" —
// and the name buf gives the file is part of what protoc-gen-go writes, so the
// descriptor is File_greeter_service_proto rather than
// File_rpc_greeter_service_proto. That rename does not reach the service's
// callers: a file's name is independent of its package, so the message and
// service full names, the RPC paths and the encoding are untouched. It is a
// large diff on the next generate, not a coordinated release. The generated
// files themselves still land next to the declaration they came from.
//
// # Checking the declarations
//
// Lint, Breaking and Format run the same pinned buf, scoped to the discovered
// services, so a vendored proto is compiled as an import and never checked
// against rules that belong to the repository it came from.
//
//   - rpc:lint runs "buf lint".
//   - rpc:breaking compares against BreakingAgainst, the repository's own main
//     branch by default. The branch is resolved against the checkout the
//     target runs in, so a build that has it only as a remote-tracking ref
//     compares against "origin/<branch>"; the history still has to be in the
//     checkout, which a CI job that fetches one commit has to be told to do.
//   - rpc:format rewrites the declarations, and rpc:formatCheck reports what
//     it would rewrite without touching anything, which is what CI runs.
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

	// DualStack lists the service directories that generate dual stack
	// whatever their layout: the Connect adapters, the plain service
	// interface, and Twirp while Twirp is on. It is for a legacy service
	// that moves to the versioned layout before its Twirp callers are
	// gone; without it the versioned layout means native.
	//
	// The entries are directories relative to the repository root, the
	// same paths service discovery reports:
	//
	//	rpc.DualStack = []string{"rpc/elephant/collab/v1"}
	//
	// An entry that names no discovered service is an error rather than a
	// setting with no effect, since a typo here silently changes what a
	// service generates. Override: RPC_DUAL_STACK, separated by the
	// platform's path list separator.
	DualStack []string

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

	// BreakingAgainst is the buf input Breaking compares the declarations
	// against. The default is the repository's own main branch, which is
	// what a pull request build wants; a repository that names its trunk
	// something else, or that wants to compare against a published module,
	// sets it here. Override: RPC_BREAKING_AGAINST.
	BreakingAgainst = DefaultBreakingAgainst
)

// DefaultBreakingAgainst is what Breaking compares against unless a
// repository says otherwise: the tip of the main branch in the repository's
// own git history.
const DefaultBreakingAgainst = ".git#branch=main"

// Environment variables that override the configuration above for a single
// run.
const (
	TwirpEnv           = "RPC_TWIRP"
	DualStackEnv       = "RPC_DUAL_STACK"
	VendorDirEnv       = "RPC_VENDOR_DIR"
	ExtraProtoRootsEnv = "RPC_EXTRA_PROTO_ROOTS"
	BreakingAgainstEnv = "RPC_BREAKING_AGAINST"
)

// config is the effective configuration for a run: the package variables
// with the environment applied on top.
type config struct {
	Twirp           bool
	DualStack       []string
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
		DualStack:       DualStack,
		VendorDir:       filepath.ToSlash(VendorDir),
		ExtraProtoRoots: ExtraProtoRoots,
	}

	if v := os.Getenv(DualStackEnv); v != "" {
		conf.DualStack = filepath.SplitList(v)
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

	return conf, nil
}

// checkInterfaceOwner refuses the one configuration that cannot compile:
// protoc-gen-twirp and protoc-gen-elephant-rpc both writing the plain service
// interface. Neither plugin runs for a native service, so the question only
// arises when the run has a dual-stack service in it.
func checkInterfaceOwner(conf config, services []service) error {
	if !conf.Twirp {
		return nil
	}

	dualStack := slices.ContainsFunc(services, func(s service) bool {
		return !s.Native
	})
	if !dualStack {
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
	conf, services, err := loadServices()
	if err != nil {
		return err
	}

	err = applyDualStack(conf, services)
	if err != nil {
		return err
	}

	err = checkInterfaceOwner(conf, services)
	if err != nil {
		return err
	}

	return generateCode(conf, services)
}

// loadServices is the opening of every target: the effective configuration
// and the service declarations it discovers. A repository with no declaration
// at all is an error, since every target here is about the declarations.
func loadServices() (config, []service, error) {
	conf, err := loadConfig()
	if err != nil {
		return config{}, nil, err
	}

	services, err := discoverServices(conf)
	if err != nil {
		return config{}, nil, err
	}

	if len(services) == 0 {
		return config{}, nil, fmt.Errorf(
			"no %s files under %s to work with",
			serviceFileName, conf.ProtoRoot)
	}

	return conf, services, nil
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
	// Versioned reports whether the declaration is in the versioned
	// layout, which is to say that the directory it lives in is a version.
	Versioned bool
	// Native reports whether the service implements connect-go's own
	// handler interface, which is what the versioned layout means unless
	// DualStack says otherwise. A native service gets no adapters, no
	// plain service interface and no Twirp.
	Native bool
}

// applyDualStack decides each service's shape: the versioned layout is native
// unless DualStack names the directory. An entry that names no discovered
// service is refused rather than ignored, since the mistake it usually is —
// a path spelled from the proto root rather than from the repository root —
// otherwise shows up as a service that quietly stopped generating its Twirp
// code.
func applyDualStack(conf config, services []service) error {
	named := make(map[string]bool, len(conf.DualStack))

	for _, d := range conf.DualStack {
		named[path.Clean(filepath.ToSlash(d))] = true
	}

	matched := make(map[string]bool, len(named))

	for i := range services {
		dual := named[services[i].Dir]
		if dual {
			matched[services[i].Dir] = true
		}

		services[i].Native = services[i].Versioned && !dual
	}

	var unknown []string

	for d := range named {
		if !matched[d] {
			unknown = append(unknown, d)
		}
	}

	if len(unknown) == 0 {
		return nil
	}

	slices.Sort(unknown)

	discovered := make([]string, len(services))
	for i, s := range services {
		discovered[i] = s.Dir
	}

	return fmt.Errorf(
		"rpc.DualStack (or %s) names %s, which is not a service directory"+
			" in this repository: the directories are relative to the"+
			" repository root, and the discovered ones are %s",
		DualStackEnv, strings.Join(unknown, ", "),
		strings.Join(discovered, ", "))
}

// versionExp matches the version element of the versioned proto layout, in
// buf's spelling: v1, v2, v1alpha1, v2beta1.
var versionExp = regexp.MustCompile(`^v[0-9]+(?:[a-z]+[0-9]+)?$`)

// serviceFileName is the file a service declaration lives in. It is the whole
// of service discovery: a directory holding one is a service.
const serviceFileName = "service.proto"

// notSourceDirs are the directory names service discovery never descends
// into. A dependency's checkout is not this repository's declaration, and a
// walk that does not say so finds every .proto in a vendored module.
//
// "testdata" is on the list for the reason the go command treats it as
// opaque: a fixture declaration belongs to the test that reads it, and a
// plugin or a parser that keeps one — elephantine's protoc-gen-elephant-rpc
// does — would otherwise have Go generated into its fixture.
var notSourceDirs = map[string]bool{
	"vendor":       true,
	"node_modules": true,
	"testdata":     true,
}

// discoverServices finds the service declarations under the proto root, at
// any depth: a directory holding a service.proto is a service, and the path
// says which layout it is in.
//
// The flat layout is "<root>/<application>/service.proto", which is what the
// fleet grew up with. Everything else is the versioned layout, where the
// declaration's own directory is a version and the application is named one
// level up — "<root>/<application>/<version>", and, for a declaration that
// mirrors a package with a prefix in it, "<root>/elephant/<application>/v1".
// The layout decides the shape, so this is also where a service becomes
// native or dual stack.
//
// A directory that only looks like a version — "views" — is somebody else's
// word, and a declaration under it belongs to the flat layout. The directories
// in notSourceDirs are not walked into at all.
func discoverServices(conf config) ([]service, error) {
	skip := map[string]bool{
		conf.VendorDir: true,
	}

	// A directory that is a proto root of its own is compiled as imports
	// and never generated for.
	for _, r := range conf.ExtraProtoRoots {
		skip[path.Clean(filepath.ToSlash(r))] = true
	}

	var services []service

	err := filepath.WalkDir(conf.ProtoRoot,
		func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			slashed := filepath.ToSlash(p)

			if d.IsDir() {
				if slashed == conf.ProtoRoot {
					return nil
				}

				if skip[slashed] || notSourceDirs[d.Name()] ||
					strings.HasPrefix(d.Name(), ".") {
					return fs.SkipDir
				}

				return nil
			}

			if d.Name() != serviceFileName {
				return nil
			}

			s, ok := serviceAt(conf.ProtoRoot, path.Dir(slashed))
			if ok {
				services = append(services, s)
			}

			return nil
		})
	if err != nil {
		return nil, fmt.Errorf(
			"look for service declarations under %s: %w", conf.ProtoRoot, err)
	}

	slices.SortFunc(services, func(a service, b service) int {
		return strings.Compare(a.Dir, b.Dir)
	})

	return services, nil
}

// serviceAt derives a service from the directory its declaration lives in,
// and reports whether the directory names an application at all. A
// service.proto in the proto root itself does not: there is no directory left
// to take the application name from.
func serviceAt(root string, dir string) (service, bool) {
	rel, err := filepath.Rel(filepath.FromSlash(root), filepath.FromSlash(dir))
	if err != nil {
		return service{}, false
	}

	elements := strings.Split(filepath.ToSlash(rel), "/")
	if len(elements) == 0 || elements[0] == "." || elements[0] == "" {
		return service{}, false
	}

	last := elements[len(elements)-1]

	// The version has to have something in front of it inside the proto
	// root, or there is no application name to be had, and the declaration
	// is read as a flat one.
	versioned := len(elements) > 1 && versionExp.MatchString(last)

	name := last
	if versioned {
		name = elements[len(elements)-2]
	}

	return service{
		Name:      name,
		Dir:       dir,
		Versioned: versioned,
	}, true
}
