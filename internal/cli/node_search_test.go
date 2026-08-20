package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/internal/nodeindex"
	"github.com/yyl1212/agent-studio/internal/nodepackage"
)

type stubNodeSearchCatalog struct {
	search func(nodeindex.Query) (nodeindex.SearchResult, error)
	get    func(string) (nodeindex.PackageDetail, error)
}

func (catalog stubNodeSearchCatalog) Search(query nodeindex.Query) (nodeindex.SearchResult, error) {
	return catalog.search(query)
}

func (catalog stubNodeSearchCatalog) Get(name string) (nodeindex.PackageDetail, error) {
	return catalog.get(name)
}

func TestNodeSearchParsesRepeatedCategoriesAndEmptyQuery(t *testing.T) {
	var got nodeindex.Query
	code := runNodeSearch(context.Background(), []string{"search", "--category", "integration", "--category", "utility"}, io.Discard, io.Discard,
		nodeSearchTestDependencies(t, stubNodeSearchCatalog{
			search: func(query nodeindex.Query) (nodeindex.SearchResult, error) {
				got = query
				return nodeindex.SearchResult{Release: "v0.1.0", Limit: 50, Items: []nodeindex.PackageSummary{}}, nil
			},
		}))
	if code != 0 || got.Text != "" || !slices.Equal(got.Categories, []string{"integration", "utility"}) || !got.CompatibleOnly || got.Limit != 50 || got.Offset != 0 {
		t.Fatalf("code=%d query=%+v", code, got)
	}
}

func TestNodeSearchAllOnlyDisablesCompatibilityFilter(t *testing.T) {
	var got nodeindex.Query
	code := runNodeSearch(context.Background(), []string{"search", "--all", "echo"}, io.Discard, io.Discard,
		nodeSearchTestDependencies(t, stubNodeSearchCatalog{
			search: func(query nodeindex.Query) (nodeindex.SearchResult, error) {
				got = query
				return nodeindex.SearchResult{Release: "v0.1.0", Limit: 50, Items: []nodeindex.PackageSummary{}}, nil
			},
		}))
	if code != 0 || got.Text != "echo" || got.CompatibleOnly {
		t.Fatalf("code=%d query=%+v", code, got)
	}
}

func TestNodeSearchHumanOutputIncludesCompatibilityAndDisclaimer(t *testing.T) {
	result := searchOutputFixture()
	var stdout bytes.Buffer
	code := runNodeSearch(context.Background(), []string{"search", "echo"}, &stdout, io.Discard,
		nodeSearchTestDependencies(t, stubNodeSearchCatalog{search: func(nodeindex.Query) (nodeindex.SearchResult, error) { return result, nil }}))
	want := "release: v0.1.0\ntotal: 1\ngithub.com/example/nodes — Example Nodes\n  recommended: v1.2.0\n  license: Apache-2.0\n  compatibility: nodeAPI agent-studio.dev/v1alpha1; runtime [v0.2.0, v0.4.0)\n审核说明：收录表示元数据已经审核，不代表代码安全；安装和执行前请人工审查来源。\n"
	if code != 0 || stdout.String() != want || strings.Contains(stdout.String(), "go install") {
		t.Fatalf("code=%d stdout=%q", code, stdout.String())
	}
}

func TestNodeSearchHumanEscapesUntrustedControls(t *testing.T) {
	result := searchOutputFixture()
	result.Items[0].DisplayName = "Example\nNodes"
	result.Items[0].License = "Apache\x1b[31m"
	var stdout bytes.Buffer
	code := runNodeSearch(context.Background(), []string{"search"}, &stdout, io.Discard,
		nodeSearchTestDependencies(t, stubNodeSearchCatalog{search: func(nodeindex.Query) (nodeindex.SearchResult, error) { return result, nil }}))
	if code != 0 || strings.Contains(stdout.String(), "Example\nNodes") || strings.ContainsRune(stdout.String(), '\x1b') ||
		!strings.Contains(stdout.String(), `Example\nNodes`) || !strings.Contains(stdout.String(), `Apache\u001b[31m`) {
		t.Fatalf("code=%d stdout=%q", code, stdout.String())
	}
}

func TestNodeInfoHumanEscapesLifecycleControls(t *testing.T) {
	detail := infoOutputFixture()
	detail.Versions[1].Lifecycle.Message = "withdrawn\n\x1b[31m"
	var stdout bytes.Buffer
	code := runNodeSearch(context.Background(), []string{"info", "--version", "v1.1.0", detail.Name}, &stdout, io.Discard,
		nodeSearchTestDependencies(t, stubNodeSearchCatalog{get: func(string) (nodeindex.PackageDetail, error) { return detail, nil }}))
	if code != 0 || strings.Contains(stdout.String(), "withdrawn\n") || strings.ContainsRune(stdout.String(), '\x1b') ||
		!strings.Contains(stdout.String(), `withdrawn\n\u001b[31m`) {
		t.Fatalf("code=%d stdout=%q", code, stdout.String())
	}
}

func TestNodeSearchJSONIsStable(t *testing.T) {
	result := searchOutputFixture()
	var stdout bytes.Buffer
	code := runNodeSearch(context.Background(), []string{"search", "--json", "echo"}, &stdout, io.Discard,
		nodeSearchTestDependencies(t, stubNodeSearchCatalog{search: func(nodeindex.Query) (nodeindex.SearchResult, error) { return result, nil }}))
	want := "{\"release\":\"v0.1.0\",\"total\":1,\"offset\":0,\"limit\":50,\"items\":[{\"name\":\"github.com/example/nodes\",\"displayName\":\"Example Nodes\",\"description\":\"Example package\",\"license\":\"Apache-2.0\",\"repository\":\"https://github.com/example/nodes\",\"categories\":[\"integration\"],\"keywords\":[\"echo\"],\"recommendedVersion\":{\"version\":\"v1.2.0\",\"source\":{\"repository\":\"https://github.com/example/nodes\",\"moduleDir\":\"\",\"tag\":\"v1.2.0\",\"commit\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"manifestDigest\":\"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\"},\"lifecycle\":{\"status\":\"active\",\"message\":\"\"},\"compatibility\":{\"nodeAPI\":\"agent-studio.dev/v1alpha1\",\"runtime\":{\"minVersion\":\"v0.2.0\",\"maxVersionExclusive\":\"v0.4.0\"}}},\"reasons\":[]}]}\n"
	if code != 0 || stdout.String() != want {
		t.Fatalf("code=%d stdout=%q", code, stdout.String())
	}
}

func TestNodeSearchRejectsInvalidFlagsAndOversizedQuery(t *testing.T) {
	for _, args := range [][]string{
		{"search", "--unknown"},
		{"search", "one", "two"},
		{"info"},
		{"info", "--version"},
		{"missing"},
	} {
		var stderr bytes.Buffer
		code := runNodeSearch(context.Background(), args, io.Discard, &stderr, nodeSearchDependencies{})
		if code != 2 || stderr.String() != nodeSearchUsage {
			t.Fatalf("args=%v code=%d stderr=%q", args, code, stderr.String())
		}
	}

	var stderr bytes.Buffer
	catalog := stubNodeSearchCatalog{search: func(query nodeindex.Query) (nodeindex.SearchResult, error) {
		return nodeindex.Search(nodeindex.Index{}, nodeindex.Runtime{Version: "v0.3.0", NodeAPI: nodeindex.APIVersion}, query)
	}}
	code := runNodeSearch(context.Background(), []string{"search", strings.Repeat("界", 129)}, io.Discard, &stderr, nodeSearchTestDependencies(t, catalog))
	if code != 1 || !strings.Contains(stderr.String(), string(nodeindex.CodeContentInvalid)) || strings.Contains(stderr.String(), strings.Repeat("界", 129)) {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestNodeInfoSelectsExactVersionWithoutMutatingDetail(t *testing.T) {
	detail := infoOutputFixture()
	originalVersions := append([]nodeindex.PackageVersion(nil), detail.Versions...)
	var stdout bytes.Buffer
	code := runNodeSearch(context.Background(), []string{"info", "--version", "v1.1.0", "--json", detail.Name}, &stdout, io.Discard,
		nodeSearchTestDependencies(t, stubNodeSearchCatalog{get: func(name string) (nodeindex.PackageDetail, error) {
			if name != detail.Name {
				t.Fatalf("name=%q", name)
			}
			return detail, nil
		}}))
	var output nodeindex.PackageDetail
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if code != 0 || len(output.Versions) != 1 || output.Versions[0].Version != "v1.1.0" || output.RecommendedVersion == nil || output.RecommendedVersion.Version != "v1.2.0" {
		t.Fatalf("code=%d stdout=%q", code, stdout.String())
	}
	if !slices.EqualFunc(detail.Versions, originalVersions, func(left, right nodeindex.PackageVersion) bool {
		return left.Version == right.Version && left.Lifecycle == right.Lifecycle
	}) {
		t.Fatal("info command mutated catalog detail")
	}
}

func TestNodeInfoHumanShowsSourceReviewLifecycleCompatibilityAndDisclaimer(t *testing.T) {
	detail := infoOutputFixture()
	var stdout bytes.Buffer
	code := runNodeSearch(context.Background(), []string{"info", "--version", "v1.1.0", detail.Name}, &stdout, io.Discard,
		nodeSearchTestDependencies(t, stubNodeSearchCatalog{get: func(string) (nodeindex.PackageDetail, error) { return detail, nil }}))
	for _, want := range []string{
		"module: github.com/example/nodes", "recommended: v1.2.0", "version: v1.1.0",
		"source: https://github.com/example/nodes tag v1.1.0", "commit: cccccccccccccccccccccccccccccccccccccccc",
		"manifest digest: sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		"review: approved at 2026-08-19T01:02:03Z", "index commit: eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		"lifecycle: withdrawn — compromised release", "compatibility: incompatible (no_active_stable_version)",
		"审核说明：收录表示元数据已经审核，不代表代码安全；安装和执行前请人工审查来源。",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("missing %q in %q", want, stdout.String())
		}
	}
	if code != 0 || strings.Contains(stdout.String(), "go install") {
		t.Fatalf("code=%d stdout=%q", code, stdout.String())
	}
}

func TestNodeInfoUnknownPackageUsesStableJSONError(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runNodeSearch(context.Background(), []string{"info", "--json", "github.com/example/missing"}, &stdout, &stderr,
		nodeSearchTestDependencies(t, stubNodeSearchCatalog{get: func(name string) (nodeindex.PackageDetail, error) {
			return nodeindex.Detail(nodeindex.Index{}, nodeindex.Runtime{Version: "v0.3.0", NodeAPI: nodeindex.APIVersion}, name)
		}}))
	want := "{\"code\":\"NODE_PACKAGE_NOT_FOUND\",\"message\":\"未找到节点包\"}\n"
	if code != 1 || stdout.Len() != 0 || stderr.String() != want {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func nodeSearchTestDependencies(t *testing.T, catalog nodeSearchCatalog) nodeSearchDependencies {
	t.Helper()
	return nodeSearchDependencies{
		configuredCacheDir: func() string { return t.TempDir() },
		resolveCacheDir:    nodeindex.ResolveCacheDir,
		runtime:            func() nodeindex.Runtime { return nodeindex.Runtime{Version: "v0.3.0", NodeAPI: nodeindex.APIVersion} },
		openCatalog:        func(string, nodeindex.Runtime) (nodeSearchCatalog, error) { return catalog, nil },
	}
}

func searchOutputFixture() nodeindex.SearchResult {
	version := nodeindex.VersionSummary{
		Version:       "v1.2.0",
		Source:        nodeindex.Source{Repository: "https://github.com/example/nodes", Tag: "v1.2.0", Commit: strings.Repeat("a", 40), ManifestDigest: "sha256:" + strings.Repeat("b", 64)},
		Lifecycle:     nodeindex.Lifecycle{Status: "active"},
		Compatibility: nodepackage.Compatibility{NodeAPI: nodeindex.APIVersion, Runtime: nodepackage.RuntimeRange{MinVersion: "v0.2.0", MaxVersionExclusive: "v0.4.0"}},
	}
	return nodeindex.SearchResult{Release: "v0.1.0", Total: 1, Limit: 50, Items: []nodeindex.PackageSummary{{
		Name: "github.com/example/nodes", DisplayName: "Example Nodes", Description: "Example package", License: "Apache-2.0",
		Repository: "https://github.com/example/nodes", Categories: []string{"integration"}, Keywords: []string{"echo"},
		RecommendedVersion: &version, Reasons: []nodeindex.Reason{},
	}}}
}

func infoOutputFixture() nodeindex.PackageDetail {
	makeVersion := func(version, lifecycle string) nodeindex.PackageVersion {
		commit := strings.Repeat("a", 40)
		digest := "sha256:" + strings.Repeat("b", 64)
		indexCommit := strings.Repeat("f", 40)
		if version == "v1.1.0" {
			commit = strings.Repeat("c", 40)
			digest = "sha256:" + strings.Repeat("d", 64)
			indexCommit = strings.Repeat("e", 40)
		}
		return nodeindex.PackageVersion{
			Version:   version,
			Source:    nodeindex.Source{Repository: "https://github.com/example/nodes", Tag: version, Commit: commit, ManifestDigest: digest},
			Review:    nodeindex.Review{Status: "approved", ReviewedAt: time.Date(2026, 8, 19, 1, 2, 3, 0, time.UTC), IndexCommit: indexCommit},
			Lifecycle: nodeindex.Lifecycle{Status: lifecycle, Message: map[bool]string{true: "compromised release"}[lifecycle == "withdrawn"]},
			Manifest: nodepackage.Manifest{
				APIVersion: nodepackage.APIVersion, Kind: nodepackage.Kind,
				Metadata:      nodepackage.Metadata{Name: "github.com/example/nodes", DisplayName: "Example Nodes", Description: "Example package", License: "Apache-2.0", Repository: "https://github.com/example/nodes"},
				Compatibility: nodepackage.Compatibility{NodeAPI: nodeindex.APIVersion, Runtime: nodepackage.RuntimeRange{MinVersion: "v0.2.0", MaxVersionExclusive: "v0.4.0"}},
				Registrations: []nodepackage.Registration{{Package: "github.com/example/nodes/register", Nodes: []nodepackage.NodeRef{{Type: "example.echo", Version: "1.0.0"}}}},
			},
		}
	}
	recommendedVersion := searchOutputFixture().Items[0].RecommendedVersion
	return nodeindex.PackageDetail{
		Name: "github.com/example/nodes", Categories: []string{"integration", "utility"}, Keywords: []string{"echo"},
		Versions:           []nodeindex.PackageVersion{makeVersion("v1.2.0", "active"), makeVersion("v1.1.0", "withdrawn")},
		RecommendedVersion: recommendedVersion, Reasons: []nodeindex.Reason{},
		Assessments: []nodeindex.VersionAssessment{{Version: "v1.2.0", Compatible: true, Reasons: []nodeindex.Reason{}}, {Version: "v1.1.0", Compatible: false, Reasons: []nodeindex.Reason{nodeindex.ReasonNoActiveStableVersion}}},
	}
}
