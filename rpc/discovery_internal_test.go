package rpc

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDiscoverServices covers what the path a declaration lives in says: the
// application it belongs to, whether it is versioned, and — since the layout
// decides the shape — whether it is native.
//
// It is an in-package test because discovery is what everything else is built
// on and the end-to-end tests can only see it through a whole generation.
func TestDiscoverServices(t *testing.T) {
	// The one declaration the case names in three places.
	const spellV1 = "rpc/spell/v1"

	declarations := []string{
		// The flat layout the fleet grew up with.
		"rpc/greeter/service.proto",
		// The versioned layout, in both spellings of the version.
		spellV1 + "/service.proto",
		"rpc/spell/v2beta1/service.proto",
		// A declaration that mirrors a package with a prefix in it,
		// which is what rpc:stub writes.
		"rpc/elephant/collab/v1/service.proto",
		// A directory that only looks like a version is somebody
		// else's word, and the declaration under it is a flat one.
		"rpc/hub/views/service.proto",
		// A vendored proto is compiled as an import and never
		// generated for, whatever it declares.
		"rpc/vendor/newsdoc/newsdoc.proto",
		"rpc/vendor/other/service.proto",
		// Neither a dependency's checkout nor anything a tool hides in
		// a dot directory is this repository's declaration, and a
		// fixture belongs to the test that reads it.
		"rpc/vendor/service.proto",
		"rpc/.cache/leftover/service.proto",
		"rpc/greeter/testdata/fixture/v1/service.proto",
		// A file that is not a declaration is not a service.
		"rpc/greeter/types.proto",
	}

	dir := t.TempDir()

	for _, d := range declarations {
		p := filepath.Join(dir, filepath.FromSlash(d))

		err := os.MkdirAll(filepath.Dir(p), 0o700)
		if err != nil {
			t.Fatalf("create the fixture directory: %v", err)
		}

		err = os.WriteFile(p, []byte("syntax = \"proto3\";\n"), 0o600)
		if err != nil {
			t.Fatalf("write the fixture declaration: %v", err)
		}
	}

	t.Chdir(dir)

	conf := config{
		ProtoRoot: "rpc",
		VendorDir: "rpc/vendor",
	}

	services, err := discoverServices(conf)
	if err != nil {
		t.Fatalf("discover the services: %v", err)
	}

	wanted := []service{
		{Name: "collab", Dir: "rpc/elephant/collab/v1", Versioned: true},
		{Name: "greeter", Dir: "rpc/greeter"},
		{Name: "views", Dir: "rpc/hub/views"},
		{Name: "spell", Dir: spellV1, Versioned: true},
		{Name: "spell", Dir: "rpc/spell/v2beta1", Versioned: true},
	}

	if len(services) != len(wanted) {
		t.Fatalf("discovered %v, wanted %v", services, wanted)
	}

	// The results are sorted on the directory, so a generation run is the
	// same run twice.
	for i, w := range wanted {
		if services[i] != w {
			t.Errorf("service %d is %v, wanted %v", i, services[i], w)
		}
	}

	err = applyDualStack(conf, services)
	if err != nil {
		t.Fatalf("apply the dual stack override: %v", err)
	}

	for _, s := range services {
		if s.Native != s.Versioned {
			t.Errorf("%s is native=%t, wanted %t",
				s.Dir, s.Native, s.Versioned)
		}
	}

	conf.DualStack = []string{spellV1}

	err = applyDualStack(conf, services)
	if err != nil {
		t.Fatalf("apply the dual stack override: %v", err)
	}

	for _, s := range services {
		native := s.Versioned && s.Dir != spellV1

		if s.Native != native {
			t.Errorf("%s is native=%t, wanted %t",
				s.Dir, s.Native, native)
		}
	}
}
