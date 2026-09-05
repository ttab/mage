package rpc_test

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ttab/mage/rpc"
)

// TestGenerate compiles a fixture repository and checks that the files the
// fleet expects appear and build. It runs the real generators, so it needs
// the network the first time a version is used.
func TestGenerate(t *testing.T) {
	cases := []struct {
		name  string
		twirp bool
		setup func(t *testing.T)
	}{
		{
			// The environment override is what a CI job or a one-off
			// regeneration uses.
			name:  "twirp through the environment",
			twirp: true,
			setup: func(t *testing.T) {
				t.Helper()
				t.Setenv(rpc.TwirpEnv, "true")
			},
		},
		{
			// A new service is Connect only, which is the default.
			name:  "connect only",
			twirp: false,
			setup: func(_ *testing.T) {},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()

			withoutElephantRPCPlugin(t)
			copyTree(t, filepath.Join("testdata", "greeter"), dir)
			c.setup(t)
			t.Chdir(dir)

			err := rpc.Release("v1.2.3")
			if err != nil {
				t.Fatalf("generate the fixture: %v", err)
			}

			generated := filepath.Join("rpc", "greeter")

			mustExist(t, filepath.Join(generated, "service.pb.go"))
			mustExist(t, filepath.Join(generated, "types.pb.go"))
			mustExist(t, filepath.Join(generated,
				"greeterconnect", "service.connect.go"))

			// A file that declares no service is compiled to messages
			// and nothing else.
			mustNotExist(t, filepath.Join(generated,
				"greeterconnect", "types.connect.go"))
			mustNotExist(t, filepath.Join(generated, "types.twirp.go"))

			twirpFile := filepath.Join(generated, "service.twirp.go")

			if c.twirp {
				mustExist(t, twirpFile)
			} else {
				mustNotExist(t, twirpFile)
			}

			// Nothing needs a workspace configuration until something
			// is vendored into the repository.
			mustNotExist(t, "buf.yaml")

			// The specification documents the /twirp/ paths and
			// Twirp's errors, so it follows Twirp: a Connect only
			// repository would otherwise commit a document of an
			// API it does not serve.
			spec := filepath.Join("docs", "greeter-openapi.json")

			if c.twirp {
				checkSpec(t, spec, "greeter", "v1.2.3")
			} else {
				mustNotExist(t, spec)
			}

			vetModule(t, dir)
		})
	}
}

// TestGenerateWithoutGitTags covers a repository that has never been tagged,
// which is where a new Connect-only service starts. The version is only ever
// stamped into an OpenAPI specification, and a Connect-only repository writes
// none, so generating must not go looking for one.
func TestGenerateWithoutGitTags(t *testing.T) {
	dir := t.TempDir()

	withoutElephantRPCPlugin(t)
	copyTree(t, filepath.Join("testdata", "greeter"), dir)
	t.Chdir(dir)

	err := rpc.Generate()
	if err != nil {
		t.Fatalf("generate the fixture: %v", err)
	}

	mustExist(t, filepath.Join("rpc", "greeter", "greeterconnect",
		"service.connect.go"))
	mustNotExist(t, filepath.Join("docs", "greeter-openapi.json"))
}

// TestVendoredImport covers the newsdoc case: a service that imports a proto
// file from another repository. The compiler only sees its own workspace, so
// the file is vendored in and the vendor directory becomes a module root of
// its own.
func TestVendoredImport(t *testing.T) {
	dir := t.TempDir()

	withoutElephantRPCPlugin(t)
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

	err = rpc.Release("v0.1.0")
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

// TestElephantRPCPluginOverride covers ELEPHANT_RPC_PLUGIN pointing at a
// module checkout, which is how protoc-gen-elephant-rpc is developed against
// a repository that generates with it, and the only way to run it at all
// while it has no released version.
func TestElephantRPCPluginOverride(t *testing.T) {
	dir := t.TempDir()

	source := filepath.Join(dir, "fixture")
	plugin := filepath.Join(dir, "plugin")

	copyTree(t, filepath.Join("testdata", "greeter"), source)
	copyTree(t, filepath.Join("testdata", "plugin"), plugin)

	goCommand(t, plugin, "mod", "tidy")

	t.Setenv(rpc.ElephantRPCPluginEnv, plugin)
	t.Chdir(source)

	err := rpc.Release("v1.2.3")
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

	err := rpc.Release("v1.2.3")
	if err == nil {
		t.Fatal("expected the generation to fail")
	}
}

// TestElephantRPCPlugin generates the fixture with the real
// protoc-gen-elephant-rpc, which is the assertion that the plugin is wired
// into the template correctly: it has to receive the Go import path mappings
// and the interface option, and the code it emits has to compile against the
// Connect code generated beside it.
//
// The plugin has no released version to pin, so the test runs only when
// ELEPHANT_RPC_PLUGIN names a checkout or a "module@version". Point it at an
// elephantine checkout to run it:
//
//	ELEPHANT_RPC_PLUGIN=../elephantine go test ./rpc
func TestElephantRPCPlugin(t *testing.T) {
	plugin := os.Getenv(rpc.ElephantRPCPluginEnv)
	if plugin == "" {
		t.Skipf(
			"%s is unset, so there is no protoc-gen-elephant-rpc to run:"+
				" set it to an elephantine checkout, or to a"+
				" \"module@version\", to run this test",
			rpc.ElephantRPCPluginEnv)
	}

	cases := []struct {
		name string
		// twirp says whether the repository still generates Twirp, which
		// is what decides who declares the plain service interface.
		twirp bool
	}{
		{
			// Twirp generates the interface, so the plugin emits the
			// adapters and leaves it alone.
			name:  "twirp declares the interface",
			twirp: true,
		},
		{
			// Nothing else declares it, so the plugin does.
			name:  "the plugin declares the interface",
			twirp: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()

			copyTree(t, filepath.Join("testdata", "greeter"), dir)

			if c.twirp {
				t.Setenv(rpc.TwirpEnv, "true")
			}

			t.Chdir(dir)

			err := rpc.Release("v1.2.3")
			if err != nil {
				t.Fatalf("generate the fixture: %v", err)
			}

			var (
				messages = filepath.Join("rpc", "greeter")
				adapters = filepath.Join(messages, "greeterconnect")
			)

			mustExist(t, filepath.Join(adapters, "service.elephant.go"))

			// A file that declares no service gets nothing from the
			// plugin.
			mustNotExist(t, filepath.Join(adapters, "types.elephant.go"))
			mustNotExist(t, filepath.Join(messages, "types.rpc.go"))

			iface := filepath.Join(messages, "service.rpc.go")

			if c.twirp {
				mustNotExist(t, iface)
			} else {
				mustExist(t, iface)
			}

			vetModule(t, dir)
		})
	}
}

// withoutElephantRPCPlugin keeps a test that is not about the plugin from
// picking one up from the environment of whoever is running it.
func withoutElephantRPCPlugin(t *testing.T) {
	t.Helper()

	t.Setenv(rpc.ElephantRPCPluginEnv, "")
}

// checkSpec asserts that the OpenAPI specification was generated, stamped
// with the version, and given the servers the service is reachable at.
func checkSpec(t *testing.T, path string, application string, version string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the generated specification: %v", err)
	}

	var spec struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
		Servers []struct {
			URL string `json:"url"`
		} `json:"servers"`
	}

	err = json.Unmarshal(data, &spec)
	if err != nil {
		t.Fatalf("unmarshal the generated specification: %v", err)
	}

	if spec.Info.Version != version {
		t.Errorf("got the specification version %q, wanted %q",
			spec.Info.Version, version)
	}

	want := "https://" + application + ".api.tt.se"

	if len(spec.Servers) == 0 || spec.Servers[0].URL != want {
		t.Errorf("got the servers %v, wanted %q first", spec.Servers, want)
	}
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
