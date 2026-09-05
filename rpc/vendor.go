package rpc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ttab/mage/internal"
)

// VendorProto copies a .proto file out of a Go module and into the
// repository's vendored proto root, so that buf can resolve it as an import.
//
// The compiler only sees the files in its workspace, and a workspace cannot
// reach outside the repository, which is what protoc was doing when it was
// handed a dependency's module directory as a --proto_path. A vendored file
// keeps the path it has in the repository it came from, so the import in the
// service's own .proto is unchanged:
//
//	mage rpc:vendorProto github.com/ttab/elephant-api newsdoc/newsdoc.proto
//
// That is the only file the fleet vendors today. It is generated from the
// newsdoc module, so the copy changes when that module does, and the target
// is idempotent: run it in CI and let "git diff --exit-code" report the
// drift.
//
// The vendored file is compiled but never generated for. Its Go code comes
// from the module it was vendored out of, which is where the service imports
// it from.
func VendorProto(module string, file string) error {
	conf, err := loadConfig()
	if err != nil {
		return err
	}

	name := filepath.ToSlash(filepath.Clean(file))
	if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
		return fmt.Errorf(
			"%q has to be a path inside the module, such as newsdoc/newsdoc.proto",
			file)
	}

	dir, err := internal.OutputSilent("go", "list", "-m", "-f", "{{.Dir}}", module)
	if err != nil {
		return fmt.Errorf(
			"resolve the location of the %s module, is it a dependency?: %w",
			module, err)
	}

	dir = strings.TrimSpace(dir)
	if dir == "" {
		return fmt.Errorf("the %s module has no local directory", module)
	}

	source, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		return fmt.Errorf("read %s from the %s module: %w", name, module, err)
	}

	target := filepath.Join(filepath.FromSlash(conf.VendorDir), filepath.FromSlash(name))

	current, err := os.ReadFile(target)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read the vendored %q: %w", target, err)
	}

	if string(current) == string(source) {
		_, _ = fmt.Fprintf(os.Stdout, "%s is up to date\n", target)

		return nil
	}

	err = internal.EnsureDirectory(filepath.Dir(target))
	if err != nil {
		return fmt.Errorf("ensure the vendored proto directory: %w", err)
	}

	// The target is built from the file name checked above, which is
	// relative and does not jump context.
	err = os.WriteFile(target, source, 0o600) //nolint:gosec
	if err != nil {
		return fmt.Errorf("write %q: %w", target, err)
	}

	// The vendor directory is a module root of its own in the buf
	// workspace, and buf needs a configuration file to be told so.
	err = ensureBufConfig(conf)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(os.Stdout, "vendored %s from %s\n     to %s\n",
		name, module, target)

	return nil
}
