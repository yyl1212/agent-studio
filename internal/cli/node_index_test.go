package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/internal/nodeindex"
)

type stubIndexCatalog struct {
	status nodeindex.Status
}

func (catalog stubIndexCatalog) Status() nodeindex.Status {
	return catalog.status
}

func TestNodeIndexStatusJSONIsStableAndOmitsCachePath(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "node-index")
	refreshCalls := 0
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runNodeIndex(context.Background(), []string{"status", "--json"}, &stdout, &stderr, nodeIndexDependencies{
		configuredCacheDir: func() string { return cacheDir },
		resolveCacheDir:    nodeindex.ResolveCacheDir,
		runtime: func() nodeindex.Runtime {
			return nodeindex.Runtime{Version: "0.2.0-dev", NodeAPI: nodeindex.APIVersion}
		},
		openCatalog: func(gotDir string, runtime nodeindex.Runtime) (indexStatusCatalog, error) {
			if gotDir != cacheDir || runtime.Version != "0.2.0-dev" || runtime.NodeAPI != nodeindex.APIVersion {
				t.Fatalf("dir=%q runtime=%+v", gotDir, runtime)
			}
			warning := nodeindex.CodeEmbeddedSnapshot
			return stubIndexCatalog{status: nodeindex.Status{
				Source: nodeindex.SourceEmbedded, Release: "v0.1.0",
				GeneratedAt:  time.Date(2026, 8, 20, 16, 44, 9, 0, time.UTC),
				PackageCount: 2, CompatiblePackageCount: 1,
				RuntimeVersion: "0.2.0-dev", NodeAPI: nodeindex.APIVersion,
				Stale: true, WarningCode: &warning,
			}}, nil
		},
		refresh: func(context.Context, string) (nodeindex.RefreshResult, error) {
			refreshCalls++
			return nodeindex.RefreshResult{}, nil
		},
	})
	want := "{\"source\":\"embedded\",\"release\":\"v0.1.0\",\"generatedAt\":\"2026-08-20T16:44:09Z\",\"packageCount\":2,\"compatiblePackageCount\":1,\"runtimeVersion\":\"0.2.0-dev\",\"nodeAPI\":\"agent-studio.dev/v1alpha1\",\"stale\":true,\"warningCode\":\"INDEX_EMBEDDED_SNAPSHOT\"}\n"
	if code != 0 || stdout.String() != want || stderr.Len() != 0 || refreshCalls != 0 || strings.Contains(stdout.String(), cacheDir) {
		t.Fatalf("code=%d stdout=%q stderr=%q refresh=%d", code, stdout.String(), stderr.String(), refreshCalls)
	}
}

func TestNodeIndexStatusHumanIncludesWarningAndCachePath(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "node-index")
	warning := nodeindex.CodeContentInvalid
	var stdout bytes.Buffer
	code := runNodeIndex(context.Background(), []string{"status"}, &stdout, io.Discard, nodeIndexDependencies{
		configuredCacheDir: func() string { return cacheDir },
		resolveCacheDir:    nodeindex.ResolveCacheDir,
		runtime:            func() nodeindex.Runtime { return nodeindex.Runtime{Version: "v0.3.0", NodeAPI: nodeindex.APIVersion} },
		openCatalog: func(string, nodeindex.Runtime) (indexStatusCatalog, error) {
			return stubIndexCatalog{status: nodeindex.Status{
				Source: nodeindex.SourceCache, Release: "v0.2.0",
				GeneratedAt:  time.Date(2026, 8, 21, 1, 2, 3, 0, time.UTC),
				PackageCount: 3, CompatiblePackageCount: 2,
				RuntimeVersion: "v0.3.0", NodeAPI: nodeindex.APIVersion,
				Stale: true, WarningCode: &warning,
			}}, nil
		},
	})
	want := "source: cache\nrelease: v0.2.0\ngenerated: 2026-08-21T01:02:03Z\npackages: 3 (compatible: 2)\nwarning: INDEX_CONTENT_INVALID\ncache: " + filepath.Join(cacheDir, "index.json") + "\n"
	if code != 0 || stdout.String() != want {
		t.Fatalf("code=%d stdout=%q", code, stdout.String())
	}
}

func TestNodeIndexStatusHumanEscapesCachePathControls(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "node\nindex")
	var stdout bytes.Buffer
	code := runNodeIndex(context.Background(), []string{"status"}, &stdout, io.Discard, nodeIndexDependencies{
		configuredCacheDir: func() string { return cacheDir },
		resolveCacheDir:    nodeindex.ResolveCacheDir,
		runtime:            func() nodeindex.Runtime { return nodeindex.Runtime{Version: "v0.3.0", NodeAPI: nodeindex.APIVersion} },
		openCatalog: func(string, nodeindex.Runtime) (indexStatusCatalog, error) {
			return stubIndexCatalog{status: nodeindex.Status{Source: nodeindex.SourceCache, Release: "v0.1.0", GeneratedAt: time.Unix(0, 0).UTC()}}, nil
		},
	})
	if code != 0 || strings.Contains(stdout.String(), "node\nindex") || !strings.Contains(stdout.String(), `node\nindex`) {
		t.Fatalf("code=%d stdout=%q", code, stdout.String())
	}
}

func TestNodeIndexRefreshOutputsUpdatedAndReopensCatalog(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "node-index")
	openCalls := 0
	var stdout bytes.Buffer
	code := runNodeIndex(context.Background(), []string{"refresh"}, &stdout, io.Discard, nodeIndexDependencies{
		configuredCacheDir: func() string { return cacheDir },
		resolveCacheDir:    nodeindex.ResolveCacheDir,
		runtime:            func() nodeindex.Runtime { return nodeindex.Runtime{Version: "v0.3.0", NodeAPI: nodeindex.APIVersion} },
		openCatalog: func(string, nodeindex.Runtime) (indexStatusCatalog, error) {
			openCalls++
			return stubIndexCatalog{status: nodeindex.Status{Source: nodeindex.SourceCache, Release: "v0.2.0"}}, nil
		},
		refresh: func(ctx context.Context, gotDir string) (nodeindex.RefreshResult, error) {
			if ctx == nil || gotDir != cacheDir {
				t.Fatalf("ctx=%v dir=%q", ctx, gotDir)
			}
			return nodeindex.RefreshResult{Status: nodeindex.RefreshUpdated, PreviousRelease: "v0.1.0", Release: "v0.2.0"}, nil
		},
	})
	if code != 0 || stdout.String() != "updated v0.1.0 -> v0.2.0\n" || openCalls != 1 {
		t.Fatalf("code=%d stdout=%q openCalls=%d", code, stdout.String(), openCalls)
	}
}

func TestNodeIndexRefreshRejectsInactiveFinalSnapshot(t *testing.T) {
	warning := nodeindex.CodeContentInvalid
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	dependencies := validNodeIndexDependencies(t, func(context.Context, string) (nodeindex.RefreshResult, error) {
		return nodeindex.RefreshResult{Status: nodeindex.RefreshUpdated, PreviousRelease: "v0.1.0", Release: "v0.1.0"}, nil
	})
	dependencies.openCatalog = func(string, nodeindex.Runtime) (indexStatusCatalog, error) {
		return stubIndexCatalog{status: nodeindex.Status{Source: nodeindex.SourceEmbedded, Release: "v0.1.0", WarningCode: &warning}}, nil
	}
	code := runNodeIndex(context.Background(), []string{"refresh", "--json"}, &stdout, &stderr, dependencies)
	if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), string(nodeindex.CodeContentInvalid)) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestNodeIndexRefreshAlreadyCurrentJSON(t *testing.T) {
	var stdout bytes.Buffer
	code := runNodeIndex(context.Background(), []string{"refresh", "--json"}, &stdout, io.Discard, validNodeIndexDependencies(t, func(context.Context, string) (nodeindex.RefreshResult, error) {
		return nodeindex.RefreshResult{Status: nodeindex.RefreshAlreadyCurrent, PreviousRelease: "v0.2.0", Release: "v0.2.0"}, nil
	}))
	want := "{\"status\":\"already-current\",\"previousRelease\":\"v0.2.0\",\"release\":\"v0.2.0\"}\n"
	if code != 0 || stdout.String() != want {
		t.Fatalf("code=%d stdout=%q", code, stdout.String())
	}
}

func TestNodeIndexRateLimitJSONIsSafe(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "Bearer-secret")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runNodeIndex(context.Background(), []string{"refresh", "--json"}, &stdout, &stderr, validNodeIndexDependencies(t, func(context.Context, string) (nodeindex.RefreshResult, error) {
		return nodeindex.RefreshResult{}, errors.New("INDEX_RATE_LIMITED: " + secret)
	}))
	if code != 1 || stdout.Len() != 0 || strings.Contains(stderr.String(), secret) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestNodeIndexCodedRateLimitJSON(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runNodeIndex(context.Background(), []string{"refresh", "--json"}, &stdout, &stderr, validNodeIndexDependencies(t, func(context.Context, string) (nodeindex.RefreshResult, error) {
		source := nodeindex.NewGitHubSource(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusTooManyRequests, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("Bearer secret"))}, nil
		})})
		_, err := source.Latest(context.Background())
		return nodeindex.RefreshResult{}, err
	}))
	want := "{\"code\":\"INDEX_RATE_LIMITED\",\"message\":\"GitHub 请求受到限流，请稍后重试\"}\n"
	if code != 1 || stdout.Len() != 0 || stderr.String() != want {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestNodeIndexRejectsInvalidSyntax(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"status", "--all"},
		{"status", "--json", "extra"},
		{"refresh", "extra"},
		{"missing"},
	} {
		var stderr bytes.Buffer
		code := runNodeIndex(context.Background(), args, io.Discard, &stderr, nodeIndexDependencies{})
		if code != 2 || stderr.String() != "node index usage: node index <status|refresh> [--json]\n" {
			t.Fatalf("args=%v code=%d stderr=%q", args, code, stderr.String())
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func validNodeIndexDependencies(t *testing.T, refresh func(context.Context, string) (nodeindex.RefreshResult, error)) nodeIndexDependencies {
	t.Helper()
	cacheDir := filepath.Join(t.TempDir(), "node-index")
	return nodeIndexDependencies{
		configuredCacheDir: func() string { return cacheDir },
		resolveCacheDir:    nodeindex.ResolveCacheDir,
		runtime:            func() nodeindex.Runtime { return nodeindex.Runtime{Version: "v0.3.0", NodeAPI: nodeindex.APIVersion} },
		openCatalog: func(string, nodeindex.Runtime) (indexStatusCatalog, error) {
			return stubIndexCatalog{status: nodeindex.Status{Source: nodeindex.SourceCache, Release: "v0.2.0"}}, nil
		},
		refresh: refresh,
	}
}
