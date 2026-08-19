package nodepackage

import (
	"testing"
)

func TestNormalizeSDKVersion(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
		ok    bool
	}{
		{input: "0.3.0", want: "v0.3.0", ok: true},
		{input: "v0.3.0-rc.1", want: "v0.3.0-rc.1", ok: true},
		{input: "not-a-version"},
	} {
		got, ok := NormalizeSDKVersion(test.input)
		if got != test.want || ok != test.ok {
			t.Fatalf("NormalizeSDKVersion(%q)=(%q,%t), want (%q,%t)", test.input, got, ok, test.want, test.ok)
		}
	}
}

func TestNormalizeRuntimeVersion(t *testing.T) {
	for _, test := range []struct {
		input       string
		want        string
		development bool
		ok          bool
	}{
		{input: "v0.3.0-rc.1", want: "v0.3.0-rc.1", ok: true},
		{input: "0.3.0-dev", want: "v0.3.0", development: true, ok: true},
		{input: "broken"},
	} {
		got, development, ok := NormalizeRuntimeVersion(test.input)
		if got != test.want || development != test.development || ok != test.ok {
			t.Fatalf("NormalizeRuntimeVersion(%q)=(%q,%t,%t)", test.input, got, development, ok)
		}
	}
}

func TestCheckCompatibilityVersionMatrix(t *testing.T) {
	manifest := compatibilityManifest("v0.3.0", "v0.4.0")
	tests := []struct {
		name        string
		runtime     string
		sdk         string
		nodeAPI     string
		requiredSDK string
		wantErrors  bool
		wantCode    string
	}{
		{name: "runtime minimum included", runtime: "v0.3.0", sdk: "0.3.0", nodeAPI: APIVersion, requiredSDK: "v0.3.0"},
		{name: "runtime maximum excluded", runtime: "v0.4.0", sdk: "0.3.0", nodeAPI: APIVersion, requiredSDK: "v0.3.0", wantErrors: true, wantCode: "NODE_PACKAGE_RUNTIME_INCOMPATIBLE"},
		{name: "prerelease compares by semver", runtime: "v0.3.0-rc.1", sdk: "0.3.0", nodeAPI: APIVersion, requiredSDK: "v0.3.0", wantErrors: true, wantCode: "NODE_PACKAGE_RUNTIME_INCOMPATIBLE"},
		{name: "development base participates", runtime: "0.3.0-dev", sdk: "0.3.0", nodeAPI: APIVersion, requiredSDK: "v0.3.0", wantCode: "NODE_PACKAGE_DEVELOPMENT_VERSION"},
		{name: "development out of range keeps warning", runtime: "0.4.0-dev", sdk: "0.3.0", nodeAPI: APIVersion, requiredSDK: "v0.3.0", wantErrors: true, wantCode: "NODE_PACKAGE_DEVELOPMENT_VERSION"},
		{name: "invalid runtime fails", runtime: "broken", sdk: "0.3.0", nodeAPI: APIVersion, requiredSDK: "v0.3.0", wantErrors: true, wantCode: "NODE_PACKAGE_RUNTIME_INCOMPATIBLE"},
		{name: "node api exact", runtime: "v0.3.0", sdk: "0.3.0", nodeAPI: "agent-studio.dev/v2", requiredSDK: "v0.3.0", wantErrors: true, wantCode: "NODE_PACKAGE_API_INCOMPATIBLE"},
		{name: "sdk requirement missing", runtime: "v0.3.0", sdk: "0.3.0", nodeAPI: APIVersion, wantErrors: true, wantCode: "NODE_PACKAGE_SDK_REQUIREMENT_MISSING"},
		{name: "sdk too new", runtime: "v0.3.0", sdk: "0.3.0", nodeAPI: APIVersion, requiredSDK: "v0.4.0", wantErrors: true, wantCode: "NODE_PACKAGE_SDK_TOO_NEW"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			diagnostics := CheckCompatibility(manifest, test.runtime, test.sdk, test.nodeAPI, test.requiredSDK)
			if HasErrors(diagnostics) != test.wantErrors {
				t.Fatalf("diagnostics=%+v", diagnostics)
			}
			if test.wantCode != "" && !hasDiagnosticCode(diagnostics, test.wantCode) {
				t.Fatalf("missing code %s in %+v", test.wantCode, diagnostics)
			}
		})
	}
}

func compatibilityManifest(minimum, maximum string) Manifest {
	return Manifest{
		Metadata: Metadata{Name: "example.com/nodes"},
		Compatibility: Compatibility{
			NodeAPI: APIVersion,
			Runtime: RuntimeRange{MinVersion: minimum, MaxVersionExclusive: maximum},
		},
	}
}

func hasDiagnosticCode(diagnostics []Diagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}
