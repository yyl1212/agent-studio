package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yyl1212/agent-studio/internal/nodemanifest"
	"github.com/yyl1212/agent-studio/internal/nodepackage"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

func TestRunNodeInitCreatesExtensionAndPrintsNextSteps(t *testing.T) {
	root := t.TempDir()
	writeNodeInitFixture(t, root)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(context.Background(), []string{"node", "init", "echo"}, &stdout, &stderr, appDependencies{
		workingDir: func() (string, error) { return root, nil },
		nodeInit:   initializeNode,
	})
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	wantOutput := "created extensions/echo\n" +
		"next: CGO_ENABLED=0 go run ./cmd/agent-studio node test ./extensions/echo\n" +
		"next: CGO_ENABLED=0 go run ./cmd/agent-studio generate\n"
	if stdout.String() != wantOutput {
		t.Fatalf("stdout=%q, want %q", stdout.String(), wantOutput)
	}
	for _, name := range []string{"node.go", "node_test.go", "README.md"} {
		if _, err := os.Stat(filepath.Join(root, "extensions", "echo", name)); err != nil {
			t.Fatalf("file %s: %v", name, err)
		}
	}
	manifest, err := nodemanifest.Load(filepath.Join(root, "agent-studio.nodes.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Nodes) != 1 || manifest.Nodes[0].Package != "example.com/project/extensions/echo" {
		t.Fatalf("manifest=%#v", manifest)
	}
	packageData, err := os.ReadFile(filepath.Join(root, nodepackage.Filename))
	if err != nil {
		t.Fatal(err)
	}
	packageManifest, err := nodepackage.Parse(nodepackage.Filename, packageData)
	if err != nil {
		t.Fatal(err)
	}
	if len(packageManifest.Registrations) != 1 || packageManifest.Registrations[0].Package != "example.com/project/extensions/echo" ||
		packageManifest.Registrations[0].Nodes[0].Type != "extension.echo" || packageManifest.Registrations[0].Nodes[0].Version != "1.0.0" {
		t.Fatalf("package manifest=%+v", packageManifest)
	}
}

func TestRunNodeInitRejectsMissingName(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(context.Background(), []string{"node", "init"}, &stdout, &stderr, appDependencies{})
	if code != 2 || stdout.Len() != 0 || stderr.String() != "node init requires exactly one name\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunNodeInitRejectsInvalidNameAsUsageError(t *testing.T) {
	called := false
	var stderr bytes.Buffer
	code := run(context.Background(), []string{"node", "init", "Bad"}, io.Discard, &stderr, appDependencies{
		nodeInit: func(context.Context, string, string) (nodeInitResult, error) {
			called = true
			return nodeInitResult{}, nil
		},
	})
	if code != 2 || called || !strings.Contains(stderr.String(), "use lowercase kebab-case") {
		t.Fatalf("code=%d called=%v stderr=%q", code, called, stderr.String())
	}
}

func TestRunNodeInitSecondAttemptDoesNotChangeGeneratedFiles(t *testing.T) {
	root := t.TempDir()
	writeNodeInitFixture(t, root)
	first, err := initializeNode(context.Background(), root, "echo")
	if err != nil {
		t.Fatal(err)
	}
	nodePath := filepath.Join(root, filepath.FromSlash(first.Directory), "node.go")
	before, err := os.ReadFile(nodePath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = initializeNode(context.Background(), root, "echo")
	if err == nil || (!strings.Contains(err.Error(), "duplicate package") && !strings.Contains(err.Error(), "not empty")) {
		t.Fatalf("error=%v", err)
	}
	after, readErr := os.ReadFile(nodePath)
	if readErr != nil || !bytes.Equal(before, after) {
		t.Fatalf("file changed=%v read error=%v", !bytes.Equal(before, after), readErr)
	}
}

func TestInitializeNodeRequiresPackageManifest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/project\n\ngo 1.26.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "agent-studio.nodes.yaml"), []byte("apiVersion: agent-studio.dev/v1alpha1\nnodes: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := initializeNode(context.Background(), root, "echo")
	if err == nil || !strings.Contains(err.Error(), nodepackage.Filename) {
		t.Fatalf("error=%v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "extensions")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("extensions created: %v", statErr)
	}
}

func TestInitializeNodeRejectsPackageManifestForAnotherModule(t *testing.T) {
	root := t.TempDir()
	writeNodeInitFixture(t, root)
	data, err := os.ReadFile(filepath.Join(root, nodepackage.Filename))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := nodepackage.Parse(nodepackage.Filename, data)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Metadata.Name = "example.com/other"
	encoded, err := nodepackage.Encode(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, nodepackage.Filename), encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = initializeNode(context.Background(), root, "echo")
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("error=%v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "extensions")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("extensions created: %v", statErr)
	}
}

func writeNodeInitFixture(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/project\n\ngo 1.26.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "agent-studio.nodes.yaml"), []byte("apiVersion: agent-studio.dev/v1alpha1\nnodes: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	packageManifest := nodepackage.Manifest{
		APIVersion: nodepackage.APIVersion,
		Kind:       nodepackage.Kind,
		Metadata: nodepackage.Metadata{
			Name: "example.com/project", DisplayName: "Project Nodes", Description: "",
			License: "Apache-2.0", Repository: "https://example.com/project",
		},
		Compatibility: nodepackage.Compatibility{
			NodeAPI: agentnode.APIVersion,
			Runtime: nodepackage.RuntimeRange{MinVersion: "v0.2.0", MaxVersionExclusive: "v0.4.0"},
		},
		Registrations: []nodepackage.Registration{},
	}
	data, err := nodepackage.Encode(packageManifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, nodepackage.Filename), data, 0o644); err != nil {
		t.Fatal(err)
	}
}
