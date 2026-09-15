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
// There are three shapes. ShapeDualStack serves Connect and the /twirp/ paths
// side by side, ShapeConnect serves Connect only on the same plain protobuf
// service interface, and ShapeNative serves Connect only on connect-go's own
// generated handler interface. Into the service's own directory:
//
//   - service.pb.go, the messages (protoc-gen-go), for every shape.
//   - <package>connect/service.connect.go, the Connect client and handler
//     (protoc-gen-connect-go), for every shape.
//   - <package>connect/service.elephant.go, the adapters that put Connect on
//     the plain protobuf service interface (protoc-gen-elephant-rpc), for
//     ShapeDualStack and ShapeConnect.
//   - service.rpc.go, the plain service interface itself, for ShapeConnect —
//     the shape where no protoc-gen-twirp run declares it.
//   - service.twirp.go, for ShapeDualStack.
//
// Only ShapeNative may declare a streaming method: both
// protoc-gen-elephant-rpc and protoc-gen-twirp fail generation on a stream,
// since the plain interface returns one response and has no room for one.
//
// The layout picks the default, and Shapes overrides it per service. A
// declaration in the flat layout, "<proto root>/<application>/service.proto",
// is what the fleet grew up with and defaults to ShapeDualStack while Twirp
// is on and ShapeConnect when it is off. A declaration whose own directory is
// a version — v1, v2beta1 — defaults to ShapeNative; that is what rpc:stub
// scaffolds, and nothing new goes into the flat layout.
//
// The default is not the whole rule, and the override matters in both
// directions. A versioned service can be held at ShapeDualStack while it
// still has Twirp callers. And — the case the layout cannot express — an
// existing flat-layout service can be moved to ShapeNative or ShapeConnect
// where it stands. That last one is why Shapes exists: a service's proto
// package is in its procedure path and the versioned layout is what puts a
// version in the package, so deriving the shape from the layout alone would
// mean an existing service could only reach ShapeNative by moving, and so by
// breaking the paths its callers use. Naming it in Shapes changes what it
// generates and nothing else. Retiring Twirp is the same story one shape
// down: Twirp is a repository-wide default, and ShapeConnect is how one
// service leaves it without waiting for the rest.
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

// Shape is what a service generates, and so which interface its
// implementation has and which protocols it serves.
type Shape string

const (
	// ShapeDualStack serves the /twirp/ paths and Connect side by side.
	// It generates the messages, the Connect code, the adapters that put
	// Connect on the plain protobuf service interface, and Twirp — which
	// is what declares that interface.
	ShapeDualStack Shape = "dual-stack"

	// ShapeConnect serves Connect only, on the plain protobuf service
	// interface: dual stack without the Twirp mount, so
	// protoc-gen-elephant-rpc declares the interface instead of
	// protoc-gen-twirp. It is what a service retiring Twirp becomes, and
	// it keeps every path and every handler signature it had.
	//
	// A streaming method is still not possible: the plain interface
	// returns one response and has no room for a stream.
	ShapeConnect Shape = "connect"

	// ShapeNative serves Connect only, on connect-go's own generated
	// handler interface. It generates the messages and the Connect code
	// and nothing else: no adapters, no plain interface, no Twirp.
	// Streaming methods are allowed.
	//
	// Moving a service here changes the signatures its implementation and
	// its Go callers compile against. It does not change its proto
	// package, its service name, its procedure paths or its encoding, so
	// nothing on the wire moves and no caller has to be deployed in step.
	ShapeNative Shape = "native"
)

// shapes is every valid Shape, for validation and for the error message that
// lists them.
var shapes = []Shape{ShapeDualStack, ShapeConnect, ShapeNative}

// Adapters reports whether the shape generates the Connect adapters and the
// plain protobuf service interface, which is every shape but the native one.
func (s Shape) Adapters() bool {
	return s != ShapeNative
}

// String implements fmt.Stringer.
func (s Shape) String() string {
	return string(s)
}

// Configuration for the generation targets. Set these from the importing
// magefile; every one of them can be overridden for a single run with the
// environment variable named in its documentation.
var (
	// Twirp turns protoc-gen-twirp on. It is off by default: a new service
	// is Connect-only, and an existing one turns it on for as long as it
	// still serves the /twirp/ paths. Override: RPC_TWIRP.
	Twirp = false

	// Shapes overrides, per service, what that service generates. A
	// service that is not named here takes its shape from its layout: a
	// declaration in the flat layout is ShapeDualStack when Twirp is on
	// and ShapeConnect when it is off, and one in the versioned layout is
	// ShapeNative.
	//
	// The keys are directories relative to the repository root, the same
	// paths service discovery reports:
	//
	//	rpc.Shapes = map[string]rpc.Shape{
	//		// Off Twirp and onto connect-go's own interface,
	//		// without moving and so without changing its paths.
	//		"repository": rpc.ShapeNative,
	//		// Moved layout, still has Twirp callers.
	//		"rpc/elephant/collab/v1": rpc.ShapeDualStack,
	//	}
	//
	// Both directions matter, and the second is why this is a map rather
	// than a list. A service's proto package is in its procedure path, and
	// the versioned layout is what puts a version in the package, so
	// deriving the shape from the layout alone would mean an existing
	// service could only reach ShapeNative by moving — changing the path
	// its callers use. Naming it here changes what it generates and
	// nothing else.
	//
	// A key that names no discovered service is an error rather than a
	// setting with no effect, since a typo silently changes what a service
	// generates. Override: RPC_SHAPES, "<directory>=<shape>" entries
	// separated by the platform's path list separator.
	Shapes map[string]Shape

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
	ShapesEnv          = "RPC_SHAPES"
	VendorDirEnv       = "RPC_VENDOR_DIR"
	ExtraProtoRootsEnv = "RPC_EXTRA_PROTO_ROOTS"
	BreakingAgainstEnv = "RPC_BREAKING_AGAINST"
)

// config is the effective configuration for a run: the package variables
// with the environment applied on top.
type config struct {
	Twirp           bool
	Shapes          map[string]Shape
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
		Shapes:          Shapes,
		VendorDir:       filepath.ToSlash(VendorDir),
		ExtraProtoRoots: ExtraProtoRoots,
	}

	if v := os.Getenv(ShapesEnv); v != "" {
		conf.Shapes, err = parseShapes(filepath.SplitList(v))
		if err != nil {
			return config{}, err
		}
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

// checkInterfaceOwner refuses the one configuration that cannot compile: two
// generators writing the plain protobuf service interface into the same
// package.
//
// A service's shape decides which of them writes it — protoc-gen-twirp for
// ShapeDualStack, protoc-gen-elephant-rpc for ShapeConnect, neither for
// ShapeNative — so ElephantRPCOptions setting the option itself can only
// contradict that, and does so for every service at once where the shape is
// per service. Set the shape instead.
func checkInterfaceOwner(conf config, services []service) error {
	_ = conf
	_ = services

	value, ok := optionValue(ElephantRPCOptions, interfaceOption)
	if !ok {
		return nil
	}

	return fmt.Errorf(
		"rpc.ElephantRPCOptions sets the protoc-gen-elephant-rpc %q option"+
			" to %q, but which generator writes the plain service"+
			" interface follows the service's shape —"+
			" protoc-gen-twirp into service.twirp.go for %s,"+
			" protoc-gen-elephant-rpc into service.rpc.go for %s, and"+
			" neither for %s — so setting it here can only contradict"+
			" that, and does so for every service at once where the"+
			" shape is per service: drop the option and set rpc.Shapes"+
			" (or %s) for the services that need a different shape",
		interfaceOption, value,
		ShapeDualStack, ShapeConnect, ShapeNative, ShapesEnv)
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

	err = applyShapes(conf, services)
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
	// Shape is what the service generates: its layout's default, or the
	// override Shapes names for its directory.
	Shape Shape
}

// applyShapes gives every service its shape: the layout's default, with the
// Shapes override on top.
//
// The default is the common case rather than the whole rule. A flat-layout
// declaration is what the fleet grew up with and is dual stack while Twirp is
// on; a versioned one is what rpc:stub writes and is native. The override is
// what makes the two independent of each other, which matters in both
// directions: a versioned service can keep the adapters while it still has
// Twirp callers, and — the case the layout cannot express — an existing
// service can move to connect-go's own interface without moving directory,
// and so without changing the proto package that is in its procedure path.
func applyShapes(conf config, services []service) error {
	named := make(map[string]Shape, len(conf.Shapes))

	for dir, shape := range conf.Shapes {
		err := shape.validate()
		if err != nil {
			return fmt.Errorf("the shape of %q: %w", dir, err)
		}

		named[path.Clean(filepath.ToSlash(dir))] = shape
	}

	matched := make(map[string]bool, len(named))

	for i := range services {
		services[i].Shape = defaultShape(conf, services[i])

		shape, ok := named[services[i].Dir]
		if !ok {
			continue
		}

		services[i].Shape = shape
		matched[services[i].Dir] = true
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
		"rpc.Shapes (or %s) names %s, which is not a service directory"+
			" in this repository: the directories are relative to the"+
			" repository root, and the discovered ones are %s",
		ShapesEnv, strings.Join(unknown, ", "),
		strings.Join(discovered, ", "))
}

// defaultShape is the shape a service takes from its layout when Shapes does
// not name it.
func defaultShape(conf config, s service) Shape {
	switch {
	case s.Versioned:
		return ShapeNative
	case conf.Twirp:
		return ShapeDualStack
	default:
		return ShapeConnect
	}
}

// validate reports whether the shape is one this package knows.
func (s Shape) validate() error {
	if slices.Contains(shapes, s) {
		return nil
	}

	names := make([]string, len(shapes))
	for i, v := range shapes {
		names[i] = string(v)
	}

	return fmt.Errorf("%q is not a service shape, which is one of %s",
		string(s), strings.Join(names, ", "))
}

// parseShapes reads the RPC_SHAPES entries, each "<directory>=<shape>".
func parseShapes(entries []string) (map[string]Shape, error) {
	out := make(map[string]Shape, len(entries))

	for _, e := range entries {
		dir, shape, ok := strings.Cut(e, "=")
		if !ok || dir == "" {
			return nil, fmt.Errorf(
				"%s entry %q is not \"<directory>=<shape>\"", ShapesEnv, e)
		}

		err := Shape(shape).validate()
		if err != nil {
			return nil, fmt.Errorf("%s entry %q: %w", ShapesEnv, e, err)
		}

		out[dir] = Shape(shape)
	}

	return out, nil
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
