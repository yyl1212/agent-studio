package nodeindex

import (
	"slices"
	"strings"
	"testing"
)

func TestSearchRankingFilteringAndPagination(t *testing.T) {
	index := searchFixture(t)
	got, err := Search(index, fixtureRuntime(), Query{
		Text: "search", Categories: []string{"integration"}, CompatibleOnly: true, Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Release != "v0.2.0" || got.Total != 3 || got.Offset != 0 || got.Limit != 2 || len(got.Items) != 2 {
		t.Fatalf("result=%+v", got)
	}
	if got.Items[0].Name != "github.com/example/search" || got.Items[1].Name != "github.com/example/toolkit" {
		t.Fatalf("ranked names=%v", summaryNames(got.Items))
	}

	second, err := Search(index, fixtureRuntime(), Query{Text: "search", Categories: []string{"integration"}, CompatibleOnly: true, Limit: 2, Offset: 2})
	if err != nil {
		t.Fatal(err)
	}
	if second.Total != 3 || len(second.Items) != 1 || second.Items[0].Name != "github.com/example/workflow" {
		t.Fatalf("second page=%+v", second)
	}
}

func TestSearchUsesExactPrefixKeywordAndContainsRanks(t *testing.T) {
	index := searchFixture(t)
	tests := []struct {
		query string
		want  string
	}{
		{query: "github.com/example/search", want: "github.com/example/search"},
		{query: "sea", want: "github.com/example/search"},
		{query: "search", want: "github.com/example/search"},
		{query: "workflow", want: "github.com/example/workflow"},
	}
	for _, test := range tests {
		t.Run(test.query, func(t *testing.T) {
			got, err := Search(index, fixtureRuntime(), Query{Text: test.query, CompatibleOnly: true, Limit: 50})
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Items) == 0 || got.Items[0].Name != test.want {
				t.Fatalf("items=%v", summaryNames(got.Items))
			}
		})
	}
}

func TestSearchRequiresEveryTokenAndUsesCategoryOR(t *testing.T) {
	index := searchFixture(t)
	got, err := Search(index, fixtureRuntime(), Query{Text: "search http", CompatibleOnly: true, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(summaryNames(got.Items), []string{"github.com/example/search"}) {
		t.Fatalf("items=%v", summaryNames(got.Items))
	}

	got, err = Search(index, fixtureRuntime(), Query{Categories: []string{"data", "utility"}, CompatibleOnly: true, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(summaryNames(got.Items), []string{"github.com/example/toolkit", "github.com/example/unicode"}) {
		t.Fatalf("category OR items=%v", summaryNames(got.Items))
	}
}

func TestSearchNormalizesUnicodeAndBreaksTiesByModuleName(t *testing.T) {
	index := searchFixture(t)
	got, err := Search(index, fixtureRuntime(), Query{Text: "数据", CompatibleOnly: true, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(summaryNames(got.Items), []string{"github.com/example/unicode", "github.com/example/workflow"}) {
		t.Fatalf("unicode items=%v", summaryNames(got.Items))
	}

	got, err = Search(index, fixtureRuntime(), Query{Text: "tie", CompatibleOnly: true, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(summaryNames(got.Items), []string{"github.com/example/tie-a", "github.com/example/tie-b"}) {
		t.Fatalf("tie items=%v", summaryNames(got.Items))
	}
}

func TestSearchAllIncludesIncompatibleButAlwaysHidesWithdrawnOnly(t *testing.T) {
	index := searchFixture(t)
	compatible, err := Search(index, fixtureRuntime(), Query{CompatibleOnly: true, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	all, err := Search(index, fixtureRuntime(), Query{CompatibleOnly: false, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(summaryNames(compatible.Items), "github.com/example/incompatible") ||
		!slices.Contains(summaryNames(all.Items), "github.com/example/incompatible") {
		t.Fatalf("compatible=%v all=%v", summaryNames(compatible.Items), summaryNames(all.Items))
	}
	if slices.Contains(summaryNames(all.Items), "github.com/example/withdrawn") {
		t.Fatalf("withdrawn-only package leaked into search: %v", summaryNames(all.Items))
	}
	for _, item := range all.Items {
		if item.Name == "github.com/example/incompatible" && (item.RecommendedVersion != nil || !slices.Equal(item.Reasons, []Reason{ReasonRuntimeTooOld})) {
			t.Fatalf("incompatible summary=%+v", item)
		}
	}
}

func TestSearchRejectsInvalidQueryBudgets(t *testing.T) {
	index := searchFixture(t)
	for _, query := range []Query{
		{Text: strings.Repeat("界", 129), CompatibleOnly: true, Limit: 50},
		{CompatibleOnly: true, Limit: 0},
		{CompatibleOnly: true, Limit: 101},
		{CompatibleOnly: true, Limit: 50, Offset: -1},
		{CompatibleOnly: true, Limit: 50, Offset: 10001},
	} {
		if _, err := Search(index, fixtureRuntime(), query); CodeOf(err) != CodeContentInvalid {
			t.Fatalf("query=%+v err=%v code=%q", query, err, CodeOf(err))
		}
	}
}

func TestDetailReturnsWithdrawnHistoryAndAssessments(t *testing.T) {
	index := searchFixture(t)
	detail, err := Detail(index, fixtureRuntime(), "github.com/example/search")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Name != "github.com/example/search" || len(detail.Versions) != 2 || len(detail.Assessments) != 2 {
		t.Fatalf("detail=%+v", detail)
	}
	if detail.RecommendedVersion == nil || detail.RecommendedVersion.Version != "v1.0.0" {
		t.Fatalf("recommendation=%+v", detail.RecommendedVersion)
	}
	if detail.Versions[1].Lifecycle.Status != "withdrawn" || detail.Assessments[1].Compatible ||
		!slices.Equal(detail.Assessments[1].Reasons, []Reason{ReasonNoActiveStableVersion}) {
		t.Fatalf("withdrawn detail=%+v assessment=%+v", detail.Versions[1], detail.Assessments[1])
	}
	if _, err := Detail(index, fixtureRuntime(), "github.com/example/missing"); CodeOf(err) != CodeNotFound {
		t.Fatalf("not found err=%v code=%q", err, CodeOf(err))
	}
	if _, err := Detail(index, fixtureRuntime(), "../invalid"); CodeOf(err) != CodeContentInvalid {
		t.Fatalf("invalid name err=%v code=%q", err, CodeOf(err))
	}
}

func TestSearchAndDetailReturnDeepCopies(t *testing.T) {
	index := searchFixture(t)
	search, err := Search(index, fixtureRuntime(), Query{Text: "search", CompatibleOnly: true, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	search.Items[0].Categories[0] = "changed"
	search.Items[0].Keywords[0] = "changed"
	search.Items[0].RecommendedVersion.Source.Repository = "https://github.com/changed/value"
	if index.Packages[indexPackagePosition(index, "github.com/example/search")].Categories[0] == "changed" {
		t.Fatal("search result mutated index categories")
	}

	detail, err := Detail(index, fixtureRuntime(), "github.com/example/search")
	if err != nil {
		t.Fatal(err)
	}
	detail.Versions[0].Manifest.Registrations[0].Nodes[0].Type = "changed.node"
	position := indexPackagePosition(index, "github.com/example/search")
	if index.Packages[position].Versions[0].Manifest.Registrations[0].Nodes[0].Type == "changed.node" {
		t.Fatal("detail mutated index manifest")
	}
}

func searchFixture(t *testing.T) Index {
	t.Helper()
	base, err := Parse("valid.json", readFixture(t, "valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	makePackage := func(name, display, description string, categories, keywords []string, nodeType, minimum, lifecycle string) Package {
		version := recommendationVersion(t, "v1.0.0", "approved", lifecycle, APIVersion, minimum, "v0.4.0")
		version.Source.Repository = "https://github.com/example/" + strings.TrimPrefix(name, "github.com/example/")
		version.Manifest.Metadata.Name = name
		version.Manifest.Metadata.DisplayName = display
		version.Manifest.Metadata.Description = description
		version.Manifest.Metadata.Repository = version.Source.Repository
		version.Manifest.Registrations[0].Package = name + "/nodes"
		version.Manifest.Registrations[0].Nodes[0].Type = nodeType
		return Package{Name: name, Categories: append([]string(nil), categories...), Keywords: append([]string(nil), keywords...), Versions: []PackageVersion{version}}
	}

	search := makePackage("github.com/example/search", "Search Package", "HTTP search integration", []string{"integration", "search"}, []string{"http", "search"}, "example.search", "v0.2.0", "active")
	withdrawn := clonePackageVersion(search.Versions[0])
	withdrawn.Version = "v1.1.0"
	withdrawn.Lifecycle = Lifecycle{Status: "withdrawn", Message: "upstream release withdrawn"}
	search.Versions = append(search.Versions, withdrawn)

	incompatible := makePackage("github.com/example/incompatible", "Future Package", "Requires a future runtime", []string{"future"}, []string{"future"}, "example.future", "v0.4.0", "active")
	incompatible.Versions[0].Manifest.Compatibility.Runtime.MaxVersionExclusive = "v0.5.0"
	packages := []Package{
		makePackage("github.com/example/workflow", "Workflow Helper", "Search and 数据 flow helpers", []string{"integration"}, []string{"workflow"}, "example.workflow", "v0.2.0", "active"),
		makePackage("github.com/example/toolkit", "Toolkit", "General integration tools", []string{"integration", "utility"}, []string{"search", "tools"}, "example.toolkit", "v0.2.0", "active"),
		search,
		makePackage("github.com/example/unicode", "数据搜索", "中文数据工具", []string{"data"}, []string{"数据"}, "example.unicode", "v0.2.0", "active"),
		makePackage("github.com/example/tie-b", "Tie Package", "Same score", []string{"testing"}, []string{"tie"}, "example.tie-b", "v0.2.0", "active"),
		makePackage("github.com/example/tie-a", "Tie Package", "Same score", []string{"testing"}, []string{"tie"}, "example.tie-a", "v0.2.0", "active"),
		incompatible,
		makePackage("github.com/example/withdrawn", "Withdrawn Package", "No longer listed", []string{"removed"}, []string{"removed"}, "example.withdrawn", "v0.2.0", "withdrawn"),
	}
	base.Packages = packages
	return base
}

func fixtureRuntime() Runtime {
	return Runtime{Version: "0.3.0-dev", NodeAPI: APIVersion}
}

func summaryNames(items []PackageSummary) []string {
	names := make([]string, len(items))
	for index := range items {
		names[index] = items[index].Name
	}
	return names
}

func indexPackagePosition(index Index, name string) int {
	for position := range index.Packages {
		if index.Packages[position].Name == name {
			return position
		}
	}
	return -1
}
