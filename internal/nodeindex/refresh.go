package nodeindex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	"github.com/yyl1212/agent-studio/internal/nodepackage"
	"golang.org/x/mod/semver"
)

type RefreshStatus string

const (
	RefreshUpdated        RefreshStatus = "updated"
	RefreshAlreadyCurrent RefreshStatus = "already-current"
)

type RefreshResult struct {
	Status          RefreshStatus `json:"status"`
	PreviousRelease string        `json:"previousRelease"`
	Release         string        `json:"release"`
}

type Refresher struct {
	cacheDir string
	source   ReleaseSource
	write    func(string, []byte) error
}

func NewRefresher(cacheDir string, source ReleaseSource) *Refresher {
	return &Refresher{cacheDir: cacheDir, source: source, write: writeAtomic}
}

func (refresher *Refresher) Refresh(ctx context.Context) (result RefreshResult, returnErr error) {
	releaseLock, err := tryRefreshLock(refresher.cacheDir)
	if err != nil {
		return RefreshResult{}, err
	}
	defer func() {
		if err := releaseLock(); err != nil && returnErr == nil {
			returnErr = coded(CodeCacheWriteFailed, "node index refresh lock could not be released", err)
		}
	}()
	cleanupStaleAtomicTemps(refresher.cacheDir, time.Now())

	release, err := refresher.source.Latest(ctx)
	if err != nil {
		return RefreshResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return RefreshResult{}, err
	}
	if err := validateRefreshRelease(release); err != nil {
		return RefreshResult{}, err
	}
	downloaded, err := refresher.source.Download(ctx, release.Asset)
	if err != nil {
		return RefreshResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return RefreshResult{}, err
	}
	if err := verifyDownloadedAsset(release.Asset, downloaded); err != nil {
		return RefreshResult{}, err
	}
	index, err := Parse("downloaded node index", downloaded)
	if err != nil {
		return RefreshResult{}, err
	}
	if index.Metadata.Release != release.Tag {
		return RefreshResult{}, coded(CodeReleaseInvalid, "node index release and content versions do not match", nil)
	}

	store, err := OpenStore(refresher.cacheDir)
	if err != nil {
		return RefreshResult{}, coded(CodeContentInvalid, "node index store could not be opened", err)
	}
	snapshot := store.Current()
	previous := snapshot.Index.Metadata.Release
	comparison := semver.Compare(release.Tag, previous)
	if comparison < 0 {
		return RefreshResult{}, coded(CodeReleaseDowngrade, "node index release downgrade was rejected", nil)
	}
	if comparison == 0 && snapshot.Source == SourceCache && snapshot.WarningCode == "" {
		current, err := os.ReadFile(filepath.Join(refresher.cacheDir, "index.json"))
		if err != nil {
			return RefreshResult{}, coded(CodeCacheWriteFailed, "node index cache could not be read", err)
		}
		if bytes.Equal(current, downloaded) {
			return RefreshResult{Status: RefreshAlreadyCurrent, PreviousRelease: previous, Release: release.Tag}, nil
		}
		return RefreshResult{}, coded(CodeReleaseInvalid, "node index release bytes changed without a version change", nil)
	}
	if err := refresher.write(refresher.cacheDir, downloaded); err != nil {
		return RefreshResult{}, coded(CodeCacheWriteFailed, "node index cache could not be replaced", err)
	}
	return RefreshResult{Status: RefreshUpdated, PreviousRelease: previous, Release: release.Tag}, nil
}

func validateRefreshRelease(release Release) error {
	if release.Draft || release.Prerelease || !release.Immutable || utf8.RuneCountInString(release.Tag) > nodepackage.MaxVersionLength ||
		!stableSemverPattern.MatchString(release.Tag) || !semver.IsValid(release.Tag) {
		return coded(CodeReleaseInvalid, "node index release metadata is invalid", nil)
	}
	if err := validateReleaseAsset(release.Asset); err != nil {
		return coded(CodeAssetInvalid, "node index asset metadata is invalid", err)
	}
	return nil
}

func verifyDownloadedAsset(asset ReleaseAsset, data []byte) error {
	if len(data) > MaxIndexBytes || int64(len(data)) != asset.Size {
		return coded(CodeDigestMismatch, "node index asset digest does not match", nil)
	}
	sum := sha256.Sum256(data)
	if "sha256:"+hex.EncodeToString(sum[:]) != asset.Digest {
		return coded(CodeDigestMismatch, "node index asset digest does not match", nil)
	}
	return nil
}
