package nodeindex

import (
	"errors"
	"regexp"
	"slices"
	"strings"

	"github.com/yyl1212/agent-studio/internal/nodepackage"
	"golang.org/x/mod/semver"
)

type Reason string

const (
	ReasonRuntimeInvalid        Reason = "runtime_invalid"
	ReasonNodeAPIMismatch       Reason = "node_api_mismatch"
	ReasonRuntimeTooOld         Reason = "runtime_too_old"
	ReasonRuntimeTooNew         Reason = "runtime_too_new"
	ReasonNoActiveStableVersion Reason = "no_active_stable_version"
)

type Recommendation struct {
	Version *PackageVersion
	Reasons []Reason
}

var internalDevelopmentVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-dev$`)

func Recommend(pkg Package, runtime Runtime) Recommendation {
	current, err := normalizeRuntime(runtime.Version)
	if err != nil {
		return Recommendation{Reasons: []Reason{ReasonRuntimeInvalid}}
	}

	eligible := make([]PackageVersion, 0, len(pkg.Versions))
	for _, version := range pkg.Versions {
		if version.Review.Status == "approved" && version.Lifecycle.Status == "active" &&
			semver.IsValid(version.Version) && semver.Prerelease(version.Version) == "" {
			eligible = append(eligible, version)
		}
	}
	if len(eligible) == 0 {
		return Recommendation{Reasons: []Reason{ReasonNoActiveStableVersion}}
	}

	apiMatches := make([]PackageVersion, 0, len(eligible))
	for _, version := range eligible {
		if version.Manifest.Compatibility.NodeAPI == runtime.NodeAPI {
			apiMatches = append(apiMatches, version)
		}
	}
	if len(apiMatches) == 0 {
		return Recommendation{Reasons: []Reason{ReasonNodeAPIMismatch}}
	}

	candidates := make([]PackageVersion, 0, len(apiMatches))
	tooOld := false
	tooNew := false
	for _, version := range apiMatches {
		minimum := version.Manifest.Compatibility.Runtime.MinVersion
		maximum := version.Manifest.Compatibility.Runtime.MaxVersionExclusive
		if !semver.IsValid(minimum) || !semver.IsValid(maximum) || semver.Compare(maximum, minimum) <= 0 {
			continue
		}
		switch {
		case semver.Compare(current, minimum) < 0:
			tooOld = true
		case semver.Compare(current, maximum) >= 0:
			tooNew = true
		default:
			candidates = append(candidates, version)
		}
	}
	if len(candidates) == 0 {
		reasons := make([]Reason, 0, 2)
		if tooOld {
			reasons = append(reasons, ReasonRuntimeTooOld)
		}
		if tooNew {
			reasons = append(reasons, ReasonRuntimeTooNew)
		}
		if len(reasons) == 0 {
			reasons = append(reasons, ReasonNoActiveStableVersion)
		}
		return Recommendation{Reasons: reasons}
	}

	slices.SortFunc(candidates, func(left, right PackageVersion) int {
		return semver.Compare(left.Version, right.Version)
	})
	selected := clonePackageVersion(candidates[len(candidates)-1])
	return Recommendation{Version: &selected, Reasons: []Reason{}}
}

func normalizeRuntime(value string) (string, error) {
	if internalDevelopmentVersion.MatchString(value) {
		return "v" + strings.TrimSuffix(value, "-dev"), nil
	}
	candidate := value
	if !strings.HasPrefix(candidate, "v") {
		candidate = "v" + candidate
	}
	if !semver.IsValid(candidate) {
		return "", errors.New("runtime version is invalid")
	}
	return candidate, nil
}

func clonePackageVersion(input PackageVersion) PackageVersion {
	output := input
	output.Manifest.Registrations = append([]nodepackage.Registration{}, input.Manifest.Registrations...)
	for index := range output.Manifest.Registrations {
		output.Manifest.Registrations[index].Nodes = append([]nodepackage.NodeRef{}, input.Manifest.Registrations[index].Nodes...)
	}
	return output
}
