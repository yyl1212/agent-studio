package nodeindex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrentRefreshHasOneWriter(t *testing.T) {
	dir := t.TempDir()
	data := storeIndexFixture(t, "v0.2.0")
	source := newBarrierReleaseSource(validRemoteRelease("v0.2.0", data), data)
	firstDone := make(chan error, 1)
	go func() {
		_, err := NewRefresher(dir, source).Refresh(context.Background())
		firstDone <- err
	}()
	<-source.downloadStarted
	if _, err := NewRefresher(dir, source).Refresh(context.Background()); CodeOf(err) != CodeRefreshInProgress {
		t.Fatalf("second refresh err=%v code=%q", err, CodeOf(err))
	}
	close(source.releaseDownload)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if got := source.latestCalls.Load(); got != 1 {
		t.Fatalf("latest calls=%d", got)
	}
}

func TestRefreshUpdatesAndThenReportsAlreadyCurrent(t *testing.T) {
	dir := t.TempDir()
	data := storeIndexFixture(t, "v0.2.0")
	source := &fakeReleaseSource{release: validRemoteRelease("v0.2.0", data), data: data}
	refresher := NewRefresher(dir, source)
	first, err := refresher.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != RefreshUpdated || first.PreviousRelease != "v0.1.0" || first.Release != "v0.2.0" {
		t.Fatalf("first=%+v", first)
	}
	second, err := refresher.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != RefreshAlreadyCurrent || second.PreviousRelease != "v0.2.0" || second.Release != "v0.2.0" {
		t.Fatalf("second=%+v", second)
	}
	assertFileBytes(t, filepath.Join(dir, "index.json"), data)
}

func TestRefreshRepairsEmbeddedAndInvalidCacheAtSameRelease(t *testing.T) {
	data := append([]byte(nil), embeddedIndex...)
	for _, setup := range []struct {
		name  string
		write []byte
	}{
		{name: "missing cache"},
		{name: "invalid cache", write: []byte(`{"bad":true}`)},
	} {
		t.Run(setup.name, func(t *testing.T) {
			dir := t.TempDir()
			if setup.write != nil {
				if err := os.WriteFile(filepath.Join(dir, "index.json"), setup.write, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			result, err := NewRefresher(dir, &fakeReleaseSource{release: validRemoteRelease("v0.1.0", data), data: data}).Refresh(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != RefreshUpdated || result.Release != "v0.1.0" {
				t.Fatalf("result=%+v", result)
			}
			assertFileBytes(t, filepath.Join(dir, "index.json"), data)
		})
	}
}

func TestRefreshRejectsDowngradeDriftAndInvalidContentWithoutChangingCache(t *testing.T) {
	tests := []struct {
		name       string
		current    []byte
		release    Release
		downloaded []byte
		code       Code
	}{
		{
			name: "downgrade", current: storeIndexFixture(t, "v0.3.0"),
			release: validRemoteRelease("v0.2.0", storeIndexFixture(t, "v0.2.0")), downloaded: storeIndexFixture(t, "v0.2.0"), code: CodeReleaseDowngrade,
		},
		{
			name: "same release byte drift", current: storeIndexFixture(t, "v0.2.0"),
			release: validRemoteRelease("v0.2.0", append([]byte(" \n"), storeIndexFixture(t, "v0.2.0")...)), downloaded: append([]byte(" \n"), storeIndexFixture(t, "v0.2.0")...), code: CodeReleaseInvalid,
		},
		{
			name: "metadata release mismatch", current: storeIndexFixture(t, "v0.2.0"),
			release: validRemoteRelease("v0.3.0", storeIndexFixture(t, "v0.2.0")), downloaded: storeIndexFixture(t, "v0.2.0"), code: CodeReleaseInvalid,
		},
		{
			name: "invalid content", current: storeIndexFixture(t, "v0.2.0"),
			release: validRemoteRelease("v0.3.0", []byte(`{"bad":true}`)), downloaded: []byte(`{"bad":true}`), code: CodeContentInvalid,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			writeStoreIndex(t, dir, test.current)
			before := readFile(t, filepath.Join(dir, "index.json"))
			_, err := NewRefresher(dir, &fakeReleaseSource{release: test.release, data: test.downloaded}).Refresh(context.Background())
			if CodeOf(err) != test.code {
				t.Fatalf("err=%v code=%q want=%q", err, CodeOf(err), test.code)
			}
			after := readFile(t, filepath.Join(dir, "index.json"))
			if string(after) != string(before) {
				t.Fatalf("cache changed before=%q after=%q", before, after)
			}
		})
	}
}

func TestRefreshWriterFailurePreservesOldCacheAndReleasesLock(t *testing.T) {
	dir := t.TempDir()
	current := storeIndexFixture(t, "v0.2.0")
	remote := storeIndexFixture(t, "v0.3.0")
	writeStoreIndex(t, dir, current)
	refresher := NewRefresher(dir, &fakeReleaseSource{release: validRemoteRelease("v0.3.0", remote), data: remote})
	refresher.write = func(string, []byte) error { return errors.New("disk full") }
	if _, err := refresher.Refresh(context.Background()); CodeOf(err) != CodeCacheWriteFailed {
		t.Fatalf("err=%v code=%q", err, CodeOf(err))
	}
	assertFileBytes(t, filepath.Join(dir, "index.json"), current)
	release, err := tryRefreshLock(dir)
	if err != nil {
		t.Fatalf("lock was not released: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshDefensivelyVerifiesReleaseAndDownloadedDigest(t *testing.T) {
	data := storeIndexFixture(t, "v0.2.0")
	tests := []struct {
		name    string
		release Release
		data    []byte
		code    Code
	}{
		{name: "mutable release", release: func() Release {
			release := validRemoteRelease("v0.2.0", data)
			release.Immutable = false
			return release
		}(), data: data, code: CodeReleaseInvalid},
		{name: "invalid digest metadata", release: func() Release {
			release := validRemoteRelease("v0.2.0", data)
			release.Asset.Digest = "sha256:" + string(make([]byte, 64))
			return release
		}(), data: data, code: CodeAssetInvalid},
		{name: "downloaded digest mismatch", release: validRemoteRelease("v0.2.0", append(append([]byte(nil), data...), '!')), data: data, code: CodeDigestMismatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewRefresher(t.TempDir(), &fakeReleaseSource{release: test.release, data: test.data}).Refresh(context.Background())
			if CodeOf(err) != test.code {
				t.Fatalf("err=%v code=%q want=%q", err, CodeOf(err), test.code)
			}
		})
	}
}

func TestRefreshCleansOnlyOldOwnedTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	oldTemporary := filepath.Join(dir, ".index.json.tmp-old")
	recentTemporary := filepath.Join(dir, ".index.json.tmp-recent")
	unrelated := filepath.Join(dir, "keep-me")
	for _, name := range []string{oldTemporary, recentTemporary, unrelated} {
		if err := os.WriteFile(name, []byte("temporary"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(oldTemporary, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	data := storeIndexFixture(t, "v0.2.0")
	if _, err := NewRefresher(dir, &fakeReleaseSource{release: validRemoteRelease("v0.2.0", data), data: data}).Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(oldTemporary); !os.IsNotExist(err) {
		t.Fatalf("old owned temporary was not removed: %v", err)
	}
	for _, name := range []string{recentTemporary, unrelated} {
		if _, err := os.Lstat(name); err != nil {
			t.Fatalf("safe file %s changed: %v", name, err)
		}
	}
}

type fakeReleaseSource struct {
	release       Release
	data          []byte
	latestErr     error
	downloadErr   error
	latestCalls   atomic.Int32
	downloadCalls atomic.Int32
}

func (source *fakeReleaseSource) Latest(context.Context) (Release, error) {
	source.latestCalls.Add(1)
	return source.release, source.latestErr
}

func (source *fakeReleaseSource) Download(context.Context, ReleaseAsset) ([]byte, error) {
	source.downloadCalls.Add(1)
	return append([]byte(nil), source.data...), source.downloadErr
}

type barrierReleaseSource struct {
	fakeReleaseSource
	downloadStarted chan struct{}
	releaseDownload chan struct{}
	once            sync.Once
}

func newBarrierReleaseSource(release Release, data []byte) *barrierReleaseSource {
	return &barrierReleaseSource{
		fakeReleaseSource: fakeReleaseSource{release: release, data: data},
		downloadStarted:   make(chan struct{}),
		releaseDownload:   make(chan struct{}),
	}
}

func (source *barrierReleaseSource) Download(ctx context.Context, _ ReleaseAsset) ([]byte, error) {
	source.downloadCalls.Add(1)
	source.once.Do(func() { close(source.downloadStarted) })
	select {
	case <-source.releaseDownload:
		return append([]byte(nil), source.data...), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func validRemoteRelease(tag string, data []byte) Release {
	sum := sha256.Sum256(data)
	return Release{
		Tag: tag, Immutable: true,
		Asset: ReleaseAsset{
			URL:  "https://api.github.com/repos/yyl1212/agent-studio-node-index/releases/assets/123",
			Size: int64(len(data)), Digest: "sha256:" + hex.EncodeToString(sum[:]),
		},
	}
}

func assertFileBytes(t *testing.T, name string, want []byte) {
	t.Helper()
	if got := readFile(t, name); string(got) != string(want) {
		t.Fatalf("file=%q want=%q", got, want)
	}
}

func readFile(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
