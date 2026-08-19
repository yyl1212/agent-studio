package scaffold

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yyl1212/agent-studio/internal/nodemanifest"
	"github.com/yyl1212/agent-studio/internal/nodepackage"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

func TestPlanEchoScaffold(t *testing.T) {
	manifest := nodemanifest.Manifest{APIVersion: agentnode.APIVersion, Nodes: []nodemanifest.NodePackage{}}
	packageManifest := testPackageManifest()
	plan, err := Plan(Request{
		RootDir: "/repo", ModulePath: "example.com/studio", Name: "my-echo",
		Manifest: manifest, PackageManifest: packageManifest,
	})
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
	wantRegistration := nodepackage.Registration{
		Package: "example.com/studio/extensions/my-echo",
		Nodes:   []nodepackage.NodeRef{{Type: "extension.my-echo", Version: "1.0.0"}},
	}
	if len(plan.PackageManifest.Registrations) != 1 || plan.PackageManifest.Registrations[0].Package != wantRegistration.Package ||
		len(plan.PackageManifest.Registrations[0].Nodes) != 1 || plan.PackageManifest.Registrations[0].Nodes[0] != wantRegistration.Nodes[0] {
		t.Fatalf("package manifest=%+v", plan.PackageManifest)
	}
	if got, want := plan.PackageManifestPath, filepath.FromSlash("/repo/agent-studio.node-package.json"); got != want {
		t.Fatalf("package manifest path=%q, want %q", got, want)
	}
}

func TestPlanRejectsInvalidNames(t *testing.T) {
	for _, name := range []string{"", "Echo", "echo_node", "../echo", "echo--node", "type"} {
		t.Run(name, func(t *testing.T) {
			_, err := Plan(Request{
				RootDir:         "/repo",
				ModulePath:      "example.com/studio",
				Name:            name,
				Manifest:        nodemanifest.Manifest{APIVersion: agentnode.APIVersion, Nodes: []nodemanifest.NodePackage{}},
				PackageManifest: testPackageManifest(),
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
	packageManifestPath := writePackageManifestFixture(t, root)
	plan, err := Plan(Request{
		RootDir:         root,
		ModulePath:      "example.com/studio",
		Name:            "echo",
		Manifest:        nodemanifest.Manifest{APIVersion: agentnode.APIVersion, Nodes: []nodemanifest.NodePackage{}},
		PackageManifest: testPackageManifest(),
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
	packageData, err := os.ReadFile(packageManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	packageManifest, err := nodepackage.Parse(packageManifestPath, packageData)
	if err != nil {
		t.Fatal(err)
	}
	if len(packageManifest.Registrations) != 1 || packageManifest.Registrations[0].Nodes[0].Type != "extension.echo" {
		t.Fatalf("package manifest=%+v", packageManifest)
	}
}

func TestApplyRejectsNonEmptyDirectoryWithoutChangingFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "agent-studio.nodes.yaml"), []byte("apiVersion: agent-studio.dev/v1alpha1\nnodes: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writePackageManifestFixture(t, root)
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
	writePackageManifestFixture(t, root)
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

func TestApplyRollsBackBothManifestsWhenSecondRenameFails(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, "agent-studio.nodes.yaml")
	originalManifest := []byte("apiVersion: agent-studio.dev/v1alpha1\nnodes: []\n")
	if err := os.WriteFile(manifestPath, originalManifest, 0o644); err != nil {
		t.Fatal(err)
	}
	packageManifestPath := writePackageManifestFixture(t, root)
	originalPackageManifest, err := os.ReadFile(packageManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	plan := testScaffoldPlan(t, root)
	wantErr := errors.New("second rename denied")
	renameCalls := 0
	err = Apply(plan, ApplyDeps{Rename: func(oldPath, newPath string) error {
		renameCalls++
		if renameCalls == 2 {
			return wantErr
		}
		return os.Rename(oldPath, newPath)
	}})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error=%v", err)
	}
	if _, statErr := os.Stat(plan.Directory); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("new directory remains: %v", statErr)
	}
	assertFileBytes(t, manifestPath, originalManifest)
	assertFileBytes(t, packageManifestPath, originalPackageManifest)
}

func TestApplyReportsManifestRestoreFailure(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, "agent-studio.nodes.yaml")
	if err := os.WriteFile(manifestPath, []byte("apiVersion: agent-studio.dev/v1alpha1\nnodes: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writePackageManifestFixture(t, root)
	plan := testScaffoldPlan(t, root)
	replaceErr := errors.New("second rename denied")
	restoreErr := errors.New("restore denied")
	renameCalls := 0
	err := Apply(plan, ApplyDeps{Rename: func(oldPath, newPath string) error {
		renameCalls++
		switch renameCalls {
		case 1:
			return os.Rename(oldPath, newPath)
		case 2:
			return replaceErr
		default:
			return restoreErr
		}
	}})
	if !errors.Is(err, replaceErr) || !errors.Is(err, restoreErr) || !strings.Contains(err.Error(), "restore manifests") {
		t.Fatalf("error=%v", err)
	}
	if _, statErr := os.Stat(plan.Directory); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("new directory remains: %v", statErr)
	}
}

func TestApplyRejectsExtensionsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "agent-studio.nodes.yaml"), []byte("apiVersion: agent-studio.dev/v1alpha1\nnodes: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writePackageManifestFixture(t, root)
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
		RootDir:         root,
		ModulePath:      "example.com/studio",
		Name:            "echo",
		Manifest:        nodemanifest.Manifest{APIVersion: agentnode.APIVersion, Nodes: []nodemanifest.NodePackage{}},
		PackageManifest: testPackageManifest(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func testPackageManifest() nodepackage.Manifest {
	return nodepackage.Manifest{
		APIVersion: nodepackage.APIVersion,
		Kind:       nodepackage.Kind,
		Metadata: nodepackage.Metadata{
			Name: "example.com/studio", DisplayName: "Studio Nodes", Description: "",
			License: "Apache-2.0", Repository: "https://example.com/studio",
		},
		Compatibility: nodepackage.Compatibility{
			NodeAPI: agentnode.APIVersion,
			Runtime: nodepackage.RuntimeRange{MinVersion: "v0.2.0", MaxVersionExclusive: "v0.4.0"},
		},
		Registrations: []nodepackage.Registration{},
	}
}

func writePackageManifestFixture(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, nodepackage.Filename)
	data, err := nodepackage.Encode(testPackageManifest())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertFileBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(want) {
		t.Fatalf("file %s=%q want=%q err=%v", path, got, want, err)
	}
}

func assertPlannedFileContains(t *testing.T, plan ScaffoldPlan, name, want string) {
	t.Helper()
	data, exists := plan.Files[name]
	if !exists || !strings.Contains(string(data), want) {
		t.Fatalf("file %s does not contain %q:\n%s", name, want, data)
	}
}
