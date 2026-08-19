package nodepackage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yyl1212/agent-studio/internal/buildinfo"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

const agentStudioModulePath = "github.com/yyl1212/agent-studio"

type CommandFunc func(
	ctx context.Context,
	dir string,
	environment map[string]string,
	name string,
	args ...string,
) ([]byte, error)

type Inspector struct {
	Command        CommandFunc
	ReadFile       func(string) ([]byte, error)
	RuntimeVersion string
	SDKVersion     string
	NodeAPIVersion string
}

type goListPackage struct {
	ImportPath string        `json:"ImportPath"`
	Dir        string        `json:"Dir"`
	Module     *goListModule `json:"Module"`
}

type goListModule struct {
	Path    string        `json:"Path"`
	Version string        `json:"Version"`
	Dir     string        `json:"Dir"`
	Main    bool          `json:"Main"`
	Replace *goListModule `json:"Replace"`
}

type listedInspection struct {
	importPath  string
	moduleKey   string
	listed      goListPackage
	diagnostics []Diagnostic
}

type moduleInspection struct {
	manifest                 Manifest
	summary                  Summary
	modulePath               string
	baseDiagnostics          []Diagnostic
	compatibilityDiagnostics []Diagnostic
}

func NewInspector(info buildinfo.Info) Inspector {
	return Inspector{
		RuntimeVersion: info.Version,
		SDKVersion:     info.SDKVersion,
		NodeAPIVersion: info.APIVersion,
	}
}

func (inspector Inspector) Inspect(ctx context.Context, root, importPath string) Inspection {
	return inspector.InspectMany(ctx, root, []string{importPath})[0]
}

func (inspector Inspector) InspectMany(ctx context.Context, root string, importPaths []string) []Inspection {
	paths := sortedUniqueStrings(importPaths)
	if len(paths) == 0 {
		return []Inspection{}
	}
	command := inspector.Command
	if command == nil {
		command = runInspectorCommand
	}
	listed := make([]listedInspection, 0, len(paths))
	groups := make(map[string]goListModule)
	groupPaths := make(map[string][]string)
	for _, importPath := range paths {
		entry := listedInspection{importPath: importPath}
		if err := module.CheckImportPath(importPath); err != nil {
			entry.diagnostics = oneInspectionError("NODE_PACKAGE_ID_MISMATCH", "", "注册包 import path 无效")
			listed = append(listed, entry)
			continue
		}
		output, err := command(ctx, root, map[string]string{"CGO_ENABLED": "0", "GOPROXY": "off"},
			"go", "list", "-mod=readonly", "-json", importPath)
		if err != nil {
			entry.diagnostics = oneInspectionError("NODE_PACKAGE_MANIFEST_NOT_FOUND", importPath, "无法离线定位节点包")
			listed = append(listed, entry)
			continue
		}
		if err := json.Unmarshal(output, &entry.listed); err != nil || entry.listed.Module == nil ||
			entry.listed.Module.Path == "" || entry.listed.Module.Dir == "" {
			entry.diagnostics = oneInspectionError("NODE_PACKAGE_MANIFEST_NOT_FOUND", importPath, "无法定位节点包 Module")
			listed = append(listed, entry)
			continue
		}
		if entry.listed.ImportPath != importPath {
			entry.diagnostics = oneInspectionError("NODE_PACKAGE_ID_MISMATCH", importPath, "go list 返回的注册包身份不匹配")
			listed = append(listed, entry)
			continue
		}
		if importPath != entry.listed.Module.Path && !strings.HasPrefix(importPath, entry.listed.Module.Path+"/") {
			entry.diagnostics = oneInspectionError("NODE_PACKAGE_ID_MISMATCH", importPath, "注册包不属于 go list 返回的 Module")
			listed = append(listed, entry)
			continue
		}
		entry.moduleKey = moduleIdentityKey(*entry.listed.Module)
		groups[entry.moduleKey] = *entry.listed.Module
		groupPaths[entry.moduleKey] = append(groupPaths[entry.moduleKey], importPath)
		listed = append(listed, entry)
	}

	readFile := inspector.ReadFile
	if readFile == nil {
		readFile = os.ReadFile
	}
	moduleResults := make(map[string]moduleInspection, len(groups))
	groupKeys := make([]string, 0, len(groups))
	for key := range groups {
		groupKeys = append(groupKeys, key)
	}
	sort.Strings(groupKeys)
	for _, key := range groupKeys {
		moduleResult := inspectModuleManifest(groups[key], readFile)
		if !HasErrors(moduleResult.baseDiagnostics) && hasDeclaredRegistration(moduleResult.manifest, groupPaths[key]) {
			moduleResult.compatibilityDiagnostics = inspector.inspectCompatibility(moduleResult.manifest, groups[key], readFile)
		}
		moduleResults[key] = moduleResult
	}

	results := make([]Inspection, 0, len(listed))
	for _, entry := range listed {
		if len(entry.diagnostics) != 0 {
			results = append(results, Inspection{Diagnostics: SortDiagnostics(entry.diagnostics)})
			continue
		}
		moduleResult := moduleResults[entry.moduleKey]
		diagnostics := append([]Diagnostic(nil), moduleResult.baseDiagnostics...)
		registration, found := findRegistration(moduleResult.manifest, entry.importPath)
		if !found && !HasErrors(diagnostics) {
			diagnostics = append(diagnostics, Diagnostic{
				Severity: SeverityError, Code: "NODE_PACKAGE_REGISTRATION_NOT_DECLARED",
				Package: moduleResult.modulePath, Message: "节点包清单未声明当前注册包",
			})
		} else if found {
			diagnostics = append(diagnostics, moduleResult.compatibilityDiagnostics...)
		}
		record := Record{
			Manifest: moduleResult.manifest, Summary: moduleResult.summary,
			ModulePath: moduleResult.modulePath, Registration: registration,
		}
		results = append(results, Inspection{Record: record, Diagnostics: SortDiagnostics(diagnostics)})
	}
	return results
}

func inspectModuleManifest(module goListModule, readFile func(string) ([]byte, error)) moduleInspection {
	result := moduleInspection{modulePath: module.Path, baseDiagnostics: []Diagnostic{}, compatibilityDiagnostics: []Diagnostic{}}
	manifestData, err := readFile(filepath.Join(module.Dir, Filename))
	if err != nil {
		result.baseDiagnostics = oneInspectionError("NODE_PACKAGE_MANIFEST_NOT_FOUND", module.Path, "节点包 Module 缺少根清单")
		return result
	}
	manifest, err := Parse(Filename, manifestData)
	if err != nil {
		result.baseDiagnostics = oneInspectionError("NODE_PACKAGE_MANIFEST_INVALID", module.Path, "节点包根清单格式无效")
		return result
	}
	result.manifest = manifest
	if manifest.Metadata.Name != module.Path {
		result.baseDiagnostics = oneInspectionError("NODE_PACKAGE_ID_MISMATCH", module.Path, "节点包清单身份与 Module 不匹配")
		return result
	}
	result.summary = packageSummary(manifest, module)
	return result
}

func (inspector Inspector) inspectCompatibility(manifest Manifest, module goListModule, readFile func(string) ([]byte, error)) []Diagnostic {
	diagnostics := checkRuntimeAndAPI(manifest, inspector.RuntimeVersion, inspector.NodeAPIVersion)
	if HasErrors(diagnostics) {
		return appendReplacementDiagnostic(diagnostics, module)
	}
	goModData, err := readFile(filepath.Join(module.Dir, "go.mod"))
	if err != nil {
		return appendReplacementDiagnostic(append(diagnostics,
			oneInspectionError("NODE_PACKAGE_SDK_REQUIREMENT_MISSING", module.Path, "无法验证节点包的 SDK 直接依赖")...), module)
	}
	parsedModule, err := modfile.Parse("go.mod", goModData, nil)
	if err != nil {
		return appendReplacementDiagnostic(append(diagnostics,
			oneInspectionError("NODE_PACKAGE_SDK_REQUIREMENT_MISSING", module.Path, "无法验证节点包的 SDK 直接依赖")...), module)
	}
	requiredSDK := ""
	if module.Main {
		requiredSDK = inspector.SDKVersion
	} else {
		for _, requirement := range parsedModule.Require {
			if requirement.Mod.Path == agentStudioModulePath && !requirement.Indirect {
				requiredSDK = requirement.Mod.Version
				break
			}
		}
	}
	diagnostics = append(diagnostics, checkSDK(manifest.Metadata.Name, inspector.SDKVersion, requiredSDK)...)
	return appendReplacementDiagnostic(diagnostics, module)
}

func appendReplacementDiagnostic(diagnostics []Diagnostic, module goListModule) []Diagnostic {
	if module.Replace != nil {
		diagnostics = append(diagnostics, Diagnostic{
			Severity: SeverityWarning, Code: "NODE_PACKAGE_LOCAL_REPLACE", Package: module.Path,
			Message: "节点包来自本地 replace",
		})
	}
	return SortDiagnostics(diagnostics)
}

func packageSummary(manifest Manifest, module goListModule) Summary {
	source := SourceModule
	version := module.Version
	switch {
	case module.Replace != nil:
		source = SourceReplacement
		version = ""
	case module.Main || module.Version == "" || module.Version == "(devel)":
		source = SourceDevelopment
		version = ""
	}
	return Summary{
		Name: manifest.Metadata.Name, DisplayName: manifest.Metadata.DisplayName,
		Version: version, License: manifest.Metadata.License,
		Repository: manifest.Metadata.Repository, Source: source,
	}
}

func findRegistration(manifest Manifest, importPath string) (Registration, bool) {
	index := sort.Search(len(manifest.Registrations), func(index int) bool {
		return manifest.Registrations[index].Package >= importPath
	})
	if index >= len(manifest.Registrations) || manifest.Registrations[index].Package != importPath {
		return Registration{}, false
	}
	return manifest.Registrations[index], true
}

func hasDeclaredRegistration(manifest Manifest, importPaths []string) bool {
	for _, importPath := range importPaths {
		if _, found := findRegistration(manifest, importPath); found {
			return true
		}
	}
	return false
}

func oneInspectionError(code, packageName, message string) []Diagnostic {
	return []Diagnostic{{Severity: SeverityError, Code: code, Package: packageName, Message: message}}
}

func moduleIdentityKey(module goListModule) string {
	parts := []string{module.Path, module.Version, module.Dir}
	if module.Main {
		parts = append(parts, "main")
	} else {
		parts = append(parts, "dependency")
	}
	if module.Replace != nil {
		parts = append(parts, module.Replace.Path, module.Replace.Version, module.Replace.Dir)
	}
	return strings.Join(parts, "\x00")
}

func sortedUniqueStrings(input []string) []string {
	output := append([]string(nil), input...)
	sort.Strings(output)
	unique := output[:0]
	for _, value := range output {
		if len(unique) == 0 || unique[len(unique)-1] != value {
			unique = append(unique, value)
		}
	}
	if unique == nil {
		return []string{}
	}
	return unique
}

func runInspectorCommand(ctx context.Context, dir string, environment map[string]string, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = dir
	command.Env = overrideEnvironment(os.Environ(), environment)
	return command.CombinedOutput()
}

func overrideEnvironment(environment []string, replacements map[string]string) []string {
	updated := make([]string, 0, len(environment)+len(replacements))
	for _, entry := range environment {
		key, _, found := strings.Cut(entry, "=")
		if found {
			if _, replace := replacements[key]; replace {
				continue
			}
		}
		updated = append(updated, entry)
	}
	keys := make([]string, 0, len(replacements))
	for key := range replacements {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		updated = append(updated, key+"="+replacements[key])
	}
	return updated
}

func (inspection Inspection) Error() error {
	messages := make([]string, 0, len(inspection.Diagnostics))
	for _, diagnostic := range SortDiagnostics(inspection.Diagnostics) {
		if diagnostic.Severity == SeverityError {
			messages = append(messages, diagnostic.Code+": "+diagnostic.Message)
		}
	}
	if len(messages) == 0 {
		return nil
	}
	return errors.New(strings.Join(messages, "; "))
}
