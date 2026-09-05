package rpc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

// docsDir is where the OpenAPI specifications are written, and where the
// READMEs across the fleet link to them.
const docsDir = "docs"

// generateOpenAPI writes docs/<service>-openapi.json for one service.
//
// Unlike the other plugins this one is not run through buf. twopdocs is
// built against a protobuf-go that reports FEATURE_SUPPORTS_EDITIONS without
// a minimum edition, which protoc accepts and buf rejects outright, so buf
// cannot run it at all. Driving the plugin from here is the same exchange
// buf would have made: a CodeGeneratorRequest built from the compiled
// descriptors on stdin, a CodeGeneratorResponse back.
func generateOpenAPI(s service, version string) error {
	set, err := descriptorSet(s.Dir)
	if err != nil {
		return err
	}

	generate := filesInDir(set, s.Dir)
	if len(generate) == 0 {
		return fmt.Errorf("no compiled proto files in %q", s.Dir)
	}

	parameter := fmt.Sprintf("application=%s,version=%s", s.Name, version)

	req := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: generate,
		Parameter:      &parameter,
		ProtoFile:      set.GetFile(),
	}

	files, err := runPlugin(goRun(openAPI3Module, OpenAPI3Version), req)
	if err != nil {
		return err
	}

	for _, f := range files {
		err := writeGenerated(docsDir, f)
		if err != nil {
			return err
		}
	}

	return stampSpec(filepath.Join(docsDir, s.Name+"-openapi.json"))
}

// descriptorSet compiles the workspace and returns the descriptors of the
// files in dir together with everything they import.
func descriptorSet(dir string) (*descriptorpb.FileDescriptorSet, error) {
	out, err := os.CreateTemp("", "mage-rpc-*.binpb")
	if err != nil {
		return nil, fmt.Errorf("create a temporary file for the descriptors: %w", err)
	}

	name := out.Name()

	defer func() {
		_ = os.Remove(name)
	}()

	err = out.Close()
	if err != nil {
		return nil, fmt.Errorf("close the temporary descriptor file: %w", err)
	}

	err = buf("build", "--as-file-descriptor-set", "--path", dir, "-o", name)
	if err != nil {
		return nil, fmt.Errorf("compile the protobuf sources in %q: %w", dir, err)
	}

	data, err := os.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("read the compiled descriptors: %w", err)
	}

	var set descriptorpb.FileDescriptorSet

	err = proto.Unmarshal(data, &set)
	if err != nil {
		return nil, fmt.Errorf("unmarshal the compiled descriptors: %w", err)
	}

	return &set, nil
}

// filesInDir returns the names of the compiled files that live directly in
// dir, which is the set protoc was handed for a service. Everything else in
// the descriptor set is an import, and is compiled but not generated for.
func filesInDir(set *descriptorpb.FileDescriptorSet, dir string) []string {
	var names []string

	for _, f := range set.GetFile() {
		if path.Dir(f.GetName()) == dir {
			names = append(names, f.GetName())
		}
	}

	return names
}

// runPlugin runs a protoc plugin over a request and returns the files it
// generated.
func runPlugin(
	command []string, req *pluginpb.CodeGeneratorRequest,
) ([]*pluginpb.CodeGeneratorResponse_File, error) {
	data, err := proto.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal the code generator request: %w", err)
	}

	var stdout bytes.Buffer

	cmd := exec.Command(command[0], command[1:]...) //nolint:gosec
	cmd.Stdin = bytes.NewReader(data)
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr

	err = cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("run %s: %w", strings.Join(command, " "), err)
	}

	var resp pluginpb.CodeGeneratorResponse

	err = proto.Unmarshal(stdout.Bytes(), &resp)
	if err != nil {
		return nil, fmt.Errorf("unmarshal the code generator response: %w", err)
	}

	if resp.GetError() != "" {
		return nil, fmt.Errorf("%s: %s",
			strings.Join(command, " "), resp.GetError())
	}

	return resp.GetFile(), nil
}

// writeGenerated writes one generated file below dir.
func writeGenerated(dir string, file *pluginpb.CodeGeneratorResponse_File) error {
	if file.GetInsertionPoint() != "" {
		return fmt.Errorf(
			"the generated file %q uses an insertion point, which is not supported",
			file.GetName())
	}

	name := path.Clean(file.GetName())
	if path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
		return fmt.Errorf("the generated file %q is outside %q",
			file.GetName(), dir)
	}

	target := filepath.Join(dir, filepath.FromSlash(name))

	err := os.MkdirAll(filepath.Dir(target), 0o700)
	if err != nil {
		return fmt.Errorf("create the directory for %q: %w", target, err)
	}

	err = os.WriteFile(target, []byte(file.GetContent()), 0o600)
	if err != nil {
		return fmt.Errorf("write %q: %w", target, err)
	}

	return nil
}

// stampSpec adds the servers the API is reachable at to a generated
// specification. The generator knows the application name but not where it
// is deployed.
func stampSpec(specPath string) error {
	name := strings.TrimSuffix(filepath.Base(specPath), "-openapi.json")

	specData, err := os.ReadFile(specPath)
	if err != nil {
		return fmt.Errorf("read openapi spec: %w", err)
	}

	var spec map[string]any

	err = json.Unmarshal(specData, &spec)
	if err != nil {
		return fmt.Errorf("unmarshal openapi spec: %w", err)
	}

	spec["servers"] = []map[string]any{
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
