package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/internal/nodeindex"
)

type fixtureNodePackageCatalog struct {
	status    nodeindex.Status
	result    nodeindex.SearchResult
	detail    nodeindex.PackageDetail
	searchErr error
	detailErr error
	queries   []nodeindex.Query
	names     []string
}

func (catalog *fixtureNodePackageCatalog) Status() nodeindex.Status {
	return catalog.status
}

func (catalog *fixtureNodePackageCatalog) Search(query nodeindex.Query) (nodeindex.SearchResult, error) {
	catalog.queries = append(catalog.queries, query)
	return catalog.result, catalog.searchErr
}

func (catalog *fixtureNodePackageCatalog) Get(name string) (nodeindex.PackageDetail, error) {
	catalog.names = append(catalog.names, name)
	return catalog.detail, catalog.detailErr
}

func TestNodeIndexRoutesAreReadOnlyAndStable(t *testing.T) {
	generatedAt := time.Date(2026, 8, 20, 1, 2, 3, 0, time.UTC)
	warning := nodeindex.CodeEmbeddedSnapshot
	catalog := &fixtureNodePackageCatalog{
		status: nodeindex.Status{
			Source: nodeindex.SourceEmbedded, Release: "v0.3.0", GeneratedAt: generatedAt,
			PackageCount: 2, CompatiblePackageCount: 1, RuntimeVersion: "v0.3.0",
			NodeAPI: "agent-studio.dev/v1alpha1", Stale: true, WarningCode: &warning,
		},
		result: nodeindex.SearchResult{Release: "v0.3.0", Total: 0, Offset: 20, Limit: 10, Items: []nodeindex.PackageSummary{}},
		detail: nodeindex.PackageDetail{
			Name: "github.com/example/nodes", Categories: []string{}, Keywords: []string{},
			Versions: []nodeindex.PackageVersion{}, Reasons: []nodeindex.Reason{}, Assessments: []nodeindex.VersionAssessment{},
		},
	}
	dependencies := fixtureDeps()
	dependencies.NodePackages = catalog
	router := NewRouter(dependencies)

	statusRecorder := performRequest(router, http.MethodGet, "/api/node-index/status", "")
	listRecorder := performRequest(router, http.MethodGet, "/api/node-packages?q=search&category=integration&category=file&compatible=false&limit=10&offset=20", "")
	detailRecorder := performRequest(router, http.MethodGet, "/api/node-package?name=github.com%2Fexample%2Fnodes", "")
	if statusRecorder.Code != http.StatusOK || listRecorder.Code != http.StatusOK || detailRecorder.Code != http.StatusOK {
		t.Fatalf("status=%d list=%d detail=%d", statusRecorder.Code, listRecorder.Code, detailRecorder.Code)
	}
	var gotStatus nodeindex.Status
	if err := json.Unmarshal(statusRecorder.Body.Bytes(), &gotStatus); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotStatus, catalog.status) {
		t.Fatalf("status=%+v, want %+v", gotStatus, catalog.status)
	}
	wantQuery := nodeindex.Query{Text: "search", Categories: []string{"integration", "file"}, CompatibleOnly: false, Limit: 10, Offset: 20}
	if !reflect.DeepEqual(catalog.queries, []nodeindex.Query{wantQuery}) || !reflect.DeepEqual(catalog.names, []string{"github.com/example/nodes"}) {
		t.Fatalf("queries=%+v names=%+v", catalog.queries, catalog.names)
	}
	if !strings.Contains(listRecorder.Body.String(), `"items":[]`) || !strings.Contains(detailRecorder.Body.String(), `"versions":[]`) {
		t.Fatalf("list=%s detail=%s", listRecorder.Body.String(), detailRecorder.Body.String())
	}

	for _, path := range []string{"/api/node-index/refresh", "/api/node-packages/install"} {
		if got := performRequest(router, http.MethodPost, path, `{}`).Code; got != http.StatusNotFound {
			t.Fatalf("%s=%d", path, got)
		}
	}
}

func TestNodeIndexListUsesDocumentedDefaults(t *testing.T) {
	catalog := &fixtureNodePackageCatalog{result: nodeindex.SearchResult{Items: []nodeindex.PackageSummary{}}}
	dependencies := fixtureDeps()
	dependencies.NodePackages = catalog
	recorder := performRequest(NewRouter(dependencies), http.MethodGet, "/api/node-packages", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	want := nodeindex.Query{Categories: []string{}, CompatibleOnly: true, Limit: 50, Offset: 0}
	if !reflect.DeepEqual(catalog.queries, []nodeindex.Query{want}) {
		t.Fatalf("queries=%+v", catalog.queries)
	}
}

func TestNodeIndexRejectsInvalidQueriesBeforeCatalog(t *testing.T) {
	tooLong := strings.Repeat("界", nodeindex.MaxQueryLength+1)
	tests := []string{
		"?q=a&q=b",
		"?compatible=true&compatible=false",
		"?compatible=maybe",
		"?limit=1&limit=2",
		"?limit=0",
		"?limit=101",
		"?limit=invalid",
		"?offset=1&offset=2",
		"?offset=-1",
		"?offset=10001",
		"?offset=invalid",
		"?q=" + url.QueryEscape(tooLong),
		"?category=INTEGRATION",
		"?category=%20integration%20",
		"?category=invalid_category",
		"?category=" + strings.Repeat("a", nodeindex.MaxCategoryLength+1),
		"?category=a&category=b&category=c&category=d&category=e&category=f&category=g&category=h&category=i",
	}
	for _, query := range tests {
		t.Run(query, func(t *testing.T) {
			catalog := &fixtureNodePackageCatalog{}
			dependencies := fixtureDeps()
			dependencies.NodePackages = catalog
			recorder := performRequest(NewRouter(dependencies), http.MethodGet, "/api/node-packages"+query, "")
			assertJSONError(t, recorder, http.StatusBadRequest, "REQUEST_INVALID")
			if len(catalog.queries) != 0 {
				t.Fatalf("catalog received queries=%+v", catalog.queries)
			}
		})
	}
}

func TestNodeIndexRejectsInvalidDetailNamesBeforeCatalog(t *testing.T) {
	for _, query := range []string{"", "?name=", "?name=a&name=b"} {
		t.Run(query, func(t *testing.T) {
			catalog := &fixtureNodePackageCatalog{}
			dependencies := fixtureDeps()
			dependencies.NodePackages = catalog
			recorder := performRequest(NewRouter(dependencies), http.MethodGet, "/api/node-package"+query, "")
			assertJSONError(t, recorder, http.StatusBadRequest, "REQUEST_INVALID")
			if len(catalog.names) != 0 {
				t.Fatalf("catalog received names=%+v", catalog.names)
			}
		})
	}
}

func TestNodeIndexMapsCatalogErrorsWithoutLeaks(t *testing.T) {
	_, notFound := nodeindex.Detail(nodeindex.Index{}, nodeindex.Runtime{}, "github.com/example/missing")
	tests := []struct {
		name       string
		path       string
		searchErr  error
		detailErr  error
		wantStatus int
		wantCode   string
	}{
		{name: "not found", path: "/api/node-package?name=github.com%2Fexample%2Fmissing", detailErr: notFound, wantStatus: http.StatusNotFound, wantCode: "NODE_PACKAGE_NOT_FOUND"},
		{name: "search invalid", path: "/api/node-packages?category=invalid%20category", searchErr: nodeIndexContentInvalidError(), wantStatus: http.StatusBadRequest, wantCode: "REQUEST_INVALID"},
		{name: "internal", path: "/api/node-packages", searchErr: errors.New("private /absolute/path"), wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL_ERROR"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalog := &fixtureNodePackageCatalog{searchErr: test.searchErr, detailErr: test.detailErr}
			dependencies := fixtureDeps()
			dependencies.NodePackages = catalog
			recorder := performRequest(NewRouter(dependencies), http.MethodGet, test.path, "")
			assertJSONError(t, recorder, test.wantStatus, test.wantCode)
			if strings.Contains(recorder.Body.String(), "private") || strings.Contains(recorder.Body.String(), "/absolute/path") {
				t.Fatalf("error body leaked internals: %s", recorder.Body.String())
			}
		})
	}
}

func nodeIndexContentInvalidError() error {
	_, err := nodeindex.Search(nodeindex.Index{}, nodeindex.Runtime{}, nodeindex.Query{Limit: 0})
	return err
}
