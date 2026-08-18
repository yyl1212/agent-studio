package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
)

type processCall struct {
	Name        string
	Args        []string
	Dir         string
	Environment map[string]string
	Stdout      io.Writer
	Stderr      io.Writer
}

type processRunner func(context.Context, processCall) error

func testNodePackage(ctx context.Context, start, packageArg string, stdout, stderr io.Writer) int {
	root, err := findProjectRoot(start)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "node test: %v\n", err)
		return 1
	}
	return runNodeTest(ctx, root, packageArg, stdout, stderr, runProcess)
}

func runNodeTest(ctx context.Context, root, packageArg string, stdout, stderr io.Writer, runner processRunner) int {
	if !isNodeExtensionPackage(root, packageArg) {
		_, _ = io.WriteString(stderr, "node test: package must be an immediate child of extensions/\n")
		return 2
	}

	list := processCall{
		Name:        "go",
		Args:        []string{"list", "-mod=readonly", packageArg},
		Dir:         root,
		Environment: map[string]string{"CGO_ENABLED": "0", "GOPROXY": "off"},
		Stdout:      stdout,
		Stderr:      stderr,
	}
	if err := runner(ctx, list); err != nil {
		return 1
	}

	test := processCall{
		Name:        "go",
		Args:        []string{"test", packageArg, "-count=1"},
		Dir:         root,
		Environment: map[string]string{"CGO_ENABLED": "0", "GOFLAGS": "-mod=mod"},
		Stdout:      stdout,
		Stderr:      stderr,
	}
	if err := runner(ctx, test); err != nil {
		return 1
	}
	return 0
}

func isNodeExtensionPackage(root, packageArg string) bool {
	modulePath, err := readModulePath(filepath.Join(root, "go.mod"))
	if err != nil {
		return false
	}

	const localPrefix = "./extensions/"
	name := ""
	switch {
	case strings.HasPrefix(packageArg, localPrefix):
		name = strings.TrimPrefix(packageArg, localPrefix)
	case strings.HasPrefix(packageArg, modulePath+"/extensions/"):
		name = strings.TrimPrefix(packageArg, modulePath+"/extensions/")
	default:
		return false
	}
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\\`) {
		return false
	}

	target, err := filepath.Abs(filepath.Join(root, "extensions", name))
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return relative == filepath.Join("extensions", name)
}

func readModulePath(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	module, err := modfile.Parse(path, content, nil)
	if err != nil {
		return "", err
	}
	if module.Module == nil || module.Module.Mod.Path == "" {
		return "", fmt.Errorf("module path is missing")
	}
	return module.Module.Mod.Path, nil
}

func runProcess(ctx context.Context, call processCall) error {
	command := exec.CommandContext(ctx, call.Name, call.Args...)
	command.Dir = call.Dir
	command.Env = processEnvironment(call.Environment)
	command.Stdout = call.Stdout
	command.Stderr = call.Stderr
	return command.Run()
}

func processEnvironment(overrides map[string]string) []string {
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
