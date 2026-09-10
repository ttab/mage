package rpc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The application names the discovery cases share.
const (
	spell   = "spell"
	greeter = "greeter"
)

// TestDiscoverServices covers what the path a declaration lives in says: the
// application it belongs to, whether it is versioned, and — since the layout
// decides the shape — whether it is native.
//
// It is an in-package test because discovery is what everything else is built
// on and the end-to-end tests can only see it through a whole generation.
func TestDiscoverServices(t *testing.T) {
	// The two declarations the case names in several places.
	const (
		spellV1 = "rpc/spell/v1"
		flatDir = "rpc/greeter"
	)

	declarations := []string{
		// The flat layout the fleet grew up with.
		flatDir + "/service.proto",
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
		{Name: greeter, Dir: flatDir},
		{Name: "views", Dir: "rpc/hub/views"},
		{Name: spell, Dir: spellV1, Versioned: true},
		{Name: spell, Dir: "rpc/spell/v2beta1", Versioned: true},
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

	// With no override the layout decides: flat is dual stack while Twirp
	// is on, versioned is native.
	conf.Twirp = true

	err = applyShapes(conf, services)
	if err != nil {
		t.Fatalf("apply the service shapes: %v", err)
	}

	for _, s := range services {
		wanted := ShapeDualStack
		if s.Versioned {
			wanted = ShapeNative
		}

		if s.Shape != wanted {
			t.Errorf("%s is %s, wanted %s", s.Dir, s.Shape, wanted)
		}
	}

	// Twirp off moves every flat service to connect, and leaves the
	// versioned ones alone.
	conf.Twirp = false

	err = applyShapes(conf, services)
	if err != nil {
		t.Fatalf("apply the service shapes: %v", err)
	}

	for _, s := range services {
		wanted := ShapeConnect
		if s.Versioned {
			wanted = ShapeNative
		}

		if s.Shape != wanted {
			t.Errorf("%s is %s, wanted %s", s.Dir, s.Shape, wanted)
		}
	}

	// The override goes both ways, and the direction the layout cannot
	// express is a flat service becoming native without moving.
	conf.Twirp = true
	conf.Shapes = map[string]Shape{
		spellV1: ShapeDualStack,
		flatDir: ShapeNative,
	}

	err = applyShapes(conf, services)
	if err != nil {
		t.Fatalf("apply the service shapes: %v", err)
	}

	for _, s := range services {
		wanted := ShapeDualStack

		switch {
		case s.Dir == flatDir:
			wanted = ShapeNative
		case s.Dir == spellV1:
			wanted = ShapeDualStack
		case s.Versioned:
			wanted = ShapeNative
		}

		if s.Shape != wanted {
			t.Errorf("%s is %s, wanted %s", s.Dir, s.Shape, wanted)
		}
	}
}

// TestShapeOverrideNamesAService covers a Shapes key that matches no
// discovered service, which is a typo that would otherwise silently leave a
// service generating something else.
func TestShapeOverrideNamesAService(t *testing.T) {
	conf := config{
		ProtoRoot: ".",
		Shapes:    map[string]Shape{"nowhere": ShapeNative},
	}

	err := applyShapes(conf, []service{{Dir: greeter, Name: greeter}})
	if err == nil {
		t.Fatal("wanted an error for a shape naming no service")
	}

	if !strings.Contains(err.Error(), "nowhere") {
		t.Errorf("the error does not name the entry: %v", err)
	}
}

// TestShapeValidation covers an unknown shape, from the magefile and from the
// environment.
func TestShapeValidation(t *testing.T) {
	conf := config{
		ProtoRoot: ".",
		Shapes:    map[string]Shape{"greeter": Shape("twirp")},
	}

	err := applyShapes(conf, []service{{Dir: greeter, Name: greeter}})
	if err == nil {
		t.Fatal("wanted an error for an unknown shape")
	}

	_, err = parseShapes([]string{"greeter=native"})
	if err != nil {
		t.Fatalf("parse a valid entry: %v", err)
	}

	for _, entry := range []string{"greeter", "greeter=twirp", "=native"} {
		_, err = parseShapes([]string{entry})
		if err == nil {
			t.Errorf("wanted an error for the entry %q", entry)
		}
	}
}
