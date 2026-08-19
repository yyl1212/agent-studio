package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yyl1212/agent-studio/internal/nodemanifest"
	"github.com/yyl1212/agent-studio/internal/nodepackage"
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
	packageManifestPath := filepath.Join(root, nodepackage.Filename)
	packageManifestData, err := os.ReadFile(packageManifestPath)
	if err != nil {
		return nodeInitResult{}, fmt.Errorf("read node package manifest %s: %w", packageManifestPath, err)
	}
	packageManifest, err := nodepackage.Parse(nodepackage.Filename, packageManifestData)
	if err != nil {
		return nodeInitResult{}, err
	}
	if packageManifest.Metadata.Name != parsedModule.Module.Mod.Path {
		return nodeInitResult{}, fmt.Errorf(
			"node package manifest module %q does not match go.mod module %q",
			packageManifest.Metadata.Name,
			parsedModule.Module.Mod.Path,
		)
	}
	plan, err := scaffold.Plan(scaffold.Request{
		RootDir:         root,
		ModulePath:      parsedModule.Module.Mod.Path,
		Name:            name,
		Manifest:        manifest,
		PackageManifest: packageManifest,
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
