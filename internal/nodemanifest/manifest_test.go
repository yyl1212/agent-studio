package nodemanifest

import (
	"errors"
	"io/fs"
	"reflect"
	"strings"
	"testing"

	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

func TestParseStrictManifest(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		wantErr string
	}{
		{name: "unknown top field", source: "apiVersion: agent-studio.dev/v1alpha1\nnodes: []\nextra: true\n", wantErr: "field extra"},
		{name: "unknown node field", source: "apiVersion: agent-studio.dev/v1alpha1\nnodes:\n  - package: example.com/node\n    command: run\n", wantErr: "field command"},
		{name: "wrong version", source: "apiVersion: v2\nnodes: []\n", wantErr: "unsupported apiVersion"},
		{name: "duplicate", source: "apiVersion: agent-studio.dev/v1alpha1\nnodes:\n  - package: example.com/node\n  - package: example.com/node\n", wantErr: "duplicate package"},
		{name: "multi document", source: "apiVersion: agent-studio.dev/v1alpha1\nnodes: []\n---\nnodes: []\n", wantErr: "multiple YAML documents"},
		{name: "empty package", source: "apiVersion: agent-studio.dev/v1alpha1\nnodes:\n  - package: \"\"\n", wantErr: "package is required"},
		{name: "invalid import path", source: "apiVersion: agent-studio.dev/v1alpha1\nnodes:\n  - package: ../node\n", wantErr: "invalid package"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse("manifest.yaml", []byte(test.source))
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error=%v, want substring %q", err, test.wantErr)
			}
			if !strings.Contains(err.Error(), "manifest.yaml") {
				t.Fatalf("error does not include source: %v", err)
			}
		})
	}
}

func TestLoadValidManifest(t *testing.T) {
	manifest, err := Load("testdata/valid.yaml")
	if err != nil {
		t.Fatal(err)
	}
	want := Manifest{
		APIVersion: agentnode.APIVersion,
		Nodes:      []NodePackage{{Package: "example.com/project/extensions/echo"}},
	}
	if !reflect.DeepEqual(manifest, want) {
		t.Fatalf("manifest=%#v, want %#v", manifest, want)
	}
}

func TestAddPackageRoundTripsWithoutMutatingInput(t *testing.T) {
	original := Manifest{APIVersion: agentnode.APIVersion, Nodes: []NodePackage{}}
	updated, err := AddPackage(original, "example.com/project/extensions/echo")
	if err != nil {
		t.Fatal(err)
	}
	if len(original.Nodes) != 0 {
		t.Fatalf("original mutated: %#v", original)
	}
	encoded, err := Marshal(updated)
	if err != nil {
		t.Fatal(err)
	}
	wantYAML := "apiVersion: agent-studio.dev/v1alpha1\nnodes:\n  - package: example.com/project/extensions/echo\n"
	if string(encoded) != wantYAML {
		t.Fatalf("encoded=%q, want %q", encoded, wantYAML)
	}
	parsed, err := Parse("roundtrip.yaml", encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(parsed, updated) {
		t.Fatalf("parsed=%#v, want %#v", parsed, updated)
	}
	if _, err := AddPackage(updated, "example.com/project/extensions/echo"); err == nil || !strings.Contains(err.Error(), "duplicate package") {
		t.Fatalf("duplicate error=%v", err)
	}
}

func TestMarshalUsesJSONArrayShapeForEmptyNodes(t *testing.T) {
	encoded, err := Marshal(Manifest{APIVersion: agentnode.APIVersion})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(encoded), "apiVersion: agent-studio.dev/v1alpha1\nnodes: []\n"; got != want {
		t.Fatalf("encoded=%q, want %q", got, want)
	}
}

func TestLoadIncludesFileNameInReadError(t *testing.T) {
	path := t.TempDir() + "/missing.yaml"
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), path) || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("error=%v", err)
	}
}
