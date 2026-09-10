package rpc

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"github.com/ttab/mage/internal"
)

var applicationExp = regexp.MustCompile(`^[a-z][0-9a-z_]*$`)

var (
	messageExp        = regexp.MustCompile(`^[A-Z][0-9a-zA-Z]*$`)
	messageConstraint = "must start with an uppercase letter and only contain the characters a-z, A-Z, 0-9"
)

// StubVersion is the version element a stubbed service is scaffolded into.
// Every new declaration starts at v1; a second major version is a directory
// somebody copies it into when the two have to be served side by side.
const StubVersion = "v1"

// StubPackagePrefix is the first element of a stubbed declaration's protobuf
// package, and of the directory under the proto root that mirrors it. The
// package is what a Connect procedure path is built out of, so it names the
// fleet the service belongs to rather than the organisation that runs it.
const StubPackagePrefix = "elephant"

// StubServiceSuffix is what a stubbed service name ends in, since buf's
// SERVICE_SUFFIX rule says so. A name that already has it keeps it rather
// than gaining a second one.
const StubServiceSuffix = "Service"

// Stub generates a protobuf service stub in the repository's proto root,
// under "elephant/<application>/v1", declaring the package
// "elephant.<application>.v1".
//
// That is a native service: the versioned layout is what makes it one, so it
// serves Connect only, implements connect-go's own handler interface and may
// declare streaming methods. The flat layout the fleet grew up with is still
// discovered and generated for; nothing new is written into it.
//
// The directory mirrors the package because buf checks one against the other
// relative to the module root, which is the proto root. A stub passes buf's
// STANDARD lint rules as it is written, with no exemptions, which is what
// "mage rpc:lint" reports.
func Stub(application, service, method string) error {
	if !applicationExp.MatchString(application) {
		return errors.New(
			"application must start with a letter and only contain the characters a-z, 0-9, or _")
	}

	if !messageExp.MatchString(service) {
		return fmt.Errorf("service %s", messageConstraint)
	}

	if !messageExp.MatchString(method) {
		return fmt.Errorf("method %s", messageConstraint)
	}

	if !strings.HasSuffix(service, StubServiceSuffix) {
		service += StubServiceSuffix
	}

	root, err := stubRoot()
	if err != nil {
		return err
	}

	module, err := modulePath()
	if err != nil {
		return err
	}

	dir := filepath.Join(root, StubPackagePrefix, application, StubVersion)

	err = internal.EnsureDirectory(dir)
	if err != nil {
		return fmt.Errorf("ensure application directory: %w", err)
	}

	tpl, err := template.New("skeleton").Parse(stubTpl)
	if err != nil {
		return fmt.Errorf("invalid template: %w", err)
	}

	var buf bytes.Buffer

	err = tpl.Execute(&buf, stubData{
		Package: strings.Join([]string{
			StubPackagePrefix, application, StubVersion,
		}, "."),
		Service:   service,
		Method:    method,
		GoPackage: filepath.ToSlash(filepath.Join(module, dir)),
		GoPackageName: fmt.Sprintf("%s%s",
			strings.ReplaceAll(application, "_", ""), StubVersion),
	})
	if err != nil {
		return fmt.Errorf("templating error: %w", err)
	}

	err = os.WriteFile(
		filepath.Join(dir, serviceFileName),
		buf.Bytes(), 0o600)
	if err != nil {
		return fmt.Errorf("write service file: %w", err)
	}

	return nil
}

// stubRoot returns the directory a new service is stubbed into: the proto
// root of a repository that already has one, and "rpc" for a repository that
// has no protobuf sources yet.
func stubRoot() (string, error) {
	conf, err := loadConfig()
	if err != nil {
		return "", err
	}

	root := conf.ProtoRoot

	if root != "." {
		return root, nil
	}

	existing, err := discoverServices(conf)
	if err != nil {
		return "", err
	}

	if len(existing) > 0 {
		return ".", nil
	}

	return rpcDir, nil
}

const stubTpl = `syntax = "proto3";

package {{.Package}};

option go_package = "{{.GoPackage}};{{.GoPackageName}}";

service {{.Service}} {
  rpc {{.Method}}({{.Method}}Request) returns ({{.Method}}Response);
}

message {{.Method}}Request {
  string param = 1;
}

message {{.Method}}Response {}
`

type stubData struct {
	// Package is the full protobuf package, which the directory the file
	// is written into mirrors.
	Package string
	Service string
	Method  string
	// GoPackage is the import path of the generated code.
	GoPackage string
	// GoPackageName names the Go package separately from the import path,
	// which the versioned layout needs: the last element of the path is
	// the version, and every service in the repository would otherwise
	// generate a package called v1.
	GoPackageName string
}
