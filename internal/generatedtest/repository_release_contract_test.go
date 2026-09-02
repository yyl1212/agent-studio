package generatedtest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yyl1212/agent-studio/internal/nodepackage"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

func TestRepositoryManifestSupportsV050Runtime(t *testing.T) {
	root := repositoryRoot(t)
	source := filepath.Join(root, nodepackage.Filename)
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := nodepackage.Parse(source, data)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := manifest.Compatibility.Runtime.MinVersion, "v0.2.0"; got != want {
		t.Fatalf("min runtime=%s want=%s", got, want)
	}
	if got, want := manifest.Compatibility.Runtime.MaxVersionExclusive, "v0.6.0"; got != want {
		t.Fatalf("max runtime=%s want=%s", got, want)
	}
	diagnostics := nodepackage.CheckCompatibility(manifest, "0.5.0-dev", "0.5.0", agentnode.APIVersion, "v0.5.0")
	if nodepackage.HasErrors(diagnostics) {
		t.Fatalf("diagnostics=%+v", diagnostics)
	}
}
