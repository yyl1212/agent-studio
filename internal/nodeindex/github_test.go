package nodeindex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestGitHubSourceUsesFixedAnonymousEndpointAndAssetAPI(t *testing.T) {
	asset := []byte(`{"index":"verified"}`)
	transport := &scriptedTransport{responses: []*http.Response{
		jsonResponse(http.StatusOK, validReleaseJSON(t, asset)),
		binaryResponse(http.StatusOK, asset),
	}}
	client := &http.Client{Transport: transport}
	source := NewGitHubSource(client)
	release, err := source.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if release.Tag != "v0.1.0" || release.Draft || release.Prerelease || !release.Immutable || release.Asset.Size != int64(len(asset)) {
		t.Fatalf("release=%+v", release)
	}
	downloaded, err := source.Download(context.Background(), release.Asset)
	if err != nil {
		t.Fatal(err)
	}
	if string(downloaded) != string(asset) {
		t.Fatalf("downloaded=%q", downloaded)
	}
	if len(transport.requests) != 2 {
		t.Fatalf("requests=%d", len(transport.requests))
	}
	assertGitHubRequest(t, transport.requests[0], "https://api.github.com/repos/yyl1212/agent-studio-node-index/releases/latest", "application/vnd.github+json")
	assertGitHubRequest(t, transport.requests[1], "https://api.github.com/repos/yyl1212/agent-studio-node-index/releases/assets/123", "application/octet-stream")
}

func TestGitHubSourceRejectsInvalidReleaseAndAssetMetadata(t *testing.T) {
	asset := []byte("valid")
	tests := []struct {
		name   string
		status int
		mutate func(map[string]any)
		code   Code
	}{
		{name: "not found", status: http.StatusNotFound, code: CodeReleaseNotFound},
		{name: "forbidden", status: http.StatusForbidden, code: CodeRateLimited},
		{name: "too many requests", status: http.StatusTooManyRequests, code: CodeRateLimited},
		{name: "draft", status: http.StatusOK, mutate: func(release map[string]any) { release["draft"] = true }, code: CodeReleaseInvalid},
		{name: "prerelease", status: http.StatusOK, mutate: func(release map[string]any) { release["prerelease"] = true }, code: CodeReleaseInvalid},
		{name: "mutable", status: http.StatusOK, mutate: func(release map[string]any) { release["immutable"] = false }, code: CodeReleaseInvalid},
		{name: "unstable tag", status: http.StatusOK, mutate: func(release map[string]any) { release["tag_name"] = "v0.1.0-rc.1" }, code: CodeReleaseInvalid},
		{name: "missing index", status: http.StatusOK, mutate: func(release map[string]any) { release["assets"] = []any{} }, code: CodeAssetInvalid},
		{name: "duplicate index", status: http.StatusOK, mutate: func(release map[string]any) {
			assets := release["assets"].([]any)
			release["assets"] = append(assets, deepClone(t, assets[0].(map[string]any)))
		}, code: CodeAssetInvalid},
		{name: "asset pending", status: http.StatusOK, mutate: func(release map[string]any) { indexAssetOf(release)["state"] = "new" }, code: CodeAssetInvalid},
		{name: "asset empty", status: http.StatusOK, mutate: func(release map[string]any) { indexAssetOf(release)["size"] = 0 }, code: CodeAssetInvalid},
		{name: "asset oversized", status: http.StatusOK, mutate: func(release map[string]any) { indexAssetOf(release)["size"] = MaxIndexBytes + 1 }, code: CodeAssetInvalid},
		{name: "asset digest null", status: http.StatusOK, mutate: func(release map[string]any) { indexAssetOf(release)["digest"] = nil }, code: CodeAssetInvalid},
		{name: "asset digest invalid", status: http.StatusOK, mutate: func(release map[string]any) { indexAssetOf(release)["digest"] = "sha256:ABC" }, code: CodeAssetInvalid},
		{name: "asset http", status: http.StatusOK, mutate: func(release map[string]any) {
			indexAssetOf(release)["url"] = "http://api.github.com/repos/yyl1212/agent-studio-node-index/releases/assets/123"
		}, code: CodeAssetInvalid},
		{name: "asset wrong host", status: http.StatusOK, mutate: func(release map[string]any) {
			indexAssetOf(release)["url"] = "https://evil.example/repos/yyl1212/agent-studio-node-index/releases/assets/123"
		}, code: CodeAssetInvalid},
		{name: "asset wrong path", status: http.StatusOK, mutate: func(release map[string]any) {
			indexAssetOf(release)["url"] = "https://api.github.com/repos/other/repo/releases/assets/123"
		}, code: CodeAssetInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status := test.status
			if status == 0 {
				status = http.StatusOK
			}
			release := validReleaseObject(asset)
			if test.mutate != nil {
				test.mutate(release)
			}
			raw, err := json.Marshal(release)
			if err != nil {
				t.Fatal(err)
			}
			source := NewGitHubSource(&http.Client{Transport: &scriptedTransport{responses: []*http.Response{jsonResponse(status, raw)}}})
			_, err = source.Latest(context.Background())
			if CodeOf(err) != test.code {
				t.Fatalf("err=%v code=%q want=%q", err, CodeOf(err), test.code)
			}
		})
	}
}

func TestGitHubSourceBoundsReleaseResponsesAndRedactsBodies(t *testing.T) {
	tests := []struct {
		name     string
		response *http.Response
	}{
		{name: "malformed", response: jsonResponse(http.StatusOK, []byte(`{"token":"Bearer SECRET"`))},
		{name: "oversized body", response: jsonResponse(http.StatusOK, []byte(strings.Repeat("x", 256<<10+1)))},
		{name: "oversized headers", response: responseWithLargeHeader(validReleaseJSON(t, []byte("valid")))},
		{name: "upstream failure", response: jsonResponse(http.StatusInternalServerError, []byte("Bearer SECRET upstream"))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := NewGitHubSource(&http.Client{Transport: &scriptedTransport{responses: []*http.Response{test.response}}})
			_, err := source.Latest(context.Background())
			if CodeOf(err) != CodeReleaseInvalid || strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "Bearer") {
				t.Fatalf("err=%v code=%q", err, CodeOf(err))
			}
		})
	}
}

func TestGitHubSourceDownloadValidatesSizeDigestAndStatus(t *testing.T) {
	asset := []byte("verified index")
	sum := sha256.Sum256(asset)
	valid := ReleaseAsset{URL: "https://api.github.com/repos/yyl1212/agent-studio-node-index/releases/assets/123", Size: int64(len(asset)), Digest: "sha256:" + hex.EncodeToString(sum[:])}
	tests := []struct {
		name     string
		asset    ReleaseAsset
		response *http.Response
		code     Code
	}{
		{name: "rate limited", asset: valid, response: binaryResponse(http.StatusTooManyRequests, []byte("Bearer SECRET")), code: CodeRateLimited},
		{name: "upstream failure", asset: valid, response: binaryResponse(http.StatusBadGateway, []byte("Bearer SECRET")), code: CodeAssetInvalid},
		{name: "declared size mismatch", asset: valid, response: binaryResponse(http.StatusOK, append(asset, '!')), code: CodeDigestMismatch},
		{name: "digest mismatch", asset: ReleaseAsset{URL: valid.URL, Size: valid.Size, Digest: "sha256:" + strings.Repeat("0", 64)}, response: binaryResponse(http.StatusOK, asset), code: CodeDigestMismatch},
		{name: "response oversized", asset: ReleaseAsset{URL: valid.URL, Size: MaxIndexBytes, Digest: valid.Digest}, response: binaryResponse(http.StatusOK, []byte(strings.Repeat("x", MaxIndexBytes+1))), code: CodeDigestMismatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := NewGitHubSource(&http.Client{Transport: &scriptedTransport{responses: []*http.Response{test.response}}})
			_, err := source.Download(context.Background(), test.asset)
			if CodeOf(err) != test.code || strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "Bearer") {
				t.Fatalf("err=%v code=%q want=%q", err, CodeOf(err), test.code)
			}
		})
	}
}

func TestGitHubSourceAllowsOnlyPinnedRedirectHosts(t *testing.T) {
	asset := []byte("verified index")
	sum := sha256.Sum256(asset)
	metadata := ReleaseAsset{URL: "https://api.github.com/repos/yyl1212/agent-studio-node-index/releases/assets/123", Size: int64(len(asset)), Digest: "sha256:" + hex.EncodeToString(sum[:])}

	allowed := &scriptedTransport{responses: []*http.Response{
		redirectResponse("https://release-assets.githubusercontent.com/object"),
		binaryResponse(http.StatusOK, asset),
	}}
	if _, err := NewGitHubSource(&http.Client{Transport: allowed}).Download(context.Background(), metadata); err != nil {
		t.Fatal(err)
	}
	if len(allowed.requests) != 2 || allowed.requests[1].URL.Host != "release-assets.githubusercontent.com" {
		t.Fatalf("requests=%v", requestURLs(allowed.requests))
	}

	blocked := &scriptedTransport{responses: []*http.Response{redirectResponse("https://evil.example/object")}}
	if _, err := NewGitHubSource(&http.Client{Transport: blocked}).Download(context.Background(), metadata); CodeOf(err) != CodeAssetInvalid {
		t.Fatalf("blocked redirect err=%v code=%q", err, CodeOf(err))
	}
}

func TestGitHubSourcePreservesContextAndDoesNotMutateClient(t *testing.T) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxResponseHeaderBytes = 1234
	originalRedirect := func(_ *http.Request, _ []*http.Request) error { return errors.New("original") }
	client := &http.Client{Transport: transport, Timeout: 7 * time.Second, CheckRedirect: originalRedirect}
	source := NewGitHubSource(client)
	if client.Timeout != 7*time.Second || client.Transport != transport || transport.MaxResponseHeaderBytes != 1234 || client.CheckRedirect == nil {
		t.Fatal("constructor mutated caller client")
	}
	if source.client == client || source.client.Transport == transport || source.client.Timeout != 7*time.Second || source.client.Jar != nil {
		t.Fatalf("source client was not isolated: %+v", source.client)
	}
	if got := NewGitHubSource(&http.Client{}).client.Timeout; got != 30*time.Second {
		t.Fatalf("default timeout=%s", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := source.Latest(ctx)
	if !errors.Is(err, context.Canceled) || CodeOf(err) != "" {
		t.Fatalf("canceled err=%v code=%q", err, CodeOf(err))
	}

	timedOut := NewGitHubSource(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})})
	_, err = timedOut.Latest(context.Background())
	if CodeOf(err) != CodeReleaseInvalid {
		t.Fatalf("client timeout err=%v code=%q", err, CodeOf(err))
	}
}

type scriptedTransport struct {
	responses []*http.Response
	requests  []*http.Request
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func (transport *scriptedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.requests = append(transport.requests, request.Clone(request.Context()))
	if err := request.Context().Err(); err != nil {
		return nil, err
	}
	if len(transport.responses) == 0 {
		return nil, errors.New("unexpected request")
	}
	response := transport.responses[0]
	transport.responses = transport.responses[1:]
	response.Request = request
	return response, nil
}

func validReleaseJSON(t *testing.T, asset []byte) []byte {
	t.Helper()
	raw, err := json.Marshal(validReleaseObject(asset))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func validReleaseObject(asset []byte) map[string]any {
	sum := sha256.Sum256(asset)
	return map[string]any{
		"tag_name":   "v0.1.0",
		"draft":      false,
		"prerelease": false,
		"immutable":  true,
		"assets": []any{
			map[string]any{
				"url":    "https://api.github.com/repos/yyl1212/agent-studio-node-index/releases/assets/123",
				"name":   "index.json",
				"state":  "uploaded",
				"size":   len(asset),
				"digest": "sha256:" + hex.EncodeToString(sum[:]),
			},
			map[string]any{"url": "https://api.github.com/repos/yyl1212/agent-studio-node-index/releases/assets/124", "name": "checksums.txt", "state": "uploaded", "size": 10, "digest": "sha256:" + strings.Repeat("1", 64)},
		},
	}
}

func indexAssetOf(release map[string]any) map[string]any {
	return release["assets"].([]any)[0].(map[string]any)
}

func jsonResponse(status int, body []byte) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(body)))}
}

func binaryResponse(status int, body []byte) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}
}

func redirectResponse(location string) *http.Response {
	return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{location}}, Body: io.NopCloser(strings.NewReader(""))}
}

func responseWithLargeHeader(body []byte) *http.Response {
	response := jsonResponse(http.StatusOK, body)
	response.Header.Set("X-Oversized", strings.Repeat("x", 64<<10))
	return response
}

func assertGitHubRequest(t *testing.T, request *http.Request, wantURL, wantAccept string) {
	t.Helper()
	if request.Method != http.MethodGet || request.URL.String() != wantURL || request.Header.Get("Accept") != wantAccept ||
		request.Header.Get("X-GitHub-Api-Version") != "2026-03-10" || request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
		t.Fatalf("request=%s %s headers=%v", request.Method, request.URL, request.Header)
	}
}

func requestURLs(requests []*http.Request) []string {
	result := make([]string, len(requests))
	for index := range requests {
		result[index] = requests[index].URL.String()
	}
	return result
}
