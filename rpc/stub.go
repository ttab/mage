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

// Stub generates a protobuf service stub in the repository's proto root,
// under "<application>/v1". That is buf's layout, and it is the one that
// leaves room for a second version of a declaration. The flat layout the
// fleet grew up with is still discovered and generated for; nothing new is
// written into it.
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

	root, err := stubRoot()
	if err != nil {
		return err
	}

	module, err := modulePath()
	if err != nil {
		return err
	}

	dir := filepath.Join(root, application, StubVersion)

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
		Application: application,
		Version:     StubVersion,
		Service:     service,
		Method:      method,
		GoPackage:   filepath.ToSlash(filepath.Join(module, dir)),
		GoPackageName: fmt.Sprintf("%s%s",
			strings.ReplaceAll(application, "_", ""), StubVersion),
	})
	if err != nil {
		return fmt.Errorf("templating error: %w", err)
	}

	err = os.WriteFile(
		filepath.Join(dir, "service.proto"),
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

package ttab.{{.Application}}.{{.Version}};

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
	Application string
	Version     string
	Service     string
	Method      string
	// GoPackage is the import path of the generated code.
	GoPackage string
	// GoPackageName names the Go package separately from the import path,
	// which the versioned layout needs: the last element of the path is
	// the version, and every service in the repository would otherwise
	// generate a package called v1.
	GoPackageName string
}
