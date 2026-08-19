package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yyl1212/agent-studio/internal/nodepackage"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
	"golang.org/x/mod/modfile"
)

type packageInitInput struct {
	DisplayName         string
	Description         string
	License             string
	Repository          string
	RuntimeMin          string
	RuntimeMaxExclusive string
}

func missingPackageInitFlags(input packageInitInput) []string {
	missing := make([]string, 0, 5)
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "--display-name", value: input.DisplayName},
		{name: "--license", value: input.License},
		{name: "--repository", value: input.Repository},
		{name: "--runtime-min", value: input.RuntimeMin},
		{name: "--runtime-max-exclusive", value: input.RuntimeMaxExclusive},
	} {
		if field.value == "" {
			missing = append(missing, field.name)
		}
	}
	return missing
}

func initializeNodePackage(ctx context.Context, start string, input packageInitInput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := findGoModuleRoot(start)
	if err != nil {
		return err
	}
	modulePathFile := filepath.Join(root, "go.mod")
	moduleData, err := os.ReadFile(modulePathFile)
	if err != nil {
		return fmt.Errorf("read Go module %s: %w", modulePathFile, err)
	}
	parsedModule, err := modfile.Parse(modulePathFile, moduleData, nil)
	if err != nil {
		return fmt.Errorf("parse Go module %s: %w", modulePathFile, err)
	}
	if parsedModule.Module == nil || parsedModule.Module.Mod.Path == "" {
		return fmt.Errorf("parse Go module %s: module path is required", modulePathFile)
	}
	manifest := nodepackage.Manifest{
		APIVersion: nodepackage.APIVersion,
		Kind:       nodepackage.Kind,
		Metadata: nodepackage.Metadata{
			Name:        parsedModule.Module.Mod.Path,
			DisplayName: input.DisplayName,
			Description: input.Description,
			License:     input.License,
			Repository:  input.Repository,
		},
		Compatibility: nodepackage.Compatibility{
			NodeAPI: agentnode.APIVersion,
			Runtime: nodepackage.RuntimeRange{
				MinVersion:          input.RuntimeMin,
				MaxVersionExclusive: input.RuntimeMaxExclusive,
			},
		},
		Registrations: []nodepackage.Registration{},
	}
	encoded, err := nodepackage.Encode(manifest)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return writeNewFileAtomic(filepath.Join(root, nodepackage.Filename), encoded, os.Link)
}

func writeNewFileAtomic(path string, data []byte, publish func(string, string) error) (returnErr error) {
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-")
	if err != nil {
		return fmt.Errorf("create node package manifest temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		if removeErr := os.Remove(temporaryPath); returnErr == nil && removeErr != nil && !os.IsNotExist(removeErr) {
			returnErr = fmt.Errorf("remove node package manifest temporary file: %w", removeErr)
		}
	}()
	if err := temporary.Chmod(0o644); err != nil {
		return fmt.Errorf("chmod node package manifest temporary file: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("write node package manifest temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync node package manifest temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close node package manifest temporary file: %w", err)
	}
	if err := publish(temporaryPath, path); err != nil {
		return fmt.Errorf("publish node package manifest %s: %w", path, err)
	}
	return nil
}
