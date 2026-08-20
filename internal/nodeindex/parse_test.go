package nodeindex

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

func TestParseValidIndexUsesNonNilSlices(t *testing.T) {
	got, err := Parse("valid.json", readFixture(t, "valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Packages == nil || len(got.Packages) != 1 {
		t.Fatalf("packages=%#v", got.Packages)
	}
	pkg := got.Packages[0]
	if pkg.Categories == nil || pkg.Keywords == nil || pkg.Versions == nil {
		t.Fatalf("nil package slices: %+v", pkg)
	}
	manifest := pkg.Versions[0].Manifest
	if manifest.Registrations == nil || manifest.Registrations[0].Nodes == nil {
		t.Fatalf("nil manifest slices: %+v", manifest)
	}
}

func TestParseRejectsUnsafeInputs(t *testing.T) {
	for _, name := range []string{
		"unknown-field.json",
		"duplicate-key.json",
		"too-many-versions.json",
		"manifest-name-mismatch.json",
		"trailing.json",
		"large-number.json",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(name, readFixture(t, filepath.Join("invalid", name)))
			if err == nil || CodeOf(err) != CodeContentInvalid {
				t.Fatalf("err=%v code=%q", err, CodeOf(err))
			}
		})
	}
}

func TestParseRejectsSemanticAndBudgetBoundaries(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "prerelease index", mutate: func(index map[string]any) { metadataOf(index)["release"] = "v0.2.0-rc.1" }},
		{name: "index release too long", mutate: func(index map[string]any) { metadataOf(index)["release"] = "v" + strings.Repeat("1", 125) + ".0.0" }},
		{name: "fractional generated time", mutate: func(index map[string]any) { metadataOf(index)["generatedAt"] = "2026-08-20T08:00:00.1Z" }},
		{name: "uppercase source oid", mutate: func(index map[string]any) { metadataOf(index)["sourceCommit"] = strings.Repeat("A", 40) }},
		{name: "duplicate package", mutate: func(index map[string]any) {
			index["packages"] = append(packagesOf(index), deepClone(t, packageOf(index)))
		}},
		{name: "unsorted categories", mutate: func(index map[string]any) { packageOf(index)["categories"] = []any{"search", "integration"} }},
		{name: "invalid category", mutate: func(index map[string]any) { packageOf(index)["categories"] = []any{"Not Valid"} }},
		{name: "too many categories", mutate: func(index map[string]any) {
			values := make([]any, 9)
			for i := range values {
				values[i] = "category-" + string(rune('a'+i))
			}
			packageOf(index)["categories"] = values
		}},
		{name: "category too long", mutate: func(index map[string]any) { packageOf(index)["categories"] = []any{strings.Repeat("a", 33)} }},
		{name: "uppercase keyword", mutate: func(index map[string]any) { packageOf(index)["keywords"] = []any{"Search"} }},
		{name: "unsorted keywords", mutate: func(index map[string]any) { packageOf(index)["keywords"] = []any{"search", "http"} }},
		{name: "too many keywords", mutate: func(index map[string]any) {
			values := make([]any, 17)
			for i := range values {
				values[i] = fmt.Sprintf("keyword-%02d", i)
			}
			packageOf(index)["keywords"] = values
		}},
		{name: "keyword too long", mutate: func(index map[string]any) { packageOf(index)["keywords"] = []any{strings.Repeat("a", 65)} }},
		{name: "no versions", mutate: func(index map[string]any) { packageOf(index)["versions"] = []any{} }},
		{name: "invalid package version", mutate: func(index map[string]any) { versionOf(index)["version"] = "1.0.0" }},
		{name: "repository query", mutate: func(index map[string]any) {
			sourceOf(index)["repository"] = "https://github.com/example/agent-nodes?token=secret"
		}},
		{name: "module dir traversal", mutate: func(index map[string]any) { sourceOf(index)["moduleDir"] = "nodes/../other" }},
		{name: "invalid manifest digest", mutate: func(index map[string]any) { sourceOf(index)["manifestDigest"] = "sha256:ABC" }},
		{name: "unapproved review", mutate: func(index map[string]any) { reviewOf(index)["status"] = "pending" }},
		{name: "fractional review time", mutate: func(index map[string]any) { reviewOf(index)["reviewedAt"] = "2026-08-20T07:30:00.1Z" }},
		{name: "active lifecycle message", mutate: func(index map[string]any) { lifecycleOf(index)["message"] = "unexpected" }},
		{name: "deprecated missing message", mutate: func(index map[string]any) { lifecycleOf(index)["status"] = "deprecated" }},
		{name: "lifecycle html", mutate: func(index map[string]any) {
			lifecycleOf(index)["status"] = "withdrawn"
			lifecycleOf(index)["message"] = "<b>removed</b>"
		}},
		{name: "manifest repository mismatch", mutate: func(index map[string]any) {
			manifestMetadataOf(index)["repository"] = "https://github.com/example/other"
		}},
		{name: "manifest unknown field", mutate: func(index map[string]any) { manifestOf(index)["unexpected"] = true }},
		{name: "unsorted manifest registrations", mutate: func(index map[string]any) {
			manifest := manifestOf(index)
			registrations := manifest["registrations"].([]any)
			second := deepClone(t, registrations[0].(map[string]any))
			second["package"] = "github.com/example/agent-nodes/alpha"
			second["nodes"] = []any{map[string]any{"type": "example.alpha", "version": "1"}}
			manifest["registrations"] = append(registrations, second)
		}},
		{name: "unsorted manifest nodes", mutate: func(index map[string]any) {
			registration := manifestOf(index)["registrations"].([]any)[0].(map[string]any)
			registration["nodes"] = append(registration["nodes"].([]any), map[string]any{"type": "example.alpha", "version": "1"})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			index := decodeIndexFixture(t)
			test.mutate(index)
			_, err := Parse("fixture.json", encodeIndexFixture(t, index))
			if CodeOf(err) != CodeContentInvalid {
				t.Fatalf("err=%v code=%q", err, CodeOf(err))
			}
		})
	}

	for _, raw := range [][]byte{
		append(readFixture(t, "valid.json"), make([]byte, MaxIndexBytes)...),
		append([]byte(`{"apiVersion":"agent-studio.dev/v1alpha1","kind":"NodePackageIndex","metadata":{"release":"v0.1.0","generatedAt":"2026-08-20T08:00:00Z","sourceCommit":"fedcba9876543210fedcba9876543210fedcba98"},"packages":[]}`), 0xff),
		[]byte(strings.Repeat("[", maxJSONDepth+2) + strings.Repeat("]", maxJSONDepth+2)),
	} {
		if _, err := Parse("fixture.json", raw); CodeOf(err) != CodeContentInvalid {
			t.Fatalf("boundary err=%v code=%q", err, CodeOf(err))
		}
	}
}

func TestParseRejectsMoreThanOneThousandPackages(t *testing.T) {
	index := decodeIndexFixture(t)
	base := packageOf(index)
	packages := make([]any, 1001)
	for i := range packages {
		pkg := deepClone(t, base)
		name := fmt.Sprintf("github.com/example/agent-nodes-%04d", i)
		repository := fmt.Sprintf("https://github.com/example/agent-nodes-%04d", i)
		pkg["name"] = name
		version := pkg["versions"].([]any)[0].(map[string]any)
		version["source"].(map[string]any)["repository"] = repository
		manifest := version["manifest"].(map[string]any)
		metadata := manifest["metadata"].(map[string]any)
		metadata["name"] = name
		metadata["repository"] = repository
		manifest["registrations"].([]any)[0].(map[string]any)["package"] = name + "/search"
		packages[i] = pkg
	}
	index["packages"] = packages
	if raw := encodeIndexFixture(t, index); len(raw) >= MaxIndexBytes {
		t.Fatalf("fixture unexpectedly exceeds byte budget: %d", len(raw))
	} else if _, err := Parse("fixture.json", raw); CodeOf(err) != CodeContentInvalid {
		t.Fatalf("err=%v code=%q", err, CodeOf(err))
	}
}

func TestParseAcceptsPlainTextLifecycleMessage(t *testing.T) {
	index := decodeIndexFixture(t)
	lifecycleOf(index)["status"] = "deprecated"
	lifecycleOf(index)["message"] = "第一行\n第二行"
	if _, err := Parse("fixture.json", encodeIndexFixture(t, index)); err != nil {
		t.Fatal(err)
	}
}

func TestParseUsesStableSafeErrors(t *testing.T) {
	for _, field := range []string{"apiVersion", "kind"} {
		index := decodeIndexFixture(t)
		index[field] = "bearer-secret-123"
		_, err := Parse("/private/cache/token.json", encodeIndexFixture(t, index))
		if CodeOf(err) != CodeSchemaUnsupported || strings.Contains(err.Error(), "/private") || strings.Contains(err.Error(), "bearer-secret-123") {
			t.Fatalf("field=%s err=%v code=%q", field, err, CodeOf(err))
		}
	}

	raw := bytes.Replace(readFixture(t, "valid.json"), []byte(`"kind": "NodePackageIndex"`), []byte(`"bearer_secret_value": true, "kind": "NodePackageIndex"`), 1)
	_, err := Parse("/private/cache/token.json", raw)
	if CodeOf(err) != CodeContentInvalid || strings.Contains(err.Error(), "bearer") || strings.Contains(err.Error(), "/private") {
		t.Fatalf("err=%v code=%q", err, CodeOf(err))
	}
}

func TestEmbeddedOfficialSnapshotParses(t *testing.T) {
	index, err := Parse("embedded", embeddedIndex)
	if err != nil {
		t.Fatal(err)
	}
	if index.Metadata.Release != "v0.1.0" || index.Packages == nil || len(index.Packages) != 0 {
		t.Fatalf("index=%+v", index)
	}
}

func TestVendoredC1AssetsMatchImmutableReleaseDigests(t *testing.T) {
	root := repositoryRoot(t)
	assets := []struct {
		path   string
		digest string
	}{
		{path: filepath.Join(root, "contracts", "node-index-v1alpha1.schema.json"), digest: "5ac1fb0b1298cc559f9428d1cf9b4b78736da1f1ee574eb79ee0560ee7bdf70d"},
		{path: filepath.Join(root, "internal", "nodeindex", "assets", "index.json"), digest: "b829b085a2a4a8606a8a6d4c284ddef6dcedd0e380cabe9435e1ac4432c8443f"},
	}
	for _, asset := range assets {
		raw, err := os.ReadFile(asset.path)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		if got := hex.EncodeToString(sum[:]); got != asset.digest {
			t.Fatalf("%s digest=%s", asset.path, got)
		}
	}

	var provenance struct {
		Repository string `json:"repository"`
		Release    string `json:"release"`
		ReleaseURL string `json:"releaseUrl"`
		Assets     map[string]struct {
			Name   string `json:"name"`
			SHA256 string `json:"sha256"`
		} `json:"assets"`
	}
	raw, err := os.ReadFile(filepath.Join(root, "contracts", "node-index-source.json"))
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&provenance); err != nil {
		t.Fatal(err)
	}
	if provenance.Repository != "https://github.com/yyl1212/agent-studio-node-index" ||
		provenance.Release != "v0.1.0" ||
		provenance.ReleaseURL != "https://github.com/yyl1212/agent-studio-node-index/releases/tag/v0.1.0" ||
		provenance.Assets["schema"].SHA256 != assets[0].digest ||
		provenance.Assets["index"].SHA256 != assets[1].digest {
		t.Fatalf("provenance=%+v", provenance)
	}
}

func TestIndexSchemaCoversStructuralFixtures(t *testing.T) {
	schema := compileIndexSchema(t)
	tests := []struct {
		name string
		want bool
	}{
		{name: "valid.json", want: true},
		{name: filepath.Join("invalid", "unknown-field.json")},
		{name: filepath.Join("invalid", "too-many-versions.json")},
		{name: filepath.Join("invalid", "large-number.json")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document, err := jsonschema.UnmarshalJSON(bytes.NewReader(readFixture(t, test.name)))
			got := err == nil && schema.Validate(document) == nil
			if got != test.want {
				t.Fatalf("schema valid=%t want=%t decodeErr=%v", got, test.want, err)
			}
		})
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func compileIndexSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repositoryRoot(t), "contracts", "node-index-v1alpha1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const resource = "https://agent-studio.dev/contracts/node-index-v1alpha1.schema.json"
	if err := compiler.AddResource(resource, document); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(resource)
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate parse_test.go")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(current), "..", ".."))
}

func decodeIndexFixture(t *testing.T) map[string]any {
	t.Helper()
	var index map[string]any
	if err := json.Unmarshal(readFixture(t, "valid.json"), &index); err != nil {
		t.Fatal(err)
	}
	return index
}

func encodeIndexFixture(t *testing.T, index map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func metadataOf(index map[string]any) map[string]any {
	return index["metadata"].(map[string]any)
}

func packagesOf(index map[string]any) []any {
	return index["packages"].([]any)
}

func packageOf(index map[string]any) map[string]any {
	return packagesOf(index)[0].(map[string]any)
}

func versionOf(index map[string]any) map[string]any {
	return packageOf(index)["versions"].([]any)[0].(map[string]any)
}

func sourceOf(index map[string]any) map[string]any {
	return versionOf(index)["source"].(map[string]any)
}

func reviewOf(index map[string]any) map[string]any {
	return versionOf(index)["review"].(map[string]any)
}

func lifecycleOf(index map[string]any) map[string]any {
	return versionOf(index)["lifecycle"].(map[string]any)
}

func manifestOf(index map[string]any) map[string]any {
	return versionOf(index)["manifest"].(map[string]any)
}

func manifestMetadataOf(index map[string]any) map[string]any {
	return manifestOf(index)["metadata"].(map[string]any)
}

func deepClone[T any](t *testing.T, value T) T {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var cloned T
	if err := json.Unmarshal(raw, &cloned); err != nil {
		t.Fatal(err)
	}
	return cloned
}
