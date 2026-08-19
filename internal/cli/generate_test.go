package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunGenerateReportsChangedAndUnchanged(t *testing.T) {
	tests := []struct {
		name    string
		result  generateResult
		wantOut string
	}{
		{name: "generated", result: generateResult{Path: "apps/api/internal/generated/nodes_gen.go", Changed: true}, wantOut: "generated apps/api/internal/generated/nodes_gen.go\n"},
		{name: "unchanged", result: generateResult{Path: "apps/api/internal/generated/nodes_gen.go"}, wantOut: "unchanged apps/api/internal/generated/nodes_gen.go\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run(context.Background(), []string{"generate"}, &stdout, &stderr, appDependencies{
				workingDir: func() (string, error) { return "/repo/subdirectory", nil },
				generate:   func(context.Context, string) (generateResult, error) { return test.result, nil },
			})
			if code != 0 || stdout.String() != test.wantOut || stderr.Len() != 0 {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunGenerateReturnsExecutionFailure(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(context.Background(), []string{"generate"}, &stdout, &stderr, appDependencies{
		workingDir: func() (string, error) { return "/repo", nil },
		generate: func(context.Context, string) (generateResult, error) {
			return generateResult{}, errors.New("manifest rejected")
		},
	})
	if code != 1 || stdout.Len() != 0 || stderr.String() != "generate nodes: manifest rejected\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestGenerateNodesFindsProjectRootFromNestedDirectory(t *testing.T) {
	root := t.TempDir()
	writeGenerateFixture(t, root, "apiVersion: agent-studio.dev/v1alpha1\nnodes: []\n")
	nested := filepath.Join(root, "some", "nested", "directory")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := generateNodes(context.Background(), nested)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.Path != "apps/api/internal/generated/nodes_gen.go" {
		t.Fatalf("result=%+v", result)
	}
	generated := filepath.Join(root, filepath.FromSlash(result.Path))
	data, err := os.ReadFile(generated)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "func RegisterNodes(registry packageRegistry) error") {
		t.Fatalf("generated=%s", data)
	}
}

func TestGenerateNodesPreservesOldOutputForInvalidManifest(t *testing.T) {
	root := t.TempDir()
	writeGenerateFixture(t, root, "apiVersion: unsupported\nnodes: []\n")
	output := filepath.Join(root, "apps", "api", "internal", "generated", "nodes_gen.go")
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("old-content"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := generateNodes(context.Background(), root)
	if err == nil || !strings.Contains(err.Error(), "unsupported apiVersion") {
		t.Fatalf("error=%v", err)
	}
	data, readErr := os.ReadFile(output)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "old-content" {
		t.Fatalf("output=%q", data)
	}
}

func writeGenerateFixture(t *testing.T, root, manifest string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/project\n\ngo 1.26.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "agent-studio.nodes.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}
