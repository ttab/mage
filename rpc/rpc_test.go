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

// TestGenerate compiles a fixture repository and checks that the files the
// fleet expects appear and build. It runs the real generators, the pinned
// protoc-gen-elephant-rpc included, so it needs the network the first time a
// version is used.
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
		setup   func(t *testing.T)
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
			// A new service is Connect only, which is the default.
			name:    "connect only",
			fixture: "greeter",
			dir:     filepath.Join("rpc", "greeter"),
			connect: "greeterconnect",
			twirp:   false,
			setup:   func(_ *testing.T) {},
		},
		{
			// The versioned layout is buf's, and what rpc:stub
			// scaffolds. Its go_package names the Go package
			// separately from the import path, since the last
			// element of the path is the version.
			name:    "versioned layout",
			fixture: "versioned",
			dir:     filepath.Join("rpc", "greeter", "v1"),
			connect: "greeterv1connect",
			twirp:   false,
			setup:   func(_ *testing.T) {},
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
			mustExist(t, filepath.Join(c.dir, c.connect,
				"service.elephant.go"))

			// A file that declares no service is compiled to
			// messages and nothing else.
			mustNotExist(t, filepath.Join(c.dir, c.connect,
				"types.connect.go"))
			mustNotExist(t, filepath.Join(c.dir, c.connect,
				"types.elephant.go"))
			mustNotExist(t, filepath.Join(c.dir, "types.twirp.go"))
			mustNotExist(t, filepath.Join(c.dir, "types.rpc.go"))

			// Exactly one of the two plugins declares the plain
			// service interface, and which one follows Twirp.
			twirpFile := filepath.Join(c.dir, "service.twirp.go")
			interfaceFile := filepath.Join(c.dir, "service.rpc.go")

			if c.twirp {
				mustExist(t, twirpFile)
				mustNotExist(t, interfaceFile)
			} else {
				mustNotExist(t, twirpFile)
				mustExist(t, interfaceFile)
			}

			// Nothing needs a workspace configuration until
			// something is vendored into the repository.
			mustNotExist(t, "buf.yaml")

			// Nothing but Go is generated: the OpenAPI
			// specifications the twirp namespace wrote are gone for
			// good.
			mustNotExist(t, "docs")

			vetModule(t, dir)
		})
	}
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

// TestStub covers scaffolding a new service, which goes into the versioned
// layout, and generating what it wrote.
func TestStub(t *testing.T) {
	dir := t.TempDir()

	copyTree(t, filepath.Join("testdata", "empty"), dir)
	t.Chdir(dir)

	err := rpc.Stub("greeter", "Greeter", "Hello")
	if err != nil {
		t.Fatalf("stub the service: %v", err)
	}

	mustExist(t, filepath.Join("rpc", "greeter", "v1", "service.proto"))

	err = rpc.Generate()
	if err != nil {
		t.Fatalf("generate the stubbed service: %v", err)
	}

	generated := filepath.Join("rpc", "greeter", "v1")

	mustExist(t, filepath.Join(generated, "service.pb.go"))
	mustExist(t, filepath.Join(generated, "service.rpc.go"))
	mustExist(t, filepath.Join(generated, "greeterv1connect",
		"service.connect.go"))
	mustExist(t, filepath.Join(generated, "greeterv1connect",
		"service.elephant.go"))

	vetModule(t, dir)
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
