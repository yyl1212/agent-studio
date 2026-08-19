package nodegen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yyl1212/agent-studio/internal/nodemanifest"
	"github.com/yyl1212/agent-studio/internal/nodepackage"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

func TestGenerateGroupsExternalRegistrationsByModule(t *testing.T) {
	root := nodegenRoot(t)
	output := filepath.Join(root, "generated", "nodes_gen.go")
	manifest := nodemanifest.Manifest{APIVersion: agentnode.APIVersion, Nodes: []nodemanifest.NodePackage{
		{Package: "example.com/external/zeta"},
		{Package: "example.com/external/alpha"},
	}}
	generator := Generator{Inspector: externalModuleInspector(t)}
	changed, err := generator.Generate(context.Background(), root, manifest, output)
	if err != nil || !changed {
		t.Fatalf("changed=%t err=%v", changed, err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	generated := string(data)
	if strings.Count(generated, "registry.RegisterPackage(record") != 1 ||
		!strings.Contains(generated, `{Type: "external.alpha", Version: "1.0.0"}`) ||
		!strings.Contains(generated, `{Type: "external.zeta", Version: "1.0.0"}`) ||
		strings.Index(generated, "example.com/external/alpha") > strings.Index(generated, "example.com/external/zeta") {
		t.Fatalf("generated:\n%s", generated)
	}
}

func TestRenderUsesDistinctRecordsForMultipleModules(t *testing.T) {
	generated, err := render([]generationGroup{
		{
			summary: nodepackage.Summary{Name: "example.com/alpha", Source: nodepackage.SourceModule},
			registrations: []nodepackage.Registration{{
				Package: "example.com/alpha/node",
				Nodes:   []nodepackage.NodeRef{{Type: "alpha.node", Version: "1.0.0"}},
			}},
		},
		{
			summary: nodepackage.Summary{Name: "example.com/zeta", Source: nodepackage.SourceModule},
			registrations: []nodepackage.Registration{{
				Package: "example.com/zeta/node",
				Nodes:   []nodepackage.NodeRef{{Type: "zeta.node", Version: "1.0.0"}},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	source := string(generated)
	for _, want := range []string{
		"record0 := nodepackage.RuntimeRecord",
		"registry.RegisterPackage(record0",
		"record1 := nodepackage.RuntimeRecord",
		"registry.RegisterPackage(record1",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("generated output missing %q:\n%s", want, source)
		}
	}
}

func externalModuleInspector(t *testing.T) nodepackage.Inspector {
	t.Helper()
	packageManifest := nodepackage.Manifest{
		APIVersion: nodepackage.APIVersion, Kind: nodepackage.Kind,
		Metadata:      nodepackage.Metadata{Name: "example.com/external", DisplayName: "External Nodes", Description: "", License: "Apache-2.0", Repository: "https://example.com/external"},
		Compatibility: nodepackage.Compatibility{NodeAPI: agentnode.APIVersion, Runtime: nodepackage.RuntimeRange{MinVersion: "v0.2.0", MaxVersionExclusive: "v0.4.0"}},
		Registrations: []nodepackage.Registration{
			{Package: "example.com/external/alpha", Nodes: []nodepackage.NodeRef{{Type: "external.alpha", Version: "1.0.0"}}},
			{Package: "example.com/external/zeta", Nodes: []nodepackage.NodeRef{{Type: "external.zeta", Version: "1.0.0"}}},
		},
	}
	manifestData, err := nodepackage.Encode(packageManifest)
	if err != nil {
		t.Fatal(err)
	}
	return nodepackage.Inspector{
		Command: func(_ context.Context, _ string, _ map[string]string, _ string, args ...string) ([]byte, error) {
			importPath := args[len(args)-1]
			return json.Marshal(map[string]any{
				"ImportPath": importPath,
				"Dir":        "/external/package",
				"Module":     map[string]any{"Path": "example.com/external", "Version": "v0.3.0", "Dir": "/external"},
			})
		},
		ReadFile: func(path string) ([]byte, error) {
			switch path {
			case "/external/agent-studio.node-package.json":
				return manifestData, nil
			case "/external/go.mod":
				return []byte("module example.com/external\n\ngo 1.26.0\n\nrequire github.com/yyl1212/agent-studio v0.2.0\n"), nil
			default:
				return nil, fmt.Errorf("unexpected read %s", path)
			}
		},
		RuntimeVersion: "v0.3.0", SDKVersion: "0.2.0", NodeAPIVersion: agentnode.APIVersion,
	}
}

func TestGenerateIsSortedAndStable(t *testing.T) {
	root := nodegenRoot(t)
	output := filepath.Join(root, "generated", "nodes_gen.go")
	generator := Generator{Inspector: externalModuleInspector(t)}
	manifest := nodemanifest.Manifest{APIVersion: agentnode.APIVersion, Nodes: []nodemanifest.NodePackage{
		{Package: "example.com/external/zeta"},
		{Package: "example.com/external/alpha"},
	}}
	changed, err := generator.Generate(context.Background(), root, manifest, output)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	first, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/nodes_gen.golden")
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(want) {
		t.Fatalf("generated:\n%s\nwant:\n%s", first, want)
	}
	changed, err = generator.Generate(context.Background(), root, manifest, output)
	if err != nil || changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	after, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("mtime changed: before=%v after=%v", before.ModTime(), after.ModTime())
	}
}

func TestGeneratePreservesOldFileWhenPackageValidationFails(t *testing.T) {
	root := nodegenRoot(t)
	output := filepath.Join(root, "nodes_gen.go")
	goModPath := filepath.Join(root, "go.mod")
	goSumPath := filepath.Join(root, "go.sum")
	if err := os.WriteFile(goSumPath, []byte("example.com/dependency v1.0.0 h1:test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("old-content"), 0o600); err != nil {
		t.Fatal(err)
	}
	goModBefore, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatal(err)
	}
	goSumBefore, err := os.ReadFile(goSumPath)
	if err != nil {
		t.Fatal(err)
	}
	inspector := externalModuleInspector(t)
	inspector.Command = func(context.Context, string, map[string]string, string, ...string) ([]byte, error) {
		return []byte("/private/token"), errors.New("package unavailable")
	}
	generator := Generator{Inspector: inspector}
	manifest := nodemanifest.Manifest{APIVersion: agentnode.APIVersion, Nodes: []nodemanifest.NodePackage{
		{Package: "example.com/external/alpha"},
		{Package: "example.com/external/zeta"},
	}}
	changed, err := generator.Generate(context.Background(), root, manifest, output)
	if changed || err == nil || strings.Contains(err.Error(), "/private/token") {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	data, readErr := os.ReadFile(output)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if got, want := string(data), "old-content"; got != want {
		t.Fatalf("output=%q, want %q", got, want)
	}
	goModAfter, err := os.ReadFile(goModPath)
	if err != nil || string(goModAfter) != string(goModBefore) {
		t.Fatalf("go.mod changed: err=%v\nbefore:\n%s\nafter:\n%s", err, goModBefore, goModAfter)
	}
	goSumAfter, err := os.ReadFile(goSumPath)
	if err != nil || string(goSumAfter) != string(goSumBefore) {
		t.Fatalf("go.sum changed: err=%v\nbefore:\n%s\nafter:\n%s", err, goSumBefore, goSumAfter)
	}
	entries, readDirErr := os.ReadDir(root)
	if readDirErr != nil {
		t.Fatal(readDirErr)
	}
	if len(entries) != 3 || entries[0].Name() != "go.mod" || entries[1].Name() != "go.sum" || entries[2].Name() != "nodes_gen.go" {
		t.Fatalf("unexpected files after failure: %v", entries)
	}
}

func TestGenerateEmptyManifestProducesCompilableEntryPoint(t *testing.T) {
	root := nodegenRoot(t)
	output := filepath.Join(root, "nodes_gen.go")
	changed, err := (Generator{}).Generate(context.Background(), root, nodemanifest.Manifest{
		APIVersion: agentnode.APIVersion,
		Nodes:      []nodemanifest.NodePackage{},
	}, output)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "func RegisterNodes(registry packageRegistry) error") ||
		!strings.Contains(string(data), "return nil") ||
		strings.Contains(string(data), `"fmt"`) {
		t.Fatalf("generated:\n%s", data)
	}
}

func nodegenRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/studio\n\ngo 1.26.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestGenerateRejectsSymlinkedOutputDirectory(t *testing.T) {
	root := nodegenRoot(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "generated")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := (Generator{}).Generate(context.Background(), root, nodemanifest.Manifest{
		APIVersion: agentnode.APIVersion,
		Nodes:      []nodemanifest.NodePackage{},
	}, filepath.Join(root, "generated", "nodes_gen.go"))
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("error=%v", err)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "nodes_gen.go")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("outside file exists: %v", statErr)
	}
}
