package nodeindex

import (
	"slices"
	"testing"

	"github.com/yyl1212/agent-studio/internal/nodepackage"
)

func TestRecommendSelectsHighestCompatibleActiveStableVersion(t *testing.T) {
	pkg := recommendationPackage(t,
		recommendationVersion(t, "v1.0.0", "approved", "active", APIVersion, "v0.2.0", "v0.4.0"),
		recommendationVersion(t, "v1.1.0-rc.1", "approved", "active", APIVersion, "v0.2.0", "v0.4.0"),
		recommendationVersion(t, "v1.1.0", "approved", "deprecated", APIVersion, "v0.2.0", "v0.4.0"),
		recommendationVersion(t, "v1.2.0", "approved", "active", APIVersion, "v0.4.0", "v0.5.0"),
	)
	got := Recommend(pkg, Runtime{Version: "0.3.0-dev", NodeAPI: APIVersion})
	if got.Version == nil || got.Version.Version != "v1.0.0" || got.Reasons == nil || len(got.Reasons) != 0 {
		t.Fatalf("recommendation=%+v", got)
	}
}

func TestRecommendUsesGoSemverAndHalfOpenRuntimeRange(t *testing.T) {
	pkg := recommendationPackage(t,
		recommendationVersion(t, "v1.9.0", "approved", "active", APIVersion, "v0.3.0", "v0.4.0"),
		recommendationVersion(t, "v1.10.0", "approved", "active", APIVersion, "v0.3.0", "v0.4.0"),
	)
	for _, runtimeVersion := range []string{"0.3.0", "v0.3.0", "0.3.0-dev"} {
		got := Recommend(pkg, Runtime{Version: runtimeVersion, NodeAPI: APIVersion})
		if got.Version == nil || got.Version.Version != "v1.10.0" {
			t.Fatalf("runtime=%s recommendation=%+v", runtimeVersion, got)
		}
	}
	got := Recommend(pkg, Runtime{Version: "v0.4.0", NodeAPI: APIVersion})
	if got.Version != nil || !slices.Equal(got.Reasons, []Reason{ReasonRuntimeTooNew}) {
		t.Fatalf("maximum recommendation=%+v", got)
	}
}

func TestRecommendExplainsNoRecommendationDeterministically(t *testing.T) {
	tests := []struct {
		name    string
		runtime Runtime
		pkg     Package
		want    []Reason
	}{
		{
			name:    "invalid runtime",
			runtime: Runtime{Version: "vv0.3.0", NodeAPI: APIVersion},
			pkg:     recommendationPackage(t, recommendationVersion(t, "v1.0.0", "approved", "active", APIVersion, "v0.2.0", "v0.4.0")),
			want:    []Reason{ReasonRuntimeInvalid},
		},
		{
			name:    "node api mismatch",
			runtime: Runtime{Version: "v0.3.0", NodeAPI: APIVersion},
			pkg:     recommendationPackage(t, recommendationVersion(t, "v1.0.0", "approved", "active", "agent-studio.dev/v1alpha2", "v0.2.0", "v0.4.0")),
			want:    []Reason{ReasonNodeAPIMismatch},
		},
		{
			name:    "runtime too old and too new across a gap",
			runtime: Runtime{Version: "v0.3.0", NodeAPI: APIVersion},
			pkg: recommendationPackage(t,
				recommendationVersion(t, "v1.0.0", "approved", "active", APIVersion, "v0.1.0", "v0.2.0"),
				recommendationVersion(t, "v1.1.0", "approved", "active", APIVersion, "v0.4.0", "v0.5.0"),
			),
			want: []Reason{ReasonRuntimeTooOld, ReasonRuntimeTooNew},
		},
		{
			name:    "no approved active stable version",
			runtime: Runtime{Version: "v0.3.0", NodeAPI: APIVersion},
			pkg: recommendationPackage(t,
				recommendationVersion(t, "v1.0.0-rc.1", "approved", "active", APIVersion, "v0.2.0", "v0.4.0"),
				recommendationVersion(t, "v1.0.0", "approved", "withdrawn", APIVersion, "v0.2.0", "v0.4.0"),
				recommendationVersion(t, "v1.1.0", "pending", "active", APIVersion, "v0.2.0", "v0.4.0"),
			),
			want: []Reason{ReasonNoActiveStableVersion},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Recommend(test.pkg, test.runtime)
			if got.Version != nil || !slices.Equal(got.Reasons, test.want) {
				t.Fatalf("recommendation=%+v want=%v", got, test.want)
			}
		})
	}
}

func TestRecommendReturnsAnIsolatedVersionCopy(t *testing.T) {
	pkg := recommendationPackage(t, recommendationVersion(t, "v1.0.0", "approved", "active", APIVersion, "v0.2.0", "v0.4.0"))
	got := Recommend(pkg, Runtime{Version: "v0.3.0", NodeAPI: APIVersion})
	if got.Version == nil {
		t.Fatal("missing recommendation")
	}
	got.Version.Source.Repository = "https://github.com/changed/value"
	got.Version.Manifest.Registrations[0].Package = "github.com/changed/value"
	got.Version.Manifest.Registrations[0].Nodes[0].Type = "changed.node"
	if pkg.Versions[0].Source.Repository == got.Version.Source.Repository ||
		pkg.Versions[0].Manifest.Registrations[0].Package == got.Version.Manifest.Registrations[0].Package ||
		pkg.Versions[0].Manifest.Registrations[0].Nodes[0].Type == got.Version.Manifest.Registrations[0].Nodes[0].Type {
		t.Fatalf("input was mutated: %+v", pkg.Versions[0])
	}
}

func recommendationPackage(t *testing.T, versions ...PackageVersion) Package {
	t.Helper()
	index, err := Parse("valid.json", readFixture(t, "valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	pkg := index.Packages[0]
	pkg.Versions = append([]PackageVersion(nil), versions...)
	return pkg
}

func recommendationVersion(t *testing.T, version, review, lifecycle, nodeAPI, minimum, maximum string) PackageVersion {
	t.Helper()
	index, err := Parse("valid.json", readFixture(t, "valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	result := index.Packages[0].Versions[0]
	result.Manifest.Registrations = append([]nodepackage.Registration(nil), result.Manifest.Registrations...)
	for registrationIndex := range result.Manifest.Registrations {
		result.Manifest.Registrations[registrationIndex].Nodes = append([]nodepackage.NodeRef(nil), result.Manifest.Registrations[registrationIndex].Nodes...)
	}
	result.Version = version
	result.Review.Status = review
	result.Lifecycle.Status = lifecycle
	if lifecycle == "active" {
		result.Lifecycle.Message = ""
	} else {
		result.Lifecycle.Message = "not recommended"
	}
	result.Manifest.Compatibility.NodeAPI = nodeAPI
	result.Manifest.Compatibility.Runtime.MinVersion = minimum
	result.Manifest.Compatibility.Runtime.MaxVersionExclusive = maximum
	return result
}
