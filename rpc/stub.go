package rpc

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"text/template"

	"github.com/ttab/mage/internal"
)

var applicationExp = regexp.MustCompile(`^[a-z][0-9a-z_]*$`)

var (
	messageExp        = regexp.MustCompile(`^[A-Z][0-9a-zA-Z]*$`)
	messageConstraint = "must start with an uppercase letter and only contain the characters a-z, A-Z, 0-9"
)

// Stub generates a protobuf service stub, in the repository's proto root.
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

	dir := filepath.Join(root, application)

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
		Service:     service,
		Method:      method,
		GoPackage:   filepath.ToSlash(filepath.Join(module, dir)),
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

	existing, err := filepath.Glob(filepath.Join("*", "service.proto"))
	if err != nil {
		return "", fmt.Errorf("glob for proto services: %w", err)
	}

	if len(existing) > 0 {
		return ".", nil
	}

	return rpcDir, nil
}

const stubTpl = `syntax = "proto3";

package ttab.{{.Application}};

option go_package = "{{.GoPackage}}";

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
	Service     string
	Method      string
	GoPackage   string
}
