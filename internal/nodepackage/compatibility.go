package nodepackage

import (
	"regexp"
	"strings"

	"golang.org/x/mod/semver"
)

var developmentRuntimePattern = regexp.MustCompile(`^([0-9]+\.[0-9]+\.[0-9]+)-dev$`)

func NormalizeSDKVersion(value string) (string, bool) {
	normalized := value
	if !strings.HasPrefix(normalized, "v") {
		normalized = "v" + normalized
	}
	if !semver.IsValid(normalized) {
		return "", false
	}
	return normalized, true
}

func NormalizeRuntimeVersion(value string) (normalized string, development bool, ok bool) {
	if matches := developmentRuntimePattern.FindStringSubmatch(value); matches != nil {
		normalized = "v" + matches[1]
		return normalized, true, semver.IsValid(normalized)
	}
	if !semver.IsValid(value) {
		return "", false, false
	}
	return value, false, true
}

func CheckCompatibility(manifest Manifest, runtimeVersion, sdkVersion, nodeAPI, requiredSDK string) []Diagnostic {
	diagnostics := checkRuntimeAndAPI(manifest, runtimeVersion, nodeAPI)
	diagnostics = append(diagnostics, checkSDK(manifest.Metadata.Name, sdkVersion, requiredSDK)...)
	return SortDiagnostics(diagnostics)
}

func checkRuntimeAndAPI(manifest Manifest, runtimeVersion, nodeAPI string) []Diagnostic {
	diagnostics := make([]Diagnostic, 0, 4)
	packageName := manifest.Metadata.Name
	if manifest.Compatibility.NodeAPI != nodeAPI {
		diagnostics = append(diagnostics, Diagnostic{
			Severity: SeverityError, Code: "NODE_PACKAGE_API_INCOMPATIBLE", Package: packageName,
			Path: "compatibility.nodeAPI", Message: "节点包不兼容当前 Node API",
		})
	}
	runtime, development, runtimeOK := NormalizeRuntimeVersion(runtimeVersion)
	if !runtimeOK || semver.Compare(runtime, manifest.Compatibility.Runtime.MinVersion) < 0 ||
		semver.Compare(runtime, manifest.Compatibility.Runtime.MaxVersionExclusive) >= 0 {
		diagnostics = append(diagnostics, Diagnostic{
			Severity: SeverityError, Code: "NODE_PACKAGE_RUNTIME_INCOMPATIBLE", Package: packageName,
			Path: "compatibility.runtime", Message: "节点包不兼容当前 Agent Studio Runtime",
		})
	}
	if development {
		diagnostics = append(diagnostics, Diagnostic{
			Severity: SeverityWarning, Code: "NODE_PACKAGE_DEVELOPMENT_VERSION", Package: packageName,
			Path: "compatibility.runtime", Message: "当前使用 Agent Studio 开发版本进行兼容检查",
		})
	}
	return SortDiagnostics(diagnostics)
}

func checkSDK(packageName, sdkVersion, requiredSDK string) []Diagnostic {
	diagnostics := make([]Diagnostic, 0, 1)
	if requiredSDK == "" {
		diagnostics = append(diagnostics, Diagnostic{
			Severity: SeverityError, Code: "NODE_PACKAGE_SDK_REQUIREMENT_MISSING", Package: packageName,
			Path: "go.mod", Message: "节点包 Module 必须直接依赖 Agent Studio SDK",
		})
	} else {
		available, availableOK := NormalizeSDKVersion(sdkVersion)
		required, requiredOK := NormalizeSDKVersion(requiredSDK)
		if !availableOK || !requiredOK || semver.Compare(required, available) > 0 {
			diagnostics = append(diagnostics, Diagnostic{
				Severity: SeverityError, Code: "NODE_PACKAGE_SDK_TOO_NEW", Package: packageName,
				Path: "go.mod", Message: "节点包要求的 Agent Studio SDK 高于当前 Runtime",
			})
		}
	}
	return SortDiagnostics(diagnostics)
}
