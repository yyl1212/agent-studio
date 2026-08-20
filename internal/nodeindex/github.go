package nodeindex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yyl1212/agent-studio/internal/nodepackage"
	"golang.org/x/mod/semver"
)

const (
	latestReleaseURL      = "https://api.github.com/repos/yyl1212/agent-studio-node-index/releases/latest"
	assetAPIPathPrefix    = "/repos/yyl1212/agent-studio-node-index/releases/assets/"
	githubAPIVersion      = "2026-03-10"
	maxReleaseBodyBytes   = 256 << 10
	maxResponseHeaderSize = 64 << 10
	defaultGitHubTimeout  = 30 * time.Second
)

type ReleaseAsset struct {
	URL    string
	Size   int64
	Digest string
}

type Release struct {
	Tag        string
	Draft      bool
	Prerelease bool
	Immutable  bool
	Asset      ReleaseAsset
}

type ReleaseSource interface {
	Latest(context.Context) (Release, error)
	Download(context.Context, ReleaseAsset) ([]byte, error)
}

type GitHubSource struct {
	client *http.Client
}

type githubReleaseWire struct {
	TagName    *string            `json:"tag_name"`
	Draft      *bool              `json:"draft"`
	Prerelease *bool              `json:"prerelease"`
	Immutable  *bool              `json:"immutable"`
	Assets     *[]githubAssetWire `json:"assets"`
}

type githubAssetWire struct {
	URL    *string `json:"url"`
	Name   *string `json:"name"`
	State  *string `json:"state"`
	Size   *int64  `json:"size"`
	Digest *string `json:"digest"`
}

func NewGitHubSource(client *http.Client) *GitHubSource {
	if client == nil {
		client = &http.Client{}
	}
	isolated := *client
	if isolated.Timeout == 0 {
		isolated.Timeout = defaultGitHubTimeout
	}
	isolated.Jar = nil
	isolated.CheckRedirect = validateGitHubRedirect
	switch transport := isolated.Transport.(type) {
	case nil:
		cloned := http.DefaultTransport.(*http.Transport).Clone()
		cloned.MaxResponseHeaderBytes = maxResponseHeaderSize
		isolated.Transport = cloned
	case *http.Transport:
		cloned := transport.Clone()
		if cloned.MaxResponseHeaderBytes <= 0 || cloned.MaxResponseHeaderBytes > maxResponseHeaderSize {
			cloned.MaxResponseHeaderBytes = maxResponseHeaderSize
		}
		isolated.Transport = cloned
	}
	return &GitHubSource{client: &isolated}
}

func (source *GitHubSource) Latest(ctx context.Context) (Release, error) {
	response, err := source.get(ctx, latestReleaseURL, "application/vnd.github+json")
	if err != nil {
		if contextError(ctx, err) != nil {
			return Release{}, contextError(ctx, err)
		}
		return Release{}, coded(CodeReleaseInvalid, "node index release request failed", err)
	}
	defer response.Body.Close()
	if !headersWithinBudget(response.Header) {
		return Release{}, coded(CodeReleaseInvalid, "node index release response is invalid", nil)
	}
	switch response.StatusCode {
	case http.StatusNotFound:
		return Release{}, coded(CodeReleaseNotFound, "node index release was not found", nil)
	case http.StatusForbidden, http.StatusTooManyRequests:
		return Release{}, coded(CodeRateLimited, "node index release request was rate limited", nil)
	case http.StatusOK:
	default:
		return Release{}, coded(CodeReleaseInvalid, "node index release response is invalid", nil)
	}

	body, err := readBounded(response.Body, maxReleaseBodyBytes)
	if err != nil {
		if contextError(ctx, err) != nil {
			return Release{}, contextError(ctx, err)
		}
		return Release{}, coded(CodeReleaseInvalid, "node index release response is invalid", err)
	}
	var wire githubReleaseWire
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&wire); err != nil {
		return Release{}, coded(CodeReleaseInvalid, "node index release response is invalid", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Release{}, coded(CodeReleaseInvalid, "node index release response is invalid", err)
	}
	return releaseFromWire(wire)
}

func (source *GitHubSource) Download(ctx context.Context, asset ReleaseAsset) ([]byte, error) {
	if err := validateReleaseAsset(asset); err != nil {
		return nil, coded(CodeAssetInvalid, "node index asset metadata is invalid", err)
	}
	response, err := source.get(ctx, asset.URL, "application/octet-stream")
	if err != nil {
		if contextError(ctx, err) != nil {
			return nil, contextError(ctx, err)
		}
		return nil, coded(CodeAssetInvalid, "node index asset request failed", err)
	}
	defer response.Body.Close()
	if !headersWithinBudget(response.Header) {
		return nil, coded(CodeAssetInvalid, "node index asset response is invalid", nil)
	}
	switch response.StatusCode {
	case http.StatusForbidden, http.StatusTooManyRequests:
		return nil, coded(CodeRateLimited, "node index asset request was rate limited", nil)
	case http.StatusOK:
	default:
		return nil, coded(CodeAssetInvalid, "node index asset response is invalid", nil)
	}

	body, err := readBounded(response.Body, MaxIndexBytes)
	if err != nil {
		if contextError(ctx, err) != nil {
			return nil, contextError(ctx, err)
		}
		return nil, coded(CodeDigestMismatch, "node index asset digest does not match", err)
	}
	if int64(len(body)) != asset.Size {
		return nil, coded(CodeDigestMismatch, "node index asset digest does not match", nil)
	}
	sum := sha256.Sum256(body)
	if "sha256:"+hex.EncodeToString(sum[:]) != asset.Digest {
		return nil, coded(CodeDigestMismatch, "node index asset digest does not match", nil)
	}
	return body, nil
}

func (source *GitHubSource) get(ctx context.Context, endpoint, accept string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", accept)
	request.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	request.Header.Set("User-Agent", "agent-studio-node-index")
	request.Header.Del("Authorization")
	request.Header.Del("Cookie")
	return source.client.Do(request)
}

func releaseFromWire(wire githubReleaseWire) (Release, error) {
	if wire.TagName == nil || wire.Draft == nil || wire.Prerelease == nil || wire.Immutable == nil || wire.Assets == nil ||
		*wire.Draft || *wire.Prerelease || !*wire.Immutable ||
		utf8Length(*wire.TagName) > nodepackage.MaxVersionLength || !stableSemverPattern.MatchString(*wire.TagName) || !semver.IsValid(*wire.TagName) {
		return Release{}, coded(CodeReleaseInvalid, "node index release metadata is invalid", nil)
	}
	var selected *ReleaseAsset
	for _, assetWire := range *wire.Assets {
		if assetWire.Name == nil || *assetWire.Name != "index.json" {
			continue
		}
		if selected != nil || assetWire.URL == nil || assetWire.State == nil || assetWire.Size == nil || assetWire.Digest == nil || *assetWire.State != "uploaded" {
			return Release{}, coded(CodeAssetInvalid, "node index asset metadata is invalid", nil)
		}
		asset := ReleaseAsset{URL: *assetWire.URL, Size: *assetWire.Size, Digest: *assetWire.Digest}
		if err := validateReleaseAsset(asset); err != nil {
			return Release{}, coded(CodeAssetInvalid, "node index asset metadata is invalid", err)
		}
		selected = &asset
	}
	if selected == nil {
		return Release{}, coded(CodeAssetInvalid, "node index asset metadata is invalid", nil)
	}
	return Release{Tag: *wire.TagName, Draft: *wire.Draft, Prerelease: *wire.Prerelease, Immutable: *wire.Immutable, Asset: *selected}, nil
}

func validateReleaseAsset(asset ReleaseAsset) error {
	if asset.Size <= 0 || asset.Size > MaxIndexBytes || !digestPattern.MatchString(asset.Digest) {
		return errors.New("asset size or digest is invalid")
	}
	parsed, err := url.Parse(asset.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "api.github.com" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		!strings.HasPrefix(parsed.Path, assetAPIPathPrefix) || strings.TrimPrefix(parsed.Path, assetAPIPathPrefix) == "" || strings.Contains(strings.TrimPrefix(parsed.Path, assetAPIPathPrefix), "/") {
		return errors.New("asset API URL is invalid")
	}
	return nil
}

func validateGitHubRedirect(request *http.Request, via []*http.Request) error {
	if len(via) >= 10 || request.URL.Scheme != "https" || request.URL.User != nil {
		return errors.New("GitHub redirect is not allowed")
	}
	switch request.URL.Host {
	case "api.github.com", "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		return nil
	default:
		return errors.New("GitHub redirect is not allowed")
	}
}

func readBounded(reader io.Reader, maximum int) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, int64(maximum)+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maximum {
		return nil, errors.New("response exceeds byte budget")
	}
	return body, nil
}

func headersWithinBudget(headers http.Header) bool {
	total := 0
	for name, values := range headers {
		total += len(name)
		for _, value := range values {
			total += len(value)
		}
		if total > maxResponseHeaderSize {
			return false
		}
	}
	return true
}

func contextError(ctx context.Context, _ error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return nil
}

func utf8Length(value string) int {
	return len([]rune(value))
}
