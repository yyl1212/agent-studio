package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yyl1212/agent-studio/internal/nodemanifest"
	"github.com/yyl1212/agent-studio/internal/scaffold"
	"golang.org/x/mod/modfile"
)

type nodeInitResult struct {
	Directory string
}

func initializeNode(ctx context.Context, start, name string) (nodeInitResult, error) {
	if err := ctx.Err(); err != nil {
		return nodeInitResult{}, err
	}
	root, err := findProjectRoot(start)
	if err != nil {
		return nodeInitResult{}, err
	}
	goModulePath := filepath.Join(root, "go.mod")
	goModuleData, err := os.ReadFile(goModulePath)
	if err != nil {
		return nodeInitResult{}, fmt.Errorf("read Go module %s: %w", goModulePath, err)
	}
	parsedModule, err := modfile.Parse(goModulePath, goModuleData, nil)
	if err != nil {
		return nodeInitResult{}, fmt.Errorf("parse Go module %s: %w", goModulePath, err)
	}
	if parsedModule.Module == nil || parsedModule.Module.Mod.Path == "" {
		return nodeInitResult{}, fmt.Errorf("parse Go module %s: module path is required", goModulePath)
	}
	manifest, err := nodemanifest.Load(filepath.Join(root, "agent-studio.nodes.yaml"))
	if err != nil {
		return nodeInitResult{}, err
	}
	plan, err := scaffold.Plan(scaffold.Request{
		RootDir:    root,
		ModulePath: parsedModule.Module.Mod.Path,
		Name:       name,
		Manifest:   manifest,
	})
	if err != nil {
		return nodeInitResult{}, err
	}
	if err := scaffold.Apply(plan, scaffold.ApplyDeps{}); err != nil {
		return nodeInitResult{}, err
	}
	relative, err := filepath.Rel(root, plan.Directory)
	if err != nil {
		return nodeInitResult{}, fmt.Errorf("make node directory relative: %w", err)
	}
	return nodeInitResult{Directory: filepath.ToSlash(relative)}, nil
}
