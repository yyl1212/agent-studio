package generatedtest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRepositoryNodeGoldenPath(t *testing.T) {
	repository := repositoryRoot(t)
	before := gitStatus(t, repository)
	fixture := t.TempDir()
	cliBinary := filepath.Join(t.TempDir(), "agent-studio")

	run(t, repository, map[string]string{"CGO_ENABLED": "0"}, "go", "build", "-o", cliBinary, "./cmd/agent-studio")
	writeFile(t, filepath.Join(fixture, "go.mod"), fmt.Sprintf(`module example.com/sdkfixture

go 1.26.0

require github.com/yyl1212/agent-studio v0.0.0

replace github.com/yyl1212/agent-studio => %s
`, filepath.ToSlash(repository)))
	writeFile(t, filepath.Join(fixture, "agent-studio.nodes.yaml"), "apiVersion: agent-studio.dev/v1alpha1\nnodes: []\n")

	moduleBefore, err := os.ReadFile(filepath.Join(fixture, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	run(t, fixture, nil, cliBinary,
		"node", "package", "init",
		"--display-name", "SDK Fixture",
		"--license", "Apache-2.0",
		"--repository", "https://example.com/sdkfixture",
		"--runtime-min", "v0.2.0",
		"--runtime-max-exclusive", "v0.5.0",
	)
	run(t, fixture, nil, cliBinary, "node", "init", "echo")
	run(t, fixture, nil, cliBinary, "node", "test", "./extensions/echo")
	moduleAfter, err := os.ReadFile(filepath.Join(fixture, "go.mod"))
	if err != nil || string(moduleAfter) != string(moduleBefore) {
		t.Fatalf("node test changed go.mod: %v\n%s", err, moduleAfter)
	}
	if _, err := os.Stat(filepath.Join(fixture, "go.sum")); !os.IsNotExist(err) {
		t.Fatalf("node test created go.sum: %v", err)
	}
	run(t, fixture, nil, cliBinary, "generate")
	run(t, fixture, map[string]string{"CGO_ENABLED": "0"}, "go", "test", "-mod=mod", "./...", "-count=1")

	manifest, err := os.ReadFile(filepath.Join(fixture, "agent-studio.nodes.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifest), "example.com/sdkfixture/extensions/echo") {
		t.Fatalf("manifest=%s", manifest)
	}
	packageManifest, err := os.ReadFile(filepath.Join(fixture, "agent-studio.node-package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(packageManifest), "example.com/sdkfixture/extensions/echo") ||
		!strings.Contains(string(packageManifest), `"type": "extension.echo"`) ||
		!strings.Contains(string(packageManifest), `"version": "1.0.0"`) {
		t.Fatalf("package manifest=%s", packageManifest)
	}
	generated, err := os.ReadFile(filepath.Join(fixture, "apps/api/internal/generated/nodes_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(generated), `"example.com/sdkfixture/extensions/echo"`) {
		t.Fatalf("generated=%s", generated)
	}
	if strings.Contains(string(generated), filepath.ToSlash(fixture)) {
		t.Fatalf("generated output leaks fixture path %q:\n%s", fixture, generated)
	}
	if after := gitStatus(t, repository); after != before {
		t.Fatalf("repository status changed\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, directory string, overrides map[string]string, name string, args ...string) {
	t.Helper()
	command := exec.Command(name, args...)
	command.Dir = directory
	command.Env = mergeEnvironment(overrides)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run %s %s: %v\n%s", name, strings.Join(args, " "), err, output)
	}
	t.Logf("%s %s\n%s", name, strings.Join(args, " "), output)
}

func gitStatus(t *testing.T, directory string) string {
	t.Helper()
	command := exec.Command("git", "status", "--porcelain")
	command.Dir = directory
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(output)
}

func mergeEnvironment(overrides map[string]string) []string {
	environment := os.Environ()
	for key, value := range overrides {
		prefix := key + "="
		filtered := environment[:0]
		for _, entry := range environment {
			if !strings.HasPrefix(entry, prefix) {
				filtered = append(filtered, entry)
			}
		}
		environment = append(filtered, prefix+value)
	}
	return environment
}
