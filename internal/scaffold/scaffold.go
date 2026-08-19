package scaffold

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"go/format"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/yyl1212/agent-studio/internal/nodemanifest"
	"github.com/yyl1212/agent-studio/internal/nodepackage"
	"github.com/yyl1212/agent-studio/internal/safepath"
)

//go:embed templates/*
var templateFiles embed.FS

var extensionNamePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)

type Request struct {
	RootDir         string
	ModulePath      string
	Name            string
	Manifest        nodemanifest.Manifest
	PackageManifest nodepackage.Manifest
}

type ScaffoldPlan struct {
	Directory           string
	Files               map[string][]byte
	Manifest            nodemanifest.Manifest
	ManifestPath        string
	PackageManifest     nodepackage.Manifest
	PackageManifestPath string
}

type ApplyDeps struct {
	Rename func(string, string) error
}

type templateData struct {
	PackageName string
	NodeType    string
	Title       string
	Name        string
}

func Plan(request Request) (ScaffoldPlan, error) {
	if err := ValidateName(request.Name); err != nil {
		return ScaffoldPlan{}, err
	}
	packageName := strings.ReplaceAll(request.Name, "-", "")
	importPath := strings.TrimSuffix(request.ModulePath, "/") + "/extensions/" + request.Name
	updatedManifest, err := nodemanifest.AddPackage(request.Manifest, importPath)
	if err != nil {
		return ScaffoldPlan{}, fmt.Errorf("add node package to manifest: %w", err)
	}
	updatedPackageManifest, err := nodepackage.AddRegistration(request.PackageManifest, nodepackage.Registration{
		Package: importPath,
		Nodes: []nodepackage.NodeRef{{
			Type: "extension." + request.Name, Version: "1.0.0",
		}},
	})
	if err != nil {
		return ScaffoldPlan{}, fmt.Errorf("add node to package manifest: %w", err)
	}
	data := templateData{
		PackageName: packageName,
		NodeType:    "extension." + request.Name,
		Title:       titleName(request.Name),
		Name:        request.Name,
	}
	files := make(map[string][]byte, 3)
	for _, specification := range []struct {
		template string
		output   string
		goSource bool
	}{
		{template: "node.go.tmpl", output: "node.go", goSource: true},
		{template: "node_test.go.tmpl", output: "node_test.go", goSource: true},
		{template: "README.md.tmpl", output: "README.md"},
	} {
		rendered, renderErr := renderTemplate(specification.template, data)
		if renderErr != nil {
			return ScaffoldPlan{}, renderErr
		}
		if specification.goSource {
			rendered, renderErr = format.Source(rendered)
			if renderErr != nil {
				return ScaffoldPlan{}, fmt.Errorf("format scaffold %s: %w", specification.output, renderErr)
			}
		}
		files[specification.output] = rendered
	}
	root := filepath.Clean(request.RootDir)
	return ScaffoldPlan{
		Directory:           filepath.Join(root, "extensions", request.Name),
		Files:               files,
		Manifest:            updatedManifest,
		ManifestPath:        filepath.Join(root, "agent-studio.nodes.yaml"),
		PackageManifest:     updatedPackageManifest,
		PackageManifestPath: filepath.Join(root, nodepackage.Filename),
	}, nil
}

func ValidateName(name string) error {
	if !extensionNamePattern.MatchString(name) {
		return fmt.Errorf("invalid node name %q: use lowercase kebab-case", name)
	}
	packageName := strings.ReplaceAll(name, "-", "")
	if token.Lookup(packageName).IsKeyword() {
		return fmt.Errorf("invalid node name %q: generated package %q is a Go keyword", name, packageName)
	}
	return nil
}

func Apply(plan ScaffoldPlan, deps ApplyDeps) (returnErr error) {
	root := filepath.Dir(plan.ManifestPath)
	if err := safepath.ValidateWriteTarget(root, plan.Directory); err != nil {
		return fmt.Errorf("validate node directory: %w", err)
	}
	if err := safepath.ValidateWriteTarget(root, plan.ManifestPath); err != nil {
		return fmt.Errorf("validate node manifest: %w", err)
	}
	if err := safepath.ValidateWriteTarget(root, plan.PackageManifestPath); err != nil {
		return fmt.Errorf("validate node package manifest: %w", err)
	}
	manifestData, err := nodemanifest.Marshal(plan.Manifest)
	if err != nil {
		return err
	}
	packageManifestData, err := nodepackage.Encode(plan.PackageManifest)
	if err != nil {
		return err
	}
	originalManifest, err := os.ReadFile(plan.ManifestPath)
	if err != nil {
		return fmt.Errorf("read node manifest %s: %w", plan.ManifestPath, err)
	}
	originalPackageManifest, err := os.ReadFile(plan.PackageManifestPath)
	if err != nil {
		return fmt.Errorf("read node package manifest %s: %w", plan.PackageManifestPath, err)
	}
	createdDirectory := false
	entries, err := os.ReadDir(plan.Directory)
	switch {
	case errorsIsNotExist(err):
		if err := os.MkdirAll(filepath.Dir(plan.Directory), 0o755); err != nil {
			return fmt.Errorf("create extensions directory: %w", err)
		}
		if err := os.Mkdir(plan.Directory, 0o755); err != nil {
			return fmt.Errorf("create node directory %s: %w", plan.Directory, err)
		}
		createdDirectory = true
	case err != nil:
		return fmt.Errorf("inspect node directory %s: %w", plan.Directory, err)
	case len(entries) > 0:
		return fmt.Errorf("node directory %s is not empty", plan.Directory)
	}

	createdFiles := make([]string, 0, len(plan.Files))
	defer func() {
		if returnErr == nil {
			return
		}
		for _, path := range createdFiles {
			_ = os.Remove(path)
		}
		if createdDirectory {
			_ = os.Remove(plan.Directory)
		}
	}()

	names := make([]string, 0, len(plan.Files))
	for name := range plan.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if filepath.Base(name) != name {
			return fmt.Errorf("invalid scaffold file name %q", name)
		}
		path := filepath.Join(plan.Directory, name)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return fmt.Errorf("create scaffold file %s: %w", path, err)
		}
		createdFiles = append(createdFiles, path)
		if _, err := file.Write(plan.Files[name]); err != nil {
			_ = file.Close()
			return fmt.Errorf("write scaffold file %s: %w", path, err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("close scaffold file %s: %w", path, err)
		}
	}
	rename := deps.Rename
	if rename == nil {
		rename = os.Rename
	}
	if err := writeManifestAtomic(plan.ManifestPath, manifestData, rename); err != nil {
		return err
	}
	if err := writeManifestAtomic(plan.PackageManifestPath, packageManifestData, rename); err != nil {
		rollbackErr := restoreManifests(plan, originalManifest, originalPackageManifest, rename)
		if rollbackErr != nil {
			return errors.Join(err, fmt.Errorf("restore manifests: %w", rollbackErr))
		}
		return err
	}
	return nil
}

func restoreManifests(plan ScaffoldPlan, manifestData, packageManifestData []byte, rename func(string, string) error) error {
	var restoreErrors []error
	if err := writeManifestAtomic(plan.ManifestPath, manifestData, rename); err != nil {
		restoreErrors = append(restoreErrors, err)
	}
	if err := writeManifestAtomic(plan.PackageManifestPath, packageManifestData, rename); err != nil {
		restoreErrors = append(restoreErrors, err)
	}
	return errors.Join(restoreErrors...)
}

func renderTemplate(name string, data templateData) ([]byte, error) {
	templatePath := "templates/" + name
	tmpl, err := template.New(name).ParseFS(templateFiles, templatePath)
	if err != nil {
		return nil, fmt.Errorf("parse scaffold template %s: %w", name, err)
	}
	var output bytes.Buffer
	if err := tmpl.ExecuteTemplate(&output, name, data); err != nil {
		return nil, fmt.Errorf("render scaffold template %s: %w", name, err)
	}
	return output.Bytes(), nil
}

func titleName(name string) string {
	parts := strings.Split(name, "-")
	for index, part := range parts {
		parts[index] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func writeManifestAtomic(path string, data []byte, rename func(string, string) error) (returnErr error) {
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("create manifest temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		if returnErr != nil {
			_ = temporary.Close()
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o644); err != nil {
		return fmt.Errorf("chmod manifest temporary file: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("write manifest temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync manifest temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close manifest temporary file: %w", err)
	}
	if err := rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace node manifest %s: %w", path, err)
	}
	return nil
}

func errorsIsNotExist(err error) bool {
	return err != nil && (os.IsNotExist(err) || err == fs.ErrNotExist)
}
