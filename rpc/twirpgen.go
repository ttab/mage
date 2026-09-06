package rpc

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

// The module protoc-gen-twirp is run from. It is carried here as text rather
// than as a nested module, because a directory holding a go.mod is stripped
// out of the module zip and would not reach a repository that consumes this
// one; the files are written into a working directory for the length of a
// generation run instead.
//
// The module exists because protoc-gen-twirp is a "+incompatible" module with
// no go.mod of its own: run as "go run <module>@<version>" it resolves
// google.golang.org/protobuf at whatever the proxy answers with that day, and
// it is compiled by whichever toolchain happens to be on the machine. Both
// decide the bytes it writes — the gzipped file descriptor it embeds comes out
// of compress/flate, whose output changed between Go 1.26 and Go 1.27, so the
// same declaration produced a different service.twirp.go on two developers'
// machines. The go directive and the complete go.sum here pin the
// dependencies; GeneratorToolchain pins the compiler.
//
// Bumping TwirpVersion or ProtocGenGoVersion means regenerating these two
// files, and a test fails until they say the same numbers as the pins.
var (
	//go:embed twirpgen/go.mod.txt
	twirpGenGoMod string

	//go:embed twirpgen/go.sum.txt
	twirpGenGoSum string
)

// twirpGenName is the tool name the module declares, which is what "go tool"
// is asked for.
const twirpGenName = "protoc-gen-twirp"

// twirpGenerator writes the protoc-gen-twirp module into the work directory
// and returns the command that runs the plugin out of it.
func twirpGenerator(work string) ([]string, error) {
	dir := filepath.Join(work, "twirpgen")

	err := os.MkdirAll(dir, 0o700)
	if err != nil {
		return nil, fmt.Errorf(
			"create the protoc-gen-twirp module directory: %w", err)
	}

	files := map[string]string{
		"go.mod": twirpGenGoMod,
		"go.sum": twirpGenGoSum,
	}

	for name, content := range files {
		err := os.WriteFile(
			filepath.Join(dir, name), []byte(content), 0o600)
		if err != nil {
			return nil, fmt.Errorf(
				"write the protoc-gen-twirp module's %s: %w", name, err)
		}
	}

	return []string{"go", "-C", dir, "tool", twirpGenName}, nil
}
