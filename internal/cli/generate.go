package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/yyl1212/agent-studio/internal/nodegen"
	"github.com/yyl1212/agent-studio/internal/nodemanifest"
)

const generatedNodesPath = "apps/api/internal/generated/nodes_gen.go"

type generateResult struct {
	Path    string
	Changed bool
}

func generateNodes(ctx context.Context, start string) (generateResult, error) {
	root, err := findProjectRoot(start)
	if err != nil {
		return generateResult{}, err
	}
	manifest, err := nodemanifest.Load(filepath.Join(root, "agent-studio.nodes.yaml"))
	if err != nil {
		return generateResult{}, err
	}
	changed, err := (nodegen.Generator{}).Generate(ctx, root, manifest, filepath.FromSlash(generatedNodesPath))
	if err != nil {
		return generateResult{}, err
	}
	return generateResult{Path: generatedNodesPath, Changed: changed}, nil
}

func findProjectRoot(start string) (string, error) {
	current, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("resolve working directory %s: %w", start, err)
	}
	for {
		goModule, moduleErr := regularFile(filepath.Join(current, "go.mod"))
		manifest, manifestErr := regularFile(filepath.Join(current, "agent-studio.nodes.yaml"))
		if moduleErr != nil {
			return "", moduleErr
		}
		if manifestErr != nil {
			return "", manifestErr
		}
		if goModule && manifest {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("find project root from %s: go.mod and agent-studio.nodes.yaml were not found", start)
		}
		current = parent
	}
}

func findGoModuleRoot(start string) (string, error) {
	current, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("resolve working directory %s: %w", start, err)
	}
	for {
		goModule, moduleErr := regularFile(filepath.Join(current, "go.mod"))
		if moduleErr != nil {
			return "", moduleErr
		}
		if goModule {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("find Go module root from %s: go.mod was not found", start)
		}
		current = parent
	}
}

func regularFile(path string) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect project file %s: %w", path, err)
	}
	return info.Mode().IsRegular(), nil
}
