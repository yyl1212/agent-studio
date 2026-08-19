package nodepackage

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const validManifestJSON = `{
  "apiVersion":"agent-studio.dev/v1alpha1",
  "kind":"NodePackage",
  "metadata":{
    "name":"example.com/nodes",
    "displayName":"Example Nodes",
    "description":"Example node package",
    "license":"Apache-2.0",
    "repository":"https://example.com/nodes"
  },
  "compatibility":{
    "nodeAPI":"agent-studio.dev/v1alpha1",
    "runtime":{"minVersion":"v0.2.0","maxVersionExclusive":"v0.4.0"}
  },
  "registrations":[{
    "package":"example.com/nodes/extensions/echo",
    "nodes":[{"type":"example.echo","version":"1.0.0"}]
  }]
}`

func TestParseNodePackageManifest(t *testing.T) {
	parsed, err := Parse("fixture.json", []byte(validManifestJSON))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Metadata.Name != "example.com/nodes" || parsed.Registrations == nil {
		t.Fatalf("manifest=%+v", parsed)
	}

	emptyFixture := decodeManifestFixture(t)
	emptyFixture["registrations"] = []any{}
	empty, err := Parse("empty.json", encodeManifestFixture(t, emptyFixture))
	if err != nil {
		t.Fatal(err)
	}
	if empty.Registrations == nil || len(empty.Registrations) != 0 {
		t.Fatalf("empty registrations=%#v", empty.Registrations)
	}
}

func TestParserAndSchemaEnforceTheirOwnContracts(t *testing.T) {
	fixtures := []struct {
		name         string
		raw          []byte
		wantParseOK  bool
		wantSchemaOK bool
	}{
		{name: "valid", raw: []byte(validManifestJSON), wantParseOK: true, wantSchemaOK: true},
		{name: "wrong kind", raw: []byte(strings.Replace(validManifestJSON, `"NodePackage"`, `"WorkflowPackage"`, 1)), wantSchemaOK: false},
		{name: "unknown field", raw: replaceManifest(t, "metadata", "unknown", true), wantSchemaOK: false},
		{name: "missing description", raw: removeManifestField(t, "metadata", "description"), wantSchemaOK: false},
		{name: "http repository", raw: []byte(strings.Replace(validManifestJSON, "https://example.com/nodes", "http://example.com/nodes", 1)), wantSchemaOK: false},
		{name: "repository userinfo", raw: []byte(strings.Replace(validManifestJSON, "https://example.com/nodes", "https://user@example.com/nodes", 1)), wantSchemaOK: false},
		{name: "invalid runtime semver", raw: []byte(strings.Replace(validManifestJSON, `"v0.2.0"`, `"0.2.0"`, 1)), wantSchemaOK: false},
		{name: "runtime range reversed", raw: []byte(strings.Replace(validManifestJSON, `"v0.4.0"`, `"v0.1.0"`, 1)), wantSchemaOK: true},
		{name: "registration outside module", raw: []byte(strings.Replace(validManifestJSON, "example.com/nodes/extensions/echo", "other.example/echo", 1)), wantSchemaOK: true},
		{name: "duplicate registration", raw: duplicateRegistrationFixture(t), wantSchemaOK: true},
		{name: "duplicate node across registrations", raw: duplicateNodeFixture(t), wantSchemaOK: true},
		{name: "empty registration nodes", raw: []byte(strings.Replace(validManifestJSON, `"nodes":[{"type":"example.echo","version":"1.0.0"}]`, `"nodes":[]`, 1)), wantSchemaOK: false},
		{name: "too many registrations", raw: oversizedRegistrationsFixture(t), wantSchemaOK: false},
		{name: "too many nodes", raw: oversizedNodesFixture(t), wantSchemaOK: false},
		{name: "display name too long", raw: []byte(strings.Replace(validManifestJSON, "Example Nodes", strings.Repeat("x", 129), 1)), wantSchemaOK: false},
		{name: "multiple JSON values", raw: append([]byte(validManifestJSON), []byte(`{}`)...), wantSchemaOK: false},
	}

	schema := compileManifestSchema(t)
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			_, parseErr := Parse("fixture.json", fixture.raw)
			if got := parseErr == nil; got != fixture.wantParseOK {
				t.Fatalf("Parse ok=%t want=%t err=%v", got, fixture.wantParseOK, parseErr)
			}
			schemaOK := validateWithSchema(schema, fixture.raw) == nil
			if schemaOK != fixture.wantSchemaOK {
				t.Fatalf("Schema ok=%t want=%t", schemaOK, fixture.wantSchemaOK)
			}
		})
	}
}

func TestParseRejectsBudgetsDuplicatesAndUnsafeURLs(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
	}{
		{name: "manifest bytes", raw: append([]byte(validManifestJSON), bytes.Repeat([]byte(" "), 256<<10)...)},
		{name: "repository query", raw: []byte(strings.Replace(validManifestJSON, "https://example.com/nodes", "https://example.com/nodes?token=x", 1))},
		{name: "repository fragment", raw: []byte(strings.Replace(validManifestJSON, "https://example.com/nodes", "https://example.com/nodes#token", 1))},
		{name: "invalid module path", raw: []byte(strings.Replace(validManifestJSON, "example.com/nodes", "example.com/../nodes", 1))},
		{name: "duplicate registration", raw: duplicateRegistrationFixture(t)},
		{name: "duplicate node", raw: duplicateNodeFixture(t)},
		{name: "too many registrations", raw: oversizedRegistrationsFixture(t)},
		{name: "too many nodes", raw: oversizedNodesFixture(t)},
		{name: "invalid utf8", raw: append([]byte(validManifestJSON[:len(validManifestJSON)-1]), 0xff, '}')},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Parse("fixture.json", test.raw); err == nil {
				t.Fatal("expected manifest to be rejected")
			}
		})
	}
}

func TestParseRejectsEveryStringLengthBoundary(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "module path", mutate: func(manifest map[string]any) {
			metadataOf(manifest)["name"] = strings.Repeat("a", 513)
		}},
		{name: "display name", mutate: func(manifest map[string]any) {
			metadataOf(manifest)["displayName"] = strings.Repeat("a", 129)
		}},
		{name: "description", mutate: func(manifest map[string]any) {
			metadataOf(manifest)["description"] = strings.Repeat("a", 2049)
		}},
		{name: "license", mutate: func(manifest map[string]any) {
			metadataOf(manifest)["license"] = strings.Repeat("a", 129)
		}},
		{name: "repository", mutate: func(manifest map[string]any) {
			metadataOf(manifest)["repository"] = "https://example.com/" + strings.Repeat("a", 2029)
		}},
		{name: "node api", mutate: func(manifest map[string]any) {
			compatibilityOf(manifest)["nodeAPI"] = strings.Repeat("a", 129)
		}},
		{name: "runtime version", mutate: func(manifest map[string]any) {
			runtimeOf(manifest)["minVersion"] = "v" + strings.Repeat("1", 128)
		}},
		{name: "registration package", mutate: func(manifest map[string]any) {
			registrationOf(manifest)["package"] = "example.com/nodes/" + strings.Repeat("a", 512)
		}},
		{name: "node type", mutate: func(manifest map[string]any) { nodeOf(manifest)["type"] = strings.Repeat("a", 257) }},
		{name: "node version", mutate: func(manifest map[string]any) { nodeOf(manifest)["version"] = strings.Repeat("1", 129) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := decodeManifestFixture(t)
			test.mutate(manifest)
			if _, err := Parse("fixture.json", encodeManifestFixture(t, manifest)); err == nil {
				t.Fatal("expected oversized string to be rejected")
			}
		})
	}
}

func TestEncodeCanonicalManifest(t *testing.T) {
	manifest, err := Parse("fixture.json", []byte(validManifestJSON))
	if err != nil {
		t.Fatal(err)
	}
	manifest.Registrations = append(manifest.Registrations, Registration{
		Package: "example.com/nodes/extensions/alpha",
		Nodes:   []NodeRef{{Type: "example.beta", Version: "1"}, {Type: "example.alpha", Version: "2"}},
	})
	first, err := Encode(manifest)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Encode(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || !bytes.HasSuffix(first, []byte("\n")) {
		t.Fatalf("encoding is not stable: %s", first)
	}
	if bytes.Index(first, []byte("extensions/alpha")) > bytes.Index(first, []byte("extensions/echo")) ||
		bytes.Index(first, []byte(`"example.alpha"`)) > bytes.Index(first, []byte(`"example.beta"`)) {
		t.Fatalf("manifest is not sorted: %s", first)
	}
}

func TestEncodeUsesEmptyRegistrationArrayForNil(t *testing.T) {
	manifest, err := Parse("fixture.json", []byte(validManifestJSON))
	if err != nil {
		t.Fatal(err)
	}
	manifest.Registrations = nil
	encoded, err := Encode(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"registrations": []`)) {
		t.Fatalf("registrations must be an array: %s", encoded)
	}
}

func compileManifestSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	schemaPath := filepath.Join(filepath.Dir(current), "..", "..", "contracts", "node-package.schema.json")
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const resource = "https://agent-studio.dev/contracts/node-package.schema.json"
	if err := compiler.AddResource(resource, document); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(resource)
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func validateWithSchema(schema *jsonschema.Schema, raw []byte) error {
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	return schema.Validate(document)
}

func replaceManifest(t *testing.T, objectName, key string, value any) []byte {
	t.Helper()
	var manifest map[string]any
	if err := json.Unmarshal([]byte(validManifestJSON), &manifest); err != nil {
		t.Fatal(err)
	}
	object := manifest[objectName].(map[string]any)
	object[key] = value
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func removeManifestField(t *testing.T, objectName, key string) []byte {
	t.Helper()
	manifest := decodeManifestFixture(t)
	delete(manifest[objectName].(map[string]any), key)
	return encodeManifestFixture(t, manifest)
}

func duplicateRegistrationFixture(t *testing.T) []byte {
	t.Helper()
	manifest := decodeManifestFixture(t)
	registration := manifest["registrations"].([]any)[0].(map[string]any)
	duplicate := map[string]any{"package": registration["package"], "nodes": []any{map[string]any{"type": "example.other", "version": "1"}}}
	manifest["registrations"] = append(manifest["registrations"].([]any), duplicate)
	return encodeManifestFixture(t, manifest)
}

func duplicateNodeFixture(t *testing.T) []byte {
	t.Helper()
	manifest := decodeManifestFixture(t)
	manifest["registrations"] = append(manifest["registrations"].([]any), map[string]any{
		"package": "example.com/nodes/extensions/other",
		"nodes":   []any{map[string]any{"type": "example.echo", "version": "1.0.0"}},
	})
	return encodeManifestFixture(t, manifest)
}

func oversizedRegistrationsFixture(t *testing.T) []byte {
	t.Helper()
	manifest := decodeManifestFixture(t)
	registrations := make([]any, 0, 129)
	for index := 0; index < 129; index++ {
		suffix := strconv.Itoa(index)
		registrations = append(registrations, map[string]any{
			"package": "example.com/nodes/extensions/node" + suffix,
			"nodes":   []any{map[string]any{"type": "example.node" + suffix, "version": "1"}},
		})
	}
	manifest["registrations"] = registrations
	return encodeManifestFixture(t, manifest)
}

func oversizedNodesFixture(t *testing.T) []byte {
	t.Helper()
	manifest := decodeManifestFixture(t)
	nodes := make([]any, 0, 513)
	for index := 0; index < 513; index++ {
		nodes = append(nodes, map[string]any{"type": "example.node" + strconv.Itoa(index), "version": "1"})
	}
	registrationOf(manifest)["nodes"] = nodes
	return encodeManifestFixture(t, manifest)
}

func decodeManifestFixture(t *testing.T) map[string]any {
	t.Helper()
	var manifest map[string]any
	if err := json.Unmarshal([]byte(validManifestJSON), &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func encodeManifestFixture(t *testing.T, manifest map[string]any) []byte {
	t.Helper()
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func metadataOf(manifest map[string]any) map[string]any {
	return manifest["metadata"].(map[string]any)
}

func compatibilityOf(manifest map[string]any) map[string]any {
	return manifest["compatibility"].(map[string]any)
}

func runtimeOf(manifest map[string]any) map[string]any {
	return compatibilityOf(manifest)["runtime"].(map[string]any)
}

func registrationOf(manifest map[string]any) map[string]any {
	return manifest["registrations"].([]any)[0].(map[string]any)
}

func nodeOf(manifest map[string]any) map[string]any {
	return registrationOf(manifest)["nodes"].([]any)[0].(map[string]any)
}
