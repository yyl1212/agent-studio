package scaffold

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yyl1212/agent-studio/internal/nodemanifest"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

func TestPlanEchoScaffold(t *testing.T) {
	manifest := nodemanifest.Manifest{APIVersion: agentnode.APIVersion, Nodes: []nodemanifest.NodePackage{}}
	plan, err := Plan(Request{RootDir: "/repo", ModulePath: "example.com/studio", Name: "my-echo", Manifest: manifest})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := plan.Directory, filepath.FromSlash("/repo/extensions/my-echo"); got != want {
		t.Fatalf("directory=%q, want %q", got, want)
	}
	assertPlannedFileContains(t, plan, "node.go", `Type:        "extension.my-echo"`)
	assertPlannedFileContains(t, plan, "node.go", "package myecho")
	assertPlannedFileContains(t, plan, "node.go", `Title:       "My Echo"`)
	assertPlannedFileContains(t, plan, "node.go", "Required: true")
	assertPlannedFileContains(t, plan, "node_test.go", "agenttest.Run")
	assertPlannedFileContains(t, plan, "README.md", "node test ./extensions/my-echo")
	if got := plan.Manifest.Nodes[0].Package; got != "example.com/studio/extensions/my-echo" {
		t.Fatalf("package=%q", got)
	}
	if got, want := plan.ManifestPath, filepath.FromSlash("/repo/agent-studio.nodes.yaml"); got != want {
		t.Fatalf("manifest path=%q, want %q", got, want)
	}
}

func TestPlanRejectsInvalidNames(t *testing.T) {
	for _, name := range []string{"", "Echo", "echo_node", "../echo", "echo--node", "type"} {
		t.Run(name, func(t *testing.T) {
			_, err := Plan(Request{
				RootDir:    "/repo",
				ModulePath: "example.com/studio",
				Name:       name,
				Manifest:   nodemanifest.Manifest{APIVersion: agentnode.APIVersion, Nodes: []nodemanifest.NodePackage{}},
			})
			if err == nil {
				t.Fatalf("name %q was accepted", name)
			}
		})
	}
}

func TestApplyWritesFilesAndManifest(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, "agent-studio.nodes.yaml")
	original := "apiVersion: agent-studio.dev/v1alpha1\nnodes: []\n"
	if err := os.WriteFile(manifestPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := Plan(Request{
		RootDir:    root,
		ModulePath: "example.com/studio",
		Name:       "echo",
		Manifest:   nodemanifest.Manifest{APIVersion: agentnode.APIVersion, Nodes: []nodemanifest.NodePackage{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(plan, ApplyDeps{}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"node.go", "node_test.go", "README.md"} {
		if _, err := os.Stat(filepath.Join(root, "extensions", "echo", name)); err != nil {
			t.Fatalf("generated file %s: %v", name, err)
		}
	}
	manifest, err := nodemanifest.Load(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Nodes) != 1 || manifest.Nodes[0].Package != "example.com/studio/extensions/echo" {
		t.Fatalf("manifest=%#v", manifest)
	}
}

func TestApplyRejectsNonEmptyDirectoryWithoutChangingFiles(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "extensions", "echo")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(target, "keep.txt")
	if err := os.WriteFile(existing, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := testScaffoldPlan(t, root)
	err := Apply(plan, ApplyDeps{})
	if err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("error=%v", err)
	}
	data, readErr := os.ReadFile(existing)
	if readErr != nil || string(data) != "keep" {
		t.Fatalf("existing data=%q err=%v", data, readErr)
	}
}

func TestApplyRollsBackNewDirectoryWhenManifestRenameFails(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, "agent-studio.nodes.yaml")
	original := []byte("apiVersion: agent-studio.dev/v1alpha1\nnodes: []\n")
	if err := os.WriteFile(manifestPath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(root, "extensions", "keep", "file.txt")
	if err := os.MkdirAll(filepath.Dir(sibling), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sibling, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := testScaffoldPlan(t, root)
	wantErr := errors.New("permission denied")
	err := Apply(plan, ApplyDeps{Rename: func(string, string) error { return wantErr }})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error=%v", err)
	}
	if _, statErr := os.Stat(plan.Directory); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("new directory remains: %v", statErr)
	}
	data, readErr := os.ReadFile(manifestPath)
	if readErr != nil || string(data) != string(original) {
		t.Fatalf("manifest=%q err=%v", data, readErr)
	}
	data, readErr = os.ReadFile(sibling)
	if readErr != nil || string(data) != "keep" {
		t.Fatalf("sibling=%q err=%v", data, readErr)
	}
}

func TestApplyRejectsExtensionsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "agent-studio.nodes.yaml"), []byte("apiVersion: agent-studio.dev/v1alpha1\nnodes: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "extensions")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	err := Apply(testScaffoldPlan(t, root), ApplyDeps{})
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("error=%v", err)
	}
	entries, readErr := os.ReadDir(outside)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("outside entries=%v err=%v", entries, readErr)
	}
}

func testScaffoldPlan(t *testing.T, root string) ScaffoldPlan {
	t.Helper()
	plan, err := Plan(Request{
		RootDir:    root,
		ModulePath: "example.com/studio",
		Name:       "echo",
		Manifest:   nodemanifest.Manifest{APIVersion: agentnode.APIVersion, Nodes: []nodemanifest.NodePackage{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func assertPlannedFileContains(t *testing.T, plan ScaffoldPlan, name, want string) {
	t.Helper()
	data, exists := plan.Files[name]
	if !exists || !strings.Contains(string(data), want) {
		t.Fatalf("file %s does not contain %q:\n%s", name, want, data)
	}
}
