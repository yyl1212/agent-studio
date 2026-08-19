package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yyl1212/agent-studio/internal/nodepackage"
)

func TestInitializeNodePackageCreatesExplicitManifest(t *testing.T) {
	root := nodePackageFixtureProject(t)
	err := initializeNodePackage(context.Background(), root, packageInitInput{
		DisplayName:         "Example Nodes",
		Description:         "示例",
		License:             "Apache-2.0",
		Repository:          "https://example.com/nodes",
		RuntimeMin:          "v0.2.0",
		RuntimeMaxExclusive: "v0.4.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, nodepackage.Filename))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := nodepackage.Parse(nodepackage.Filename, data)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Metadata.Name != "example.com/project" || manifest.Metadata.Description != "示例" || len(manifest.Registrations) != 0 {
		t.Fatalf("manifest=%+v", manifest)
	}
	assertNoNodePackageTemporaryFiles(t, root)
}

func TestInitializeNodePackageRejectsInvalidInputWithoutWriting(t *testing.T) {
	for _, test := range []struct {
		name       string
		repository string
		minimum    string
		maximum    string
	}{
		{name: "repository", repository: "http://example.com/nodes", minimum: "v0.2.0", maximum: "v0.4.0"},
		{name: "minimum version", repository: "https://example.com/nodes", minimum: "0.2.0", maximum: "v0.4.0"},
		{name: "version range", repository: "https://example.com/nodes", minimum: "v0.4.0", maximum: "v0.2.0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := nodePackageFixtureProject(t)
			err := initializeNodePackage(context.Background(), root, packageInitInput{
				DisplayName: "Example Nodes", License: "Apache-2.0", Repository: test.repository,
				RuntimeMin: test.minimum, RuntimeMaxExclusive: test.maximum,
			})
			if err == nil {
				t.Fatal("expected validation error")
			}
			if _, statErr := os.Stat(filepath.Join(root, nodepackage.Filename)); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("manifest exists after validation failure: %v", statErr)
			}
		})
	}
}

func TestInitializeNodePackageRefusesToOverwrite(t *testing.T) {
	root := nodePackageFixtureProject(t)
	target := filepath.Join(root, nodepackage.Filename)
	original := []byte("keep me")
	if err := os.WriteFile(target, original, 0o644); err != nil {
		t.Fatal(err)
	}
	err := initializeNodePackage(context.Background(), root, packageInitInput{
		DisplayName: "Example Nodes", License: "Apache-2.0", Repository: "https://example.com/nodes",
		RuntimeMin: "v0.2.0", RuntimeMaxExclusive: "v0.4.0",
	})
	if err == nil {
		t.Fatal("expected existing manifest error")
	}
	data, readErr := os.ReadFile(target)
	if readErr != nil || string(data) != string(original) {
		t.Fatalf("target=%q readErr=%v", data, readErr)
	}
	assertNoNodePackageTemporaryFiles(t, root)
}

func TestWriteNewFileAtomicCleansUpWhenPublishFails(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, nodepackage.Filename)
	wantErr := errors.New("publish denied")
	err := writeNewFileAtomic(target, []byte("new"), func(string, string) error { return wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("error=%v", err)
	}
	if _, statErr := os.Stat(target); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("target exists: %v", statErr)
	}
	entries, readErr := os.ReadDir(root)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("temporary files remain: %v err=%v", entries, readErr)
	}
}

func TestFindGoModuleRootNeedsOnlyGoMod(t *testing.T) {
	root := nodePackageFixtureProject(t)
	nested := filepath.Join(root, "nested", "directory")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	found, err := findGoModuleRoot(nested)
	if err != nil || found != root {
		t.Fatalf("root=%q err=%v", found, err)
	}
}

func nodePackageFixtureProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/project\n\ngo 1.26.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func assertNoNodePackageTemporaryFiles(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".agent-studio.node-package.json-") {
			t.Fatalf("temporary file remains: %s", entry.Name())
		}
	}
}
