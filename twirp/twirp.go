package twirp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"github.com/magefile/mage/sh"
	"github.com/ttab/mage/internal"
)

const (
	twirpToolsImage = "ghcr.io/ttab/elephant-twirptools:v8.1.3-1"
)

// TwirpTools returns a command function that runs programs from the
// elephant-twirptools image as the current user with the current working
// directory mounted.
func TwirpTools() func(args ...string) error {
	uid := os.Getuid()
	gid := os.Getgid()
	cwd := internal.MustGetWD()

	return sh.RunCmd("docker", "run", "--rm",
		"-v", fmt.Sprintf("%s:/usr/src", cwd),
		"-u", fmt.Sprintf("%d:%d", uid, gid),
		twirpToolsImage,
	)
}

// Generate runs protoc to compile the service declaration and generate an
// openapi3 specification.
func Generate(name string) error {
	err := internal.EnsureDirectory(filepath.Join("rpc", name))
	if err != nil {
		return err
	}

	err = internal.EnsureDirectory("docs")
	if err != nil {
		return err
	}

	version := versionFromEnv()

	tool := TwirpTools()

	err = tool("protoc",
		"--go_out=.",
		"--twirp_out=.",
		"--openapi3_out=./docs",
		fmt.Sprintf(
			"--openapi3_opt=application=%s,version=%s",
			name, version,
		),
		filepath.Join("rpc", name, "service.proto"))
	if err != nil {
		return fmt.Errorf("run protoc: %w", err)
	}

	specPath := filepath.Join(
		"docs", name+"-openapi.json",
	)

	specData, err := os.ReadFile(specPath)
	if err != nil {
		return fmt.Errorf("read openapi spec: %w", err)
	}

	var spec map[string]interface{}

	err = json.Unmarshal(specData, &spec)
	if err != nil {
		return fmt.Errorf("unmarshal openapi spec: %w", err)
	}

	spec["servers"] = []map[string]interface{}{
		{
			"url": fmt.Sprintf("https://%s.api.tt.se", name),
		},
		{
			"url": fmt.Sprintf("https://%s.stage.api.tt.se", name),
		},
	}

	specData, err = json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal openapi spec: %w", err)
	}

	err = os.WriteFile(specPath, specData, 0o600)
	if err != nil {
		return fmt.Errorf("write openapi spec: %w", err)
	}

	return nil
}

func versionFromEnv() string {
	version := os.Getenv("API_VERSION")
	if version != "" {
		return version
	}

	v, err := internal.OutputSilent("git", "describe", "--tags", "HEAD")
	if err == nil {
		return strings.TrimSpace(v)
	}

	return "v0.0.0"
}

const stubTpl = `syntax = "proto3";

package ttab.{{.Application}};

option go_package = "./rpc/{{.Application}}";

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
}

var applicationExp = regexp.MustCompile(`^[a-z][0-9a-z_]*$`)

var (
	messageExp        = regexp.MustCompile(`^[A-Z][0-9a-za-z]*$`)
	messageConstraint = "must start with an uppercase letter and only contain the characters a-z, A-Z, 0-9"
)

// Stub generates a protobuf service stub.
func Stub(application, service, method string) error {
	if !applicationExp.MatchString(application) {
		return errors.New("application must start with a letter and only contain the characters a-z, 0-9, or _")
	}

	if !messageExp.MatchString(service) {
		return fmt.Errorf("service %s", messageConstraint)
	}

	if !messageExp.MatchString(service) {
		return fmt.Errorf("method %s", messageConstraint)
	}

	dir := filepath.Join("rpc", application)
	err := internal.EnsureDirectory(dir)
	if err != nil {
		return err
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
