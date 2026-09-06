package rpc_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ttab/mage/rpc"
)

// TestVendoredImportRootLayout covers a repository that keeps its protos in
// the repository root rather than under ./rpc, and vendors an import. The
// vendored file lands in rpc/vendor, and the existence of an rpc directory
// must not be mistaken for the repository having moved its protos there:
// service discovery still has to find the services in the root.
func TestVendoredImportRootLayout(t *testing.T) {
	dir := t.TempDir()

	withoutElephantRPCPlugin(t)
	copyTree(t, filepath.Join("testdata", "vendored"), dir)

	// Move the service from the rpc layout to the root layout.
	err := os.Rename(
		filepath.Join(dir, "rpc", "collab"),
		filepath.Join(dir, "collab"))
	if err != nil {
		t.Fatalf("move the service to the repository root: %v", err)
	}

	err = os.Remove(filepath.Join(dir, "rpc"))
	if err != nil {
		t.Fatalf("remove the empty rpc directory: %v", err)
	}

	// The fixture's go_package names the rpc layout; the service now
	// lives one directory up.
	service := filepath.Join(dir, "collab", "service.proto")

	source, err := os.ReadFile(service)
	if err != nil {
		t.Fatalf("read the moved service declaration: %v", err)
	}

	err = os.WriteFile(service, []byte(strings.ReplaceAll(string(source),
		"/rpc/collab", "/collab")), 0o600)
	if err != nil {
		t.Fatalf("rewrite the go_package of the moved service: %v", err)
	}

	goCommand(t, dir, "get", "github.com/ttab/elephant-api@latest")

	t.Chdir(dir)

	err = rpc.VendorProto(
		"github.com/ttab/elephant-api", "newsdoc/newsdoc.proto")
	if err != nil {
		t.Fatalf("vendor newsdoc.proto: %v", err)
	}

	mustExist(t, filepath.Join("rpc", "vendor", "newsdoc", "newsdoc.proto"))

	err = rpc.Generate()
	if err != nil {
		t.Fatalf("generate the fixture: %v", err)
	}

	mustExist(t, filepath.Join("collab", "service.pb.go"))
	mustExist(t, filepath.Join("collab", "collabconnect", "service.connect.go"))
	mustNotExist(t, filepath.Join("rpc", "vendor", "newsdoc", "newsdoc.pb.go"))

	vetModule(t, dir)
}
