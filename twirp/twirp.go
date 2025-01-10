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
	twirpToolsImage = "ghcr.io/ttab/elephant-twirptools:v8.1.3-4"
)

// TwirpTools returns a command function that runs programs from the
// elephant-twirptools image as the current user with the current working
// directory mounted.
func TwirpTools(exposeDirs ...string) func(args ...string) error {
	uid := os.Getuid()
	gid := os.Getgid()
	cwd := internal.MustGetWD()

	args := []string{
		"run", "--rm",
		"-v", fmt.Sprintf("%s:/usr/src", cwd),
		"-u", fmt.Sprintf("%d:%d", uid, gid),
	}

	for _, p := range exposeDirs {
		args = append(args,
			"-v", fmt.Sprintf("%s:%s", p, p))
	}

	args = append(args, twirpToolsImage)

	return sh.RunCmd("docker", args...)
}

// Generate runs protoc to compile the service declarations and generate
// openapi3 specifications for the services in the project.
func Generate() error {
	protoRoot := "."

	rpcRooted, err := internal.DirectoryExists("rpc")
	if err != nil {
		return fmt.Errorf("check for './rpc' directory: %w", err)
	}

	if rpcRooted {
		protoRoot = "rpc"
	}

	protoFiles, err := filepath.Glob(filepath.Join(protoRoot, "*", "service.proto"))
	if err != nil {
		return fmt.Errorf("glob for proto services: %w", err)
	}

	for _, p := range protoFiles {
		service := filepath.Base(filepath.Dir(p))

		err := generateService(service)
		if err != nil {
			return fmt.Errorf("generate %q: %w", service, err)
		}
	}

	return nil
}

func generateService(name string) error {
	protoRoot := "."

	rpcRooted, err := internal.DirectoryExists("rpc")
	if err != nil {
		return fmt.Errorf("check for './rpc' directory: %w", err)
	}

	if rpcRooted {
		protoRoot = "rpc"
	}

	err = internal.EnsureDirectory("docs")
	if err != nil {
		return err
	}

	version := versionFromEnv()

	protocArgs := []string{
		"protoc",
		"--go_out=.",
		"--go_opt=paths=source_relative",
		"--twirp_out=.",
		"--twirp_opt=paths=source_relative",
		"--openapi3_out=./docs",
		"--proto_path", protoRoot,
		fmt.Sprintf(
			"--openapi3_opt=application=%s,version=%s",
			name, version,
		),
	}

	var toolDirs []string

	// If we have elephant API as a dependency, automatically add it to the
	// proto path.
	eleAPI := tryElephantAPIDir()
	if eleAPI != "" {
		toolDirs = append(toolDirs, eleAPI)

		protocArgs = append(protocArgs,
			"--proto_path", eleAPI)
	}

	protoFiles, err := filepath.Glob(filepath.Join(protoRoot, name, "*.proto"))
	if err != nil {
		return fmt.Errorf("glob for proto files: %w", err)
	}

	protocArgs = append(protocArgs, protoFiles...)

	tool := TwirpTools(toolDirs...)

	err = tool(protocArgs...)
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
			"url": fmt.Sprintf("https://%s.api.stage.tt.se", name),
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

func tryElephantAPIDir() string {
	elephantAPIDir, err := sh.Output("go", "list", "-m",
		"-f", "{{.Dir}}",
		"github.com/ttab/elephant-api")
	if err != nil {
		return ""
	}

	return elephantAPIDir
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

var applicationExp = regexp.MustCompile(`^[a-z][0-9a-z_]*$`)

var (
	messageExp        = regexp.MustCompile(`^[A-Z][0-9a-zA-Z]*$`)
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

	if !messageExp.MatchString(method) {
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
