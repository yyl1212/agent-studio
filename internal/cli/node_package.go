package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/yyl1212/agent-studio/internal/buildinfo"
	"github.com/yyl1212/agent-studio/internal/nodepackage"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
	"golang.org/x/mod/modfile"
)

type inspectOutput struct {
	Package      nodepackage.Summary      `json:"package"`
	Registration nodepackage.Registration `json:"registration"`
	Diagnostics  []nodepackage.Diagnostic `json:"diagnostics"`
}

func inspectNodePackage(ctx context.Context, start, importPath string, jsonOutput bool, stdout, stderr io.Writer) int {
	return inspectNodePackageWithInspector(ctx, start, importPath, jsonOutput, stdout, stderr, nodepackage.NewInspector(buildinfo.Current()))
}

func inspectNodePackageWithInspector(
	ctx context.Context,
	start string,
	importPath string,
	jsonOutput bool,
	stdout io.Writer,
	stderr io.Writer,
	inspector nodepackage.Inspector,
) int {
	root, err := findProjectRoot(start)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "locate project: %v\n", err)
		return 1
	}
	inspection := inspector.Inspect(ctx, root, importPath)
	diagnostics := nodepackage.SortDiagnostics(inspection.Diagnostics)
	registration := inspection.Record.Registration
	registration.Nodes = append([]nodepackage.NodeRef(nil), registration.Nodes...)
	if registration.Nodes == nil {
		registration.Nodes = []nodepackage.NodeRef{}
	}
	output := inspectOutput{
		Package: inspection.Record.Summary, Registration: registration, Diagnostics: diagnostics,
	}
	if jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(output); err != nil {
			_, _ = fmt.Fprintf(stderr, "encode node package inspection: %v\n", err)
			return 1
		}
	} else {
		writeHumanInspection(stdout, inspection, diagnostics)
	}
	if nodepackage.HasErrors(diagnostics) {
		return 1
	}
	return 0
}

func writeHumanInspection(stdout io.Writer, inspection nodepackage.Inspection, diagnostics []nodepackage.Diagnostic) {
	if inspection.Record.Summary.Name != "" {
		version := inspection.Record.Summary.Version
		if version == "" {
			version = string(inspection.Record.Summary.Source)
		}
		_, _ = fmt.Fprintf(stdout, "包: %s\n", inspection.Record.Summary.Name)
		_, _ = fmt.Fprintf(stdout, "版本/状态: %s\n", version)
		_, _ = fmt.Fprintf(stdout, "Node API: %s\n", inspection.Record.Manifest.Compatibility.NodeAPI)
		_, _ = fmt.Fprintf(stdout, "Runtime: %s <= version < %s\n",
			inspection.Record.Manifest.Compatibility.Runtime.MinVersion,
			inspection.Record.Manifest.Compatibility.Runtime.MaxVersionExclusive,
		)
		nodes := append([]nodepackage.NodeRef(nil), inspection.Record.Registration.Nodes...)
		sort.Slice(nodes, func(left, right int) bool {
			if nodes[left].Type != nodes[right].Type {
				return nodes[left].Type < nodes[right].Type
			}
			return nodes[left].Version < nodes[right].Version
		})
		_, _ = io.WriteString(stdout, "节点:\n")
		for _, node := range nodes {
			_, _ = fmt.Fprintf(stdout, "- %s@%s\n", node.Type, node.Version)
		}
	}
	for _, diagnostic := range diagnostics {
		_, _ = fmt.Fprintf(stdout, "[%s] %s: %s\n", diagnostic.Severity, diagnostic.Code, diagnostic.Message)
	}
}

type packageInitInput struct {
	DisplayName         string
	Description         string
	License             string
	Repository          string
	RuntimeMin          string
	RuntimeMaxExclusive string
}

func missingPackageInitFlags(input packageInitInput) []string {
	missing := make([]string, 0, 5)
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "--display-name", value: input.DisplayName},
		{name: "--license", value: input.License},
		{name: "--repository", value: input.Repository},
		{name: "--runtime-min", value: input.RuntimeMin},
		{name: "--runtime-max-exclusive", value: input.RuntimeMaxExclusive},
	} {
		if field.value == "" {
			missing = append(missing, field.name)
		}
	}
	return missing
}

func initializeNodePackage(ctx context.Context, start string, input packageInitInput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := findGoModuleRoot(start)
	if err != nil {
		return err
	}
	modulePathFile := filepath.Join(root, "go.mod")
	moduleData, err := os.ReadFile(modulePathFile)
	if err != nil {
		return fmt.Errorf("read Go module %s: %w", modulePathFile, err)
	}
	parsedModule, err := modfile.Parse(modulePathFile, moduleData, nil)
	if err != nil {
		return fmt.Errorf("parse Go module %s: %w", modulePathFile, err)
	}
	if parsedModule.Module == nil || parsedModule.Module.Mod.Path == "" {
		return fmt.Errorf("parse Go module %s: module path is required", modulePathFile)
	}
	manifest := nodepackage.Manifest{
		APIVersion: nodepackage.APIVersion,
		Kind:       nodepackage.Kind,
		Metadata: nodepackage.Metadata{
			Name:        parsedModule.Module.Mod.Path,
			DisplayName: input.DisplayName,
			Description: input.Description,
			License:     input.License,
			Repository:  input.Repository,
		},
		Compatibility: nodepackage.Compatibility{
			NodeAPI: agentnode.APIVersion,
			Runtime: nodepackage.RuntimeRange{
				MinVersion:          input.RuntimeMin,
				MaxVersionExclusive: input.RuntimeMaxExclusive,
			},
		},
		Registrations: []nodepackage.Registration{},
	}
	encoded, err := nodepackage.Encode(manifest)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return writeNewFileAtomic(filepath.Join(root, nodepackage.Filename), encoded, os.Link)
}

func writeNewFileAtomic(path string, data []byte, publish func(string, string) error) (returnErr error) {
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-")
	if err != nil {
		return fmt.Errorf("create node package manifest temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		if removeErr := os.Remove(temporaryPath); returnErr == nil && removeErr != nil && !os.IsNotExist(removeErr) {
			returnErr = fmt.Errorf("remove node package manifest temporary file: %w", removeErr)
		}
	}()
	if err := temporary.Chmod(0o644); err != nil {
		return fmt.Errorf("chmod node package manifest temporary file: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("write node package manifest temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync node package manifest temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close node package manifest temporary file: %w", err)
	}
	if err := publish(temporaryPath, path); err != nil {
		return fmt.Errorf("publish node package manifest %s: %w", path, err)
	}
	return nil
}
