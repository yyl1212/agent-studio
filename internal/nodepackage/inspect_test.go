package nodepackage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const inspectorGoMod = `module example.com/nodes

go 1.26.0

require github.com/yyl1212/agent-studio v0.3.0
`

func TestInspectUsesOfflineGoListAndBuildsSafeRecord(t *testing.T) {
	var gotDirectory string
	var gotEnvironment map[string]string
	var gotName string
	var gotArgs []string
	inspection := Inspector{
		Command: func(_ context.Context, dir string, environment map[string]string, name string, args ...string) ([]byte, error) {
			gotDirectory, gotEnvironment, gotName, gotArgs = dir, environment, name, append([]string(nil), args...)
			return []byte(goListFixture("example.com/nodes/extensions/echo", false, false)), nil
		},
		ReadFile: fakeInspectorReadFile(map[string][]byte{
			"/module/agent-studio.node-package.json": []byte(validManifestJSON),
			"/module/go.mod":                         []byte(inspectorGoMod),
		}),
		RuntimeVersion: "v0.3.1",
		SDKVersion:     "0.3.0",
		NodeAPIVersion: APIVersion,
	}.Inspect(context.Background(), "/project", "example.com/nodes/extensions/echo")

	if err := inspection.Error(); err != nil {
		t.Fatal(err)
	}
	if gotDirectory != "/project" || gotName != "go" || !reflect.DeepEqual(gotArgs, []string{"list", "-mod=readonly", "-json", "example.com/nodes/extensions/echo"}) {
		t.Fatalf("dir=%q name=%q args=%v", gotDirectory, gotName, gotArgs)
	}
	if gotEnvironment["CGO_ENABLED"] != "0" || gotEnvironment["GOPROXY"] != "off" {
		t.Fatalf("environment=%v", gotEnvironment)
	}
	if inspection.Record.ModulePath != "example.com/nodes" || inspection.Record.Summary.Version != "v0.3.0" ||
		inspection.Record.Registration.Package != "example.com/nodes/extensions/echo" {
		t.Fatalf("record=%+v", inspection.Record)
	}
}

func TestInspectManyParsesEachModuleOnceAndSortsInput(t *testing.T) {
	readCounts := map[string]int{}
	commandCalls := []string{}
	inspector := Inspector{
		Command: func(_ context.Context, _ string, _ map[string]string, _ string, args ...string) ([]byte, error) {
			importPath := args[len(args)-1]
			commandCalls = append(commandCalls, importPath)
			return []byte(goListFixture(importPath, false, false)), nil
		},
		ReadFile: func(path string) ([]byte, error) {
			readCounts[path]++
			switch path {
			case "/module/agent-studio.node-package.json":
				manifest := strings.Replace(validManifestJSON,
					`"nodes":[{"type":"example.echo","version":"1.0.0"}]`,
					`"nodes":[{"type":"example.echo","version":"1.0.0"}]},{"package":"example.com/nodes/extensions/search","nodes":[{"type":"example.search","version":"1.0.0"}]`, 1)
				return []byte(manifest), nil
			case "/module/go.mod":
				return []byte(inspectorGoMod), nil
			default:
				return nil, os.ErrNotExist
			}
		},
		RuntimeVersion: "v0.3.1", SDKVersion: "0.3.0", NodeAPIVersion: APIVersion,
	}
	inspections := inspector.InspectMany(context.Background(), "/project", []string{
		"example.com/nodes/extensions/search",
		"example.com/nodes/extensions/echo",
		"example.com/nodes/extensions/search",
	})
	if len(inspections) != 2 || inspections[0].Record.Registration.Package != "example.com/nodes/extensions/echo" ||
		inspections[1].Record.Registration.Package != "example.com/nodes/extensions/search" {
		t.Fatalf("inspections=%+v", inspections)
	}
	if !reflect.DeepEqual(commandCalls, []string{"example.com/nodes/extensions/echo", "example.com/nodes/extensions/search"}) {
		t.Fatalf("command calls=%v", commandCalls)
	}
	if readCounts["/module/agent-studio.node-package.json"] != 1 || readCounts["/module/go.mod"] != 1 {
		t.Fatalf("read counts=%v", readCounts)
	}
	if empty := inspector.InspectMany(context.Background(), "/project", nil); empty == nil || len(empty) != 0 {
		t.Fatalf("empty=%#v", empty)
	}
}

func TestInspectRejectsInvalidModuleStates(t *testing.T) {
	for _, test := range []struct {
		name       string
		goList     string
		manifest   []byte
		goMod      []byte
		importPath string
		wantCode   string
	}{
		{name: "module missing", goList: `{"ImportPath":"example.com/nodes/extensions/echo","Dir":"/module/echo"}`, wantCode: "NODE_PACKAGE_MANIFEST_NOT_FOUND"},
		{name: "package outside module", goList: goListFixture("other.example/extensions/echo", false, false), importPath: "other.example/extensions/echo", wantCode: "NODE_PACKAGE_ID_MISMATCH"},
		{name: "manifest missing", goList: goListFixture("example.com/nodes/extensions/echo", false, false), wantCode: "NODE_PACKAGE_MANIFEST_NOT_FOUND"},
		{name: "manifest invalid", goList: goListFixture("example.com/nodes/extensions/echo", false, false), manifest: []byte(`{"kind":"bad"}`), goMod: []byte(inspectorGoMod), wantCode: "NODE_PACKAGE_MANIFEST_INVALID"},
		{name: "identity mismatch", goList: goListFixture("example.com/nodes/extensions/echo", false, false), manifest: []byte(strings.ReplaceAll(validManifestJSON, "example.com/nodes", "example.com/impostor")), goMod: []byte(inspectorGoMod), wantCode: "NODE_PACKAGE_ID_MISMATCH"},
		{name: "registration precedes missing sdk", goList: goListFixture("example.com/nodes/extensions/search", false, false), manifest: []byte(validManifestJSON), importPath: "example.com/nodes/extensions/search", wantCode: "NODE_PACKAGE_REGISTRATION_NOT_DECLARED"},
		{name: "api precedes missing sdk", goList: goListFixture("example.com/nodes/extensions/echo", false, false), manifest: []byte(strings.Replace(validManifestJSON, `"nodeAPI":"agent-studio.dev/v1alpha1"`, `"nodeAPI":"agent-studio.dev/v2"`, 1)), wantCode: "NODE_PACKAGE_API_INCOMPATIBLE"},
		{name: "sdk direct require missing", goList: goListFixture("example.com/nodes/extensions/echo", false, false), manifest: []byte(validManifestJSON), goMod: []byte("module example.com/nodes\n\ngo 1.26.0\n"), wantCode: "NODE_PACKAGE_SDK_REQUIREMENT_MISSING"},
		{name: "sdk too new", goList: goListFixture("example.com/nodes/extensions/echo", false, false), manifest: []byte(validManifestJSON), goMod: []byte(strings.Replace(inspectorGoMod, "v0.3.0", "v0.4.0", 1)), wantCode: "NODE_PACKAGE_SDK_TOO_NEW"},
	} {
		t.Run(test.name, func(t *testing.T) {
			importPath := test.importPath
			if importPath == "" {
				importPath = "example.com/nodes/extensions/echo"
			}
			files := map[string][]byte{}
			if test.manifest != nil {
				files["/module/agent-studio.node-package.json"] = test.manifest
			}
			if test.goMod != nil {
				files["/module/go.mod"] = test.goMod
			}
			inspection := Inspector{
				Command: func(context.Context, string, map[string]string, string, ...string) ([]byte, error) {
					return []byte(test.goList), nil
				},
				ReadFile: fakeInspectorReadFile(files), RuntimeVersion: "v0.3.1", SDKVersion: "0.3.0", NodeAPIVersion: APIVersion,
			}.Inspect(context.Background(), "/project", importPath)
			if inspection.Error() == nil || !hasDiagnosticCode(inspection.Diagnostics, test.wantCode) {
				t.Fatalf("diagnostics=%+v error=%v", inspection.Diagnostics, inspection.Error())
			}
		})
	}
}

func TestInspectClassifiesReplacementAndSameModule(t *testing.T) {
	for _, test := range []struct {
		name       string
		main       bool
		replaced   bool
		goMod      string
		wantSource Source
		wantCode   string
	}{
		{name: "replacement", replaced: true, goMod: inspectorGoMod, wantSource: SourceReplacement, wantCode: "NODE_PACKAGE_LOCAL_REPLACE"},
		{name: "same module skips sdk require", main: true, goMod: "module example.com/nodes\n\ngo 1.26.0\n", wantSource: SourceDevelopment},
	} {
		t.Run(test.name, func(t *testing.T) {
			inspection := Inspector{
				Command: func(context.Context, string, map[string]string, string, ...string) ([]byte, error) {
					return []byte(goListFixture("example.com/nodes/extensions/echo", test.main, test.replaced)), nil
				},
				ReadFile: fakeInspectorReadFile(map[string][]byte{
					"/module/agent-studio.node-package.json": []byte(validManifestJSON),
					"/module/go.mod":                         []byte(test.goMod),
				}),
				RuntimeVersion: "v0.3.1", SDKVersion: "0.3.0", NodeAPIVersion: APIVersion,
			}.Inspect(context.Background(), "/project", "example.com/nodes/extensions/echo")
			if inspection.Error() != nil || inspection.Record.Summary.Source != test.wantSource {
				t.Fatalf("inspection=%+v error=%v", inspection, inspection.Error())
			}
			if test.wantCode != "" && !hasDiagnosticCode(inspection.Diagnostics, test.wantCode) {
				t.Fatalf("diagnostics=%+v", inspection.Diagnostics)
			}
			if strings.Contains(fmt.Sprintf("%+v", inspection), "/secret/replacement") {
				t.Fatalf("inspection exposed replacement path: %+v", inspection)
			}
		})
	}
}

func TestInspectionDoesNotExposeLocalPaths(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "token-ghp_secret")
	inspection := Inspector{
		Command: func(context.Context, string, map[string]string, string, ...string) ([]byte, error) {
			return []byte("failure at " + secret), errors.New("go list failed: " + secret)
		},
		RuntimeVersion: "v0.3.1", SDKVersion: "0.3.0", NodeAPIVersion: APIVersion,
	}.Inspect(context.Background(), "/project", "example.com/nodes/extensions/echo")
	combined := fmt.Sprintf("%+v %v", inspection, inspection.Error())
	if strings.Contains(combined, secret) || strings.Contains(combined, "ghp_secret") {
		t.Fatalf("inspection leaked local data: %s", combined)
	}
}

func TestInspectionRejectsUnsafeImportPathBeforeCommand(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "ghp_secret")
	called := false
	inspection := Inspector{
		Command: func(context.Context, string, map[string]string, string, ...string) ([]byte, error) {
			called = true
			return nil, nil
		},
	}.Inspect(context.Background(), "/project", secret)
	combined := fmt.Sprintf("%+v %v", inspection, inspection.Error())
	if called || inspection.Error() == nil || strings.Contains(combined, secret) || strings.Contains(combined, "ghp_secret") {
		t.Fatalf("called=%t inspection=%s", called, combined)
	}
}

func goListFixture(importPath string, main, replaced bool) string {
	replace := ""
	if replaced {
		replace = `,"Replace":{"Path":"../private","Dir":"/secret/replacement"}`
	}
	return fmt.Sprintf(`{"ImportPath":%q,"Dir":"/module/extensions/pkg","Module":{"Path":"example.com/nodes","Version":"v0.3.0","Dir":"/module","Main":%t%s}}`, importPath, main, replace)
}

func fakeInspectorReadFile(files map[string][]byte) func(string) ([]byte, error) {
	return func(path string) ([]byte, error) {
		data, ok := files[path]
		if !ok {
			return nil, os.ErrNotExist
		}
		return append([]byte(nil), data...), nil
	}
}
