package rpc_test

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ttab/mage/rpc"
)

// The fixtures and generated names the cases name more than once.
const (
	versionedFixture = "versioned"
	versionedDir     = "rpc/greeter/v1"
	versionedConnect = "greeterv1connect"
)

// TestGenerate compiles a fixture repository and checks that the files the
// fleet expects appear and build. It runs the real generators, the pinned
// protoc-gen-elephant-rpc included, so it needs the network the first time a
// version is used.
//
// The cases are the two service shapes and what decides between them: the
// flat layout is dual stack, the versioned one is native, and DualStack
// overrides the layout.
func TestGenerate(t *testing.T) {
	cases := []struct {
		name string
		// fixture is the repository the case generates.
		fixture string
		// dir is where the generated code lands, and connect is the name
		// of the connect subdirectory in it.
		dir     string
		connect string
		twirp   bool
		// native is a service that implements connect-go's own handler
		// interface: no adapters, no plain interface, no Twirp.
		native bool
		setup  func(t *testing.T)
	}{
		{
			// The environment override is what a CI job or a one-off
			// regeneration uses.
			name:    "twirp through the environment",
			fixture: "greeter",
			dir:     filepath.Join("rpc", "greeter"),
			connect: "greeterconnect",
			twirp:   true,
			setup: func(t *testing.T) {
				t.Helper()
				t.Setenv(rpc.TwirpEnv, "true")
			},
		},
		{
			// The flat layout is a dual-stack service, and this is
			// what it generates today.
			name:    "connect only",
			fixture: "greeter",
			dir:     filepath.Join("rpc", "greeter"),
			connect: "greeterconnect",
			twirp:   false,
			setup:   func(_ *testing.T) {},
		},
		{
			// The versioned layout is a native service. Its
			// go_package names the Go package separately from the
			// import path, since the last element of the path is
			// the version.
			name:    "versioned layout is native",
			fixture: versionedFixture,
			dir:     filepath.Join("rpc", "greeter", "v1"),
			connect: versionedConnect,
			native:  true,
			setup:   func(_ *testing.T) {},
		},
		{
			// A declaration that mirrors a package with a prefix in
			// it is three directories down, which is the layout
			// rpc:stub writes and the one discovery has to find.
			// The fixture declares a streaming method, which only
			// compiles because neither protoc-gen-elephant-rpc nor
			// protoc-gen-twirp is run over it.
			name:    "native declaration under a package prefix",
			fixture: "native",
			dir:     filepath.Join("rpc", "elephant", "collab", "v1"),
			connect: "collabv1connect",
			native:  true,
			setup:   func(_ *testing.T) {},
		},
		{
			// Turning Twirp on does not make a native service dual
			// stack: the layout decides, and a streaming method
			// would fail protoc-gen-twirp outright.
			name:    "twirp does not reach a native service",
			fixture: "native",
			dir:     filepath.Join("rpc", "elephant", "collab", "v1"),
			connect: "collabv1connect",
			native:  true,
			setup: func(t *testing.T) {
				t.Helper()
				t.Setenv(rpc.TwirpEnv, "true")
			},
		},
		{
			// DualStack is the way back for a legacy service that
			// moves to the versioned layout before its Twirp
			// callers are gone.
			name:    "dual stack forced on a versioned service",
			fixture: versionedFixture,
			dir:     filepath.Join("rpc", "greeter", "v1"),
			connect: versionedConnect,
			twirp:   false,
			setup: func(t *testing.T) {
				t.Helper()
				withDualStack(t, versionedDir)
			},
		},
		{
			// And through the environment, with Twirp on, which is
			// the state that service is actually in.
			name:    "dual stack forced through the environment",
			fixture: versionedFixture,
			dir:     filepath.Join("rpc", "greeter", "v1"),
			connect: versionedConnect,
			twirp:   true,
			setup: func(t *testing.T) {
				t.Helper()
				t.Setenv(rpc.DualStackEnv, versionedDir)
				t.Setenv(rpc.TwirpEnv, "true")
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()

			copyTree(t, filepath.Join("testdata", c.fixture), dir)
			c.setup(t)
			t.Chdir(dir)

			err := rpc.Generate()
			if err != nil {
				t.Fatalf("generate the fixture: %v", err)
			}

			mustExist(t, filepath.Join(c.dir, "service.pb.go"))
			mustExist(t, filepath.Join(c.dir, "types.pb.go"))
			mustExist(t, filepath.Join(c.dir, c.connect,
				"service.connect.go"))

			// A file that declares no service is compiled to
			// messages and nothing else.
			mustNotExist(t, filepath.Join(c.dir, c.connect,
				"types.connect.go"))
			mustNotExist(t, filepath.Join(c.dir, c.connect,
				"types.elephant.go"))
			mustNotExist(t, filepath.Join(c.dir, "types.twirp.go"))
			mustNotExist(t, filepath.Join(c.dir, "types.rpc.go"))

			adapters := filepath.Join(c.dir, c.connect,
				"service.elephant.go")
			twirpFile := filepath.Join(c.dir, "service.twirp.go")
			interfaceFile := filepath.Join(c.dir, "service.rpc.go")

			if c.native {
				// A native service is connect-go's own
				// handler interface and nothing of ours.
				mustNotExist(t, adapters)
				mustNotExist(t, twirpFile)
				mustNotExist(t, interfaceFile)
			} else {
				mustExist(t, adapters)

				// Exactly one of the two plugins declares the
				// plain service interface, and which one
				// follows Twirp.
				if c.twirp {
					mustExist(t, twirpFile)
					mustNotExist(t, interfaceFile)
				} else {
					mustNotExist(t, twirpFile)
					mustExist(t, interfaceFile)
				}
			}

			// The proto root is the buf module root, since that is
			// what a package name is checked against and an import
			// is resolved against. Every fixture here keeps its
			// declarations under rpc.
			mustContain(t, "buf.yaml", "- path: rpc\n")

			// Nothing but Go is generated: the OpenAPI
			// specifications the twirp namespace wrote are gone for
			// good.
			mustNotExist(t, "docs")

			vetModule(t, dir)
		})
	}
}

// TestGenerateBothShapes covers a repository that holds a dual-stack service
// and a native one at the same time, which is what a repository moving one
// service at a time is. The shapes are two buf runs with different plugin
// lists, and the split is what is under test: each service has to come out
// with its own file set, and the removal of what is not generated for one
// shape must not reach the other.
func TestGenerateBothShapes(t *testing.T) {
	dir := t.TempDir()

	// The native fixture's module, with the flat fixture's declaration
	// copied in beside it. The flat declaration derives its import path
	// from whatever module it lands in, so it needs nothing of its own.
	copyTree(t, filepath.Join("testdata", "native"), dir)
	copyTree(t, filepath.Join("testdata", "greeter", "rpc"),
		filepath.Join(dir, "rpc"))

	// With Twirp on, which is the state a repository in the middle of the
	// move is in: the flat service still serves the /twirp/ paths and the
	// native one declares a stream that protoc-gen-twirp would refuse.
	t.Setenv(rpc.TwirpEnv, "true")
	t.Chdir(dir)

	err := rpc.Generate()
	if err != nil {
		t.Fatalf("generate the fixture: %v", err)
	}

	var (
		flat          = filepath.Join("rpc", "greeter")
		flatConnect   = filepath.Join(flat, "greeterconnect")
		native        = filepath.Join("rpc", "elephant", "collab", "v1")
		nativeConnect = filepath.Join(native, "collabv1connect")
	)

	// The dual-stack service: the adapters, and Twirp declaring the plain
	// service interface.
	mustExist(t, filepath.Join(flat, "service.pb.go"))
	mustExist(t, filepath.Join(flatConnect, "service.connect.go"))
	mustExist(t, filepath.Join(flatConnect, "service.elephant.go"))
	mustExist(t, filepath.Join(flat, "service.twirp.go"))
	mustNotExist(t, filepath.Join(flat, "service.rpc.go"))

	// The native service: connect-go's own handler interface and nothing
	// of ours, Twirp included.
	mustExist(t, filepath.Join(native, "service.pb.go"))
	mustExist(t, filepath.Join(nativeConnect, "service.connect.go"))
	mustNotExist(t, filepath.Join(nativeConnect, "service.elephant.go"))
	mustNotExist(t, filepath.Join(native, "service.twirp.go"))
	mustNotExist(t, filepath.Join(native, "service.rpc.go"))

	vetModule(t, dir)
}

// TestDualStackNamesAService covers a DualStack entry that matches nothing.
// The usual mistake is a path spelled from the proto root rather than from
// the repository root, and taking it silently would leave a service quietly
// generating the wrong shape.
func TestDualStackNamesAService(t *testing.T) {
	dir := t.TempDir()

	copyTree(t, filepath.Join("testdata", versionedFixture), dir)
	t.Setenv(rpc.DualStackEnv, "greeter/v1")
	t.Chdir(dir)

	err := rpc.Generate()
	if err == nil {
		t.Fatal("expected the generation to be refused")
	}

	for _, want := range []string{"greeter/v1", versionedDir} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected the refusal to name %s: %v", want, err)
		}
	}

	mustNotExist(t, filepath.Join("rpc", "greeter", "v1", "service.pb.go"))
}

// TestNativeServiceRemovesTheAdapters covers a service that moves from dual
// stack to native, which is what dropping it from DualStack means. Neither
// plugin runs for it any more, so the plain interface and the adapters that
// take and return it both have to go: the adapters would otherwise be left
// referring to an interface nothing declares.
func TestNativeServiceRemovesTheAdapters(t *testing.T) {
	dir := t.TempDir()

	copyTree(t, filepath.Join("testdata", versionedFixture), dir)
	t.Chdir(dir)

	var (
		generated     = filepath.Join("rpc", "greeter", "v1")
		connect       = filepath.Join(generated, versionedConnect)
		adapters      = filepath.Join(connect, "service.elephant.go")
		interfaceFile = filepath.Join(generated, "service.rpc.go")
		handWritten   = filepath.Join(connect, "manual.elephant.go")
	)

	withDualStack(t, versionedDir)

	err := rpc.Generate()
	if err != nil {
		t.Fatalf("generate the dual-stack service: %v", err)
	}

	mustExist(t, adapters)
	mustExist(t, interfaceFile)

	// A file no generator wrote is somebody's source, whatever it is
	// called, and is never removed.
	err = os.WriteFile(handWritten,
		[]byte("package greeterv1connect\n\n// Manual is kept.\nconst Manual = true\n"),
		0o600)
	if err != nil {
		t.Fatalf("write the hand-written file: %v", err)
	}

	withDualStack(t)

	err = rpc.Generate()
	if err != nil {
		t.Fatalf("generate the native service: %v", err)
	}

	mustNotExist(t, adapters)
	mustNotExist(t, interfaceFile)
	mustExist(t, handWritten)

	vetModule(t, dir)
}

// TestGenerateWithoutGitTags covers a repository that has never been tagged,
// which is where a new service starts: generation reads nothing but the
// sources, so it must not go looking for a version.
func TestGenerateWithoutGitTags(t *testing.T) {
	dir := t.TempDir()

	copyTree(t, filepath.Join("testdata", "greeter"), dir)
	t.Chdir(dir)

	err := rpc.Generate()
	if err != nil {
		t.Fatalf("generate the fixture: %v", err)
	}

	mustExist(t, filepath.Join("rpc", "greeter", "greeterconnect",
		"service.connect.go"))
}

// TestStaleInterfaceIsRemoved covers turning Twirp generation on and off.
// Both plugins can write the plain service interface and only one of them
// does, so the file the other one wrote the last time has to go: two
// declarations of the same interface in one package do not compile, which is
// what elephant-public-api hit and fixed by hand.
func TestStaleInterfaceIsRemoved(t *testing.T) {
	dir := t.TempDir()

	copyTree(t, filepath.Join("testdata", "greeter"), dir)
	t.Chdir(dir)

	var (
		generated     = filepath.Join("rpc", "greeter")
		twirpFile     = filepath.Join(generated, "service.twirp.go")
		interfaceFile = filepath.Join(generated, "service.rpc.go")
		handWritten   = filepath.Join(generated, "manual.rpc.go")
	)

	t.Setenv(rpc.TwirpEnv, "true")

	err := rpc.Generate()
	if err != nil {
		t.Fatalf("generate with Twirp: %v", err)
	}

	mustExist(t, twirpFile)

	// A file no generator wrote is somebody's source, whatever it is
	// called, and is never removed.
	err = os.WriteFile(handWritten,
		[]byte("package greeter\n\n// Manual is kept.\nconst Manual = true\n"),
		0o600)
	if err != nil {
		t.Fatalf("write the hand-written file: %v", err)
	}

	t.Setenv(rpc.TwirpEnv, "false")

	err = rpc.Generate()
	if err != nil {
		t.Fatalf("generate without Twirp: %v", err)
	}

	mustNotExist(t, twirpFile)
	mustExist(t, interfaceFile)
	mustExist(t, handWritten)

	vetModule(t, dir)

	// And back: the plugin's own declaration is the stale one now.
	t.Setenv(rpc.TwirpEnv, "true")

	err = rpc.Generate()
	if err != nil {
		t.Fatalf("generate with Twirp again: %v", err)
	}

	mustExist(t, twirpFile)
	mustNotExist(t, interfaceFile)
	mustExist(t, handWritten)

	vetModule(t, dir)
}

// TestTwirpWithGeneratedInterfaceIsRefused covers the configuration the
// README used to offer: Twirp and the plugin's interface option both on. Both
// write the interface, so it cannot compile, and it is refused before
// anything runs.
func TestTwirpWithGeneratedInterfaceIsRefused(t *testing.T) {
	dir := t.TempDir()

	copyTree(t, filepath.Join("testdata", "greeter"), dir)
	withElephantRPCOptions(t, "interface=true")
	t.Setenv(rpc.TwirpEnv, "true")
	t.Chdir(dir)

	err := rpc.Generate()
	if err == nil {
		t.Fatal("expected the generation to be refused")
	}

	for _, want := range []string{"service.twirp.go", "service.rpc.go"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected the refusal to name %s: %v", want, err)
		}
	}

	mustNotExist(t, filepath.Join("rpc", "greeter", "service.pb.go"))
}

// TestGoPackageMismatch covers a go_package that names an import path other
// than the one the generated code lands under. protoc-gen-go only reads the
// last element of it, so the mistake is invisible until the Connect adapters
// try to import the message package.
func TestGoPackageMismatch(t *testing.T) {
	dir := t.TempDir()

	copyTree(t, filepath.Join("testdata", "greeter"), dir)

	rewrite(t, filepath.Join(dir, "rpc", "greeter", "service.proto"),
		"./rpc/greeter", "github.com/ttab/elsewhere/rpc/greeter")

	t.Chdir(dir)

	err := rpc.Generate()
	if err == nil {
		t.Fatal("expected the generation to be refused")
	}

	if !strings.Contains(err.Error(), "github.com/ttab/rpcfixture/rpc/greeter") {
		t.Errorf("expected the error to name the import path the"+
			" generated code needs: %v", err)
	}
}

// TestTwirpOutputIsToolchainIndependent is the regression test for the
// generated Twirp file having depended on whichever Go toolchain the machine
// happened to have: protoc-gen-twirp embeds a gzipped file descriptor and
// compress/flate changed its output between Go 1.26 and Go 1.27, so the same
// declaration gave two different files.
func TestTwirpOutputIsToolchainIndependent(t *testing.T) {
	if testing.Short() {
		t.Skip("the case downloads a second Go toolchain")
	}

	// The generations chdir, and the second one still has to find the
	// fixture.
	fixture, err := filepath.Abs(filepath.Join("testdata", "greeter"))
	if err != nil {
		t.Fatalf("resolve the fixture: %v", err)
	}

	generate := func(toolchain string) string {
		dir := t.TempDir()

		copyTree(t, fixture, dir)
		t.Setenv(rpc.TwirpEnv, "true")
		t.Setenv("GOTOOLCHAIN", toolchain)
		t.Chdir(dir)

		err := rpc.Generate()
		if err != nil {
			t.Fatalf("generate with GOTOOLCHAIN=%s: %v", toolchain, err)
		}

		data, err := os.ReadFile(
			filepath.Join("rpc", "greeter", "service.twirp.go"))
		if err != nil {
			t.Fatalf("read the generated Twirp code: %v", err)
		}

		return string(data)
	}

	if generate("go1.26.5") != generate(rpc.GeneratorToolchain) {
		t.Error("the generated Twirp code differs between two ambient Go" +
			" toolchains, so it is not the pinned one that produced it")
	}
}

// TestTwirpGeneratorModulePins keeps the module protoc-gen-twirp is run from
// in step with the pins in the package. A generator version is only pinned if
// every copy of the number says the same thing.
func TestTwirpGeneratorModulePins(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("twirpgen", "go.mod.txt"))
	if err != nil {
		t.Fatalf("read the embedded go.mod: %v", err)
	}

	wanted := map[string]string{
		"github.com/twitchtv/twirp":  rpc.TwirpVersion,
		"google.golang.org/protobuf": rpc.ProtocGenGoVersion,
		"go":                         strings.TrimPrefix(rpc.GeneratorToolchain, "go"),
	}

	for module, version := range wanted {
		if !namesVersion(string(data), module, version) {
			t.Errorf("expected twirpgen/go.mod.txt to name %s %s;"+
				" regenerate the module after moving a pin",
				module, version)
		}
	}
}

// TestVendoredImport covers the newsdoc case: a service that imports a proto
// file from another repository. The compiler only sees its own workspace, so
// the file is vendored in and the vendor directory becomes a module root of
// its own.
func TestVendoredImport(t *testing.T) {
	dir := t.TempDir()

	copyTree(t, filepath.Join("testdata", "vendored"), dir)

	// The module has to be a dependency before its files can be vendored
	// out of it, which is the same requirement `go list -m` puts on it.
	goCommand(t, dir, "get", "github.com/ttab/elephant-api@latest")

	t.Chdir(dir)

	vendored := filepath.Join("rpc", "vendor", "newsdoc", "newsdoc.proto")

	for range 2 {
		// Twice: the target is idempotent so that CI can run it and
		// report the drift with a diff.
		err := rpc.VendorProto(
			"github.com/ttab/elephant-api", "newsdoc/newsdoc.proto")
		if err != nil {
			t.Fatalf("vendor newsdoc.proto: %v", err)
		}
	}

	mustExist(t, vendored)
	mustExist(t, "buf.yaml")

	config, err := os.ReadFile("buf.yaml")
	if err != nil {
		t.Fatalf("read the buf configuration: %v", err)
	}

	t.Logf("buf.yaml:\n%s", config)

	err = rpc.Generate()
	if err != nil {
		t.Fatalf("generate the fixture: %v", err)
	}

	mustExist(t, filepath.Join("rpc", "collab", "service.pb.go"))
	mustExist(t, filepath.Join("rpc", "collab",
		"collabconnect", "service.connect.go"))

	// The vendored file's Go code belongs to the module it came from, so
	// nothing is generated for it.
	mustNotExist(t, filepath.Join("rpc", "vendor", "newsdoc", "newsdoc.pb.go"))

	vetModule(t, dir)
}

// TestVendoredImportHandWrittenBufConfig covers a repository whose buf.yaml
// is somebody's own: lint and breaking change rules live in that file, so it
// is not overwritten, but it has to declare the vendored proto root or buf
// cannot resolve the import the file was vendored in for. Reporting that is
// the whole job — the target used to print success and leave a workspace that
// does not compile.
func TestVendoredImportHandWrittenBufConfig(t *testing.T) {
	dir := t.TempDir()

	copyTree(t, filepath.Join("testdata", "vendored"), dir)
	goCommand(t, dir, "get", "github.com/ttab/elephant-api@latest")

	err := os.WriteFile(filepath.Join(dir, "buf.yaml"), []byte(
		"version: v2\nlint:\n  use:\n    - STANDARD\n"), 0o600)
	if err != nil {
		t.Fatalf("write the hand-written buf configuration: %v", err)
	}

	t.Chdir(dir)

	err = rpc.VendorProto(
		"github.com/ttab/elephant-api", "newsdoc/newsdoc.proto")
	if err == nil {
		t.Fatal("expected vendoring to report the unusable workspace")
	}

	for _, want := range []string{"buf.yaml", "rpc/vendor"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected the error to name %s: %v", want, err)
		}
	}

	// The hand-written configuration is left as it was.
	config, err := os.ReadFile("buf.yaml")
	if err != nil {
		t.Fatalf("read the buf configuration: %v", err)
	}

	if !strings.Contains(string(config), "STANDARD") {
		t.Error("expected the hand-written buf configuration to be kept")
	}
}

// TestStub covers scaffolding a new service, which is a native one in the
// versioned layout, and generating what it wrote.
func TestStub(t *testing.T) {
	dir := t.TempDir()

	copyTree(t, filepath.Join("testdata", "empty"), dir)
	t.Chdir(dir)

	err := rpc.Stub("greeter", "Greeter", "Hello")
	if err != nil {
		t.Fatalf("stub the service: %v", err)
	}

	generated := filepath.Join("rpc", "elephant", "greeter", "v1")
	declaration := filepath.Join(generated, "service.proto")

	mustExist(t, declaration)

	// The directory mirrors the package, which is what buf checks it
	// against, and the service carries the suffix the caller left out.
	mustContain(t, declaration, "package elephant.greeter.v1;")
	mustContain(t, declaration, "service GreeterService {")

	err = rpc.Generate()
	if err != nil {
		t.Fatalf("generate the stubbed service: %v", err)
	}

	mustExist(t, filepath.Join(generated, "service.pb.go"))
	mustExist(t, filepath.Join(generated, "greeterv1connect",
		"service.connect.go"))

	// A stub is native: connect-go's own handler interface and nothing of
	// ours around it.
	mustNotExist(t, filepath.Join(generated, "service.rpc.go"))
	mustNotExist(t, filepath.Join(generated, "service.twirp.go"))
	mustNotExist(t, filepath.Join(generated, "greeterv1connect",
		"service.elephant.go"))

	vetModule(t, dir)
}

// TestStubPassesLint is the assertion the stub's layout exists for: a fresh
// declaration passes buf's STANDARD rules with no exemptions. It used to fail
// two of them, PACKAGE_DIRECTORY_MATCH and SERVICE_SUFFIX, which is a thing
// to find out before the first caller rather than after.
func TestStubPassesLint(t *testing.T) {
	dir := t.TempDir()

	copyTree(t, filepath.Join("testdata", "empty"), dir)
	t.Chdir(dir)

	err := rpc.Stub("greeter", "Greeter", "Hello")
	if err != nil {
		t.Fatalf("stub the service: %v", err)
	}

	err = rpc.Lint()
	if err != nil {
		t.Fatalf("lint the stubbed service: %v", err)
	}

	// Linting writes the workspace configuration, since the module root
	// is what a package name is checked against.
	mustContain(t, "buf.yaml", "- path: rpc\n")
}

// TestLintReportsAViolation checks that the target reports rather than
// silently passing. A declaration whose package does not match its directory
// is the violation the layout was changed for.
func TestLintReportsAViolation(t *testing.T) {
	dir := t.TempDir()

	copyTree(t, filepath.Join("testdata", "empty"), dir)
	t.Chdir(dir)

	err := rpc.Stub("greeter", "Greeter", "Hello")
	if err != nil {
		t.Fatalf("stub the service: %v", err)
	}

	rewrite(t,
		filepath.Join(dir, "rpc", "elephant", "greeter", "v1", "service.proto"),
		"package elephant.greeter.v1;", "package elephant.hello.v1;")

	err = rpc.Lint()
	if err == nil {
		t.Fatal("expected the lint to fail")
	}
}

// TestLintLeavesVendoredProtosAlone covers the scoping: a vendored file is
// compiled as an import, and the rules it was written against, exemptions
// included, belong to the repository it came from. newsdoc.proto has no
// version suffix and would fail PACKAGE_VERSION_SUFFIX if it were linted
// here.
func TestLintLeavesVendoredProtosAlone(t *testing.T) {
	dir := t.TempDir()

	copyTree(t, filepath.Join("testdata", "vendored"), dir)
	goCommand(t, dir, "get", "github.com/ttab/elephant-api@latest")
	t.Chdir(dir)

	err := rpc.VendorProto(
		"github.com/ttab/elephant-api", "newsdoc/newsdoc.proto")
	if err != nil {
		t.Fatalf("vendor newsdoc.proto: %v", err)
	}

	err = rpc.Lint()

	// The fixture's own declaration is a grandfathered one and fails the
	// rules it predates; the vendored file must not be named at all.
	if err != nil && strings.Contains(err.Error(), "newsdoc") {
		t.Errorf("expected the vendored proto not to be linted: %v", err)
	}
}

// TestBreaking covers the comparison against the repository's own git
// history, in both answers it has. The workspace configuration is committed
// along with the declaration, since the module root decides what buf calls a
// file and the two sides have to agree.
func TestBreaking(t *testing.T) {
	dir := t.TempDir()

	copyTree(t, filepath.Join("testdata", "empty"), dir)
	t.Chdir(dir)

	err := rpc.Stub("greeter", "Greeter", "Hello")
	if err != nil {
		t.Fatalf("stub the service: %v", err)
	}

	// Lint is what writes buf.yaml here, before the commit.
	err = rpc.Lint()
	if err != nil {
		t.Fatalf("lint the stubbed service: %v", err)
	}

	gitCommit(t, dir)

	err = rpc.Breaking()
	if err != nil {
		t.Fatalf("expected an unchanged declaration to pass: %v", err)
	}

	// A field that changes type is what every consumer compiled against.
	rewrite(t,
		filepath.Join(dir, "rpc", "elephant", "greeter", "v1", "service.proto"),
		"string param = 1;", "int32 param = 1;")

	err = rpc.Breaking()
	if err == nil {
		t.Fatal("expected the changed declaration to be reported")
	}
}

// TestBreakingWithoutGitHistory covers the state a new service starts in: no
// commits, and often no repository yet. buf would fail on the input halfway
// through a compilation, which says nothing about what to do next.
func TestBreakingWithoutGitHistory(t *testing.T) {
	dir := t.TempDir()

	copyTree(t, filepath.Join("testdata", "empty"), dir)
	t.Chdir(dir)

	err := rpc.Stub("greeter", "Greeter", "Hello")
	if err != nil {
		t.Fatalf("stub the service: %v", err)
	}

	err = rpc.Breaking()
	if err == nil {
		t.Fatal("expected the missing git history to be reported")
	}

	if !strings.Contains(err.Error(), rpc.BreakingAgainstEnv) {
		t.Errorf("expected the error to name %s: %v",
			rpc.BreakingAgainstEnv, err)
	}

	// And with a repository whose trunk is called something else, which
	// is the other half of the same mistake.
	gitCommit(t, dir)
	git(t, dir, "branch", "-m", "main", "trunk")

	err = rpc.Breaking()
	if err == nil {
		t.Fatal("expected the missing branch to be reported")
	}

	for _, want := range []string{"main", rpc.BreakingAgainstEnv} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected the error to name %s: %v", want, err)
		}
	}
}

// TestBreakingAgainstARemoteBranch covers the checkout every CI build is: the
// branch the comparison names is a remote-tracking ref and not a local
// branch, which buf resolves no more than "couldn't find remote ref main" out
// of the middle of a compilation.
func TestBreakingAgainstARemoteBranch(t *testing.T) {
	root := t.TempDir()
	origin := filepath.Join(root, "origin")

	copyTree(t, filepath.Join("testdata", "empty"), origin)
	t.Chdir(origin)

	err := rpc.Stub("greeter", "Greeter", "Hello")
	if err != nil {
		t.Fatalf("stub the service: %v", err)
	}

	// Lint is what writes buf.yaml, and both sides of the comparison read
	// it, so it is committed along with the declaration.
	err = rpc.Lint()
	if err != nil {
		t.Fatalf("lint the stubbed service: %v", err)
	}

	gitCommit(t, origin)

	clone := filepath.Join(root, "clone")

	git(t, root, "clone", "--quiet", origin, clone)

	// The branch under test is the only local branch, which is what
	// actions/checkout leaves behind.
	git(t, clone, "checkout", "--quiet", "-b", "feature")
	git(t, clone, "branch", "--delete", "--force", "main")

	t.Chdir(clone)

	err = rpc.Breaking()
	if err != nil {
		t.Fatalf("expected an unchanged declaration to pass: %v", err)
	}

	rewrite(t,
		filepath.Join(clone, "rpc", "elephant", "greeter", "v1", "service.proto"),
		"string param = 1;", "int32 param = 1;")

	err = rpc.Breaking()
	if err == nil {
		t.Fatal("expected the changed declaration to be reported")
	}

	// And reported by buf, rather than refused before it ran.
	if !strings.Contains(err.Error(), "run buf breaking") {
		t.Errorf("expected buf to have reported the change: %v", err)
	}
}

// TestBreakingBeforeTheModuleRootMoves covers the state a repository whose
// protobuf sources live under "rpc" is in on the commit that moves the buf
// module root there: the side being compared against has no buf.yaml, names
// every file from the repository root, and buf reports every declaration in
// the repository as deleted. Nothing about that says what happened, and no
// other input helps, since every earlier state has the same problem.
func TestBreakingBeforeTheModuleRootMoves(t *testing.T) {
	dir := t.TempDir()

	copyTree(t, filepath.Join("testdata", "empty"), dir)
	t.Chdir(dir)

	err := rpc.Stub("greeter", "Greeter", "Hello")
	if err != nil {
		t.Fatalf("stub the service: %v", err)
	}

	// Committed without a buf.yaml, which is the repository as it was
	// before the bump.
	gitCommit(t, dir)

	err = rpc.Breaking()
	if err == nil {
		t.Fatal("expected the comparison to be refused")
	}

	for _, want := range []string{"buf.yaml", "rpc"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected the refusal to name %s: %v", want, err)
		}
	}
}

// TestFormat covers the rewrite and the check that CI runs instead of it.
func TestFormat(t *testing.T) {
	dir := t.TempDir()

	copyTree(t, filepath.Join("testdata", "empty"), dir)
	t.Chdir(dir)

	err := rpc.Stub("greeter", "Greeter", "Hello")
	if err != nil {
		t.Fatalf("stub the service: %v", err)
	}

	declaration := filepath.Join(dir, "rpc", "elephant", "greeter", "v1",
		"service.proto")

	// A stub is written in buf's formatting to begin with.
	err = rpc.FormatCheck()
	if err != nil {
		t.Fatalf("expected the stub to be formatted: %v", err)
	}

	rewrite(t, declaration, "  string param = 1;", "        string param = 1;")

	err = rpc.FormatCheck()
	if err == nil {
		t.Fatal("expected the check to report the formatting")
	}

	err = rpc.Format()
	if err != nil {
		t.Fatalf("format the declaration: %v", err)
	}

	err = rpc.FormatCheck()
	if err != nil {
		t.Fatalf("expected the rewritten declaration to be formatted: %v", err)
	}
}

// TestElephantRPCPluginOverride covers ELEPHANT_RPC_PLUGIN pointing at a
// module checkout, which is how protoc-gen-elephant-rpc is developed against
// a repository that generates with it.
func TestElephantRPCPluginOverride(t *testing.T) {
	dir := t.TempDir()

	source := filepath.Join(dir, "fixture")
	plugin := filepath.Join(dir, "plugin")

	copyTree(t, filepath.Join("testdata", "greeter"), source)
	copyTree(t, filepath.Join("testdata", "plugin"), plugin)

	goCommand(t, plugin, "mod", "tidy")

	t.Setenv(rpc.ElephantRPCPluginEnv, plugin)
	t.Chdir(source)

	err := rpc.Generate()
	if err != nil {
		t.Fatalf("generate the fixture: %v", err)
	}

	generated := filepath.Join("rpc", "greeter", "greeterconnect")

	mustExist(t, filepath.Join(generated, "service.elephant.go"))

	// A file that declares no service gets nothing from the plugin.
	mustNotExist(t, filepath.Join(generated, "types.elephant.go"))

	vetModule(t, source)
}

// TestElephantRPCPluginOverrideInvalid checks that a value that is neither a
// directory nor a module version fails at the point of the mistake.
func TestElephantRPCPluginOverrideInvalid(t *testing.T) {
	dir := t.TempDir()

	copyTree(t, filepath.Join("testdata", "greeter"), dir)

	t.Setenv(rpc.ElephantRPCPluginEnv, "github.com/ttab/elephantine")
	t.Chdir(dir)

	err := rpc.Generate()
	if err == nil {
		t.Fatal("expected the generation to fail")
	}
}

// TestElephantRPCPluginOverrideEmpty checks that clearing the variable is an
// error rather than the same thing as leaving it unset. The plugin writes the
// Connect adapters, so a run without it would report success and leave
// whatever was generated last time in place.
func TestElephantRPCPluginOverrideEmpty(t *testing.T) {
	dir := t.TempDir()

	copyTree(t, filepath.Join("testdata", "greeter"), dir)

	t.Setenv(rpc.ElephantRPCPluginEnv, "")
	t.Chdir(dir)

	err := rpc.Generate()
	if err == nil {
		t.Fatal("expected the generation to fail")
	}

	if !strings.Contains(err.Error(), rpc.ElephantRPCPluginEnv) {
		t.Errorf("expected the error to name %s: %v",
			rpc.ElephantRPCPluginEnv, err)
	}

	mustNotExist(t, filepath.Join("rpc", "greeter", "service.pb.go"))
}

// withDualStack sets the service directories that generate dual stack for the
// length of a test.
func withDualStack(t *testing.T, dirs ...string) {
	t.Helper()

	previous := rpc.DualStack
	rpc.DualStack = dirs

	t.Cleanup(func() {
		rpc.DualStack = previous
	})
}

// git runs a git command in a fixture repository, with the machine's own
// configuration out of the way: a test must not pick up a commit signing
// setting, and must never sit waiting for somebody to approve a signature.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()

	env := append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=mage",
		"GIT_AUTHOR_EMAIL=mage@example.com",
		"GIT_COMMITTER_NAME=mage",
		"GIT_COMMITTER_EMAIL=mage@example.com",
	)

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = env

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// gitCommit makes a fixture repository a git repository with everything in it
// committed on the main branch, which is what Breaking compares against.
func gitCommit(t *testing.T, dir string) {
	t.Helper()

	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		git(t, dir, "init", "-b", "main")
	}

	git(t, dir, "add", ".")
	git(t, dir, "commit", "--no-gpg-sign", "-m", "the declaration")
}

// withElephantRPCOptions sets the plugin options for the length of a test.
func withElephantRPCOptions(t *testing.T, options ...string) {
	t.Helper()

	previous := rpc.ElephantRPCOptions
	rpc.ElephantRPCOptions = options

	t.Cleanup(func() {
		rpc.ElephantRPCOptions = previous
	})
}

// namesVersion reports whether a go.mod names a module at a version, in a
// require block, on a require line, or as the go directive.
func namesVersion(goMod string, module string, version string) bool {
	for line := range strings.Lines(goMod) {
		fields := strings.Fields(strings.TrimSpace(line))

		if len(fields) > 0 && fields[0] == "require" {
			fields = fields[1:]
		}

		if len(fields) < 2 || fields[0] != module {
			continue
		}

		if strings.TrimSuffix(fields[1], "+incompatible") == version {
			return true
		}
	}

	return false
}

// vetModule checks that the generated code builds, which is the assertion
// that matters: a plugin that emits a file nobody can compile has failed
// whatever the file list says.
func vetModule(t *testing.T, dir string) {
	t.Helper()

	goCommand(t, dir, "mod", "tidy")
	goCommand(t, dir, "vet", "./...")
}

func goCommand(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("go", args...)
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go %v: %v\n%s", args, err, out)
	}
}

// rewrite replaces text in a file, failing the test when there was nothing to
// replace.
func rewrite(t *testing.T, path string, from string, to string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}

	if !strings.Contains(string(data), from) {
		t.Fatalf("%q does not contain %q", path, from)
	}

	err = os.WriteFile(path,
		[]byte(strings.ReplaceAll(string(data), from, to)), 0o600)
	if err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func mustExist(t *testing.T, path string) {
	t.Helper()

	_, err := os.Stat(path)
	if err != nil {
		t.Errorf("expected %q to have been generated: %v", path, err)
	}
}

// mustContain checks that a generated or scaffolded file says something.
func mustContain(t *testing.T, path string, want string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("read %q: %v", path, err)

		return
	}

	if !strings.Contains(string(data), want) {
		t.Errorf("expected %q to contain %q:\n%s", path, want, data)
	}
}

func mustNotExist(t *testing.T, path string) {
	t.Helper()

	_, err := os.Stat(path)
	if err == nil {
		t.Errorf("expected %q not to have been generated", path)
	}
}

func copyTree(t *testing.T, source string, target string) {
	t.Helper()

	err := filepath.WalkDir(source, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(source, p)
		if err != nil {
			return fmt.Errorf("resolve the relative path of %q: %w", p, err)
		}

		if d.IsDir() {
			return os.MkdirAll(filepath.Join(target, rel), 0o700)
		}

		data, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("read %q: %w", p, err)
		}

		return os.WriteFile(filepath.Join(target, rel), data, 0o600)
	})
	if err != nil {
		t.Fatalf("copy the fixture: %v", err)
	}
}
