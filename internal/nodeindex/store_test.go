package nodeindex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStoreTransitionsEmbeddedCacheInvalidAndDeleted(t *testing.T) {
	dir := t.TempDir()
	store, err := openStoreWithEmbedded(dir, embeddedIndex)
	if err != nil {
		t.Fatal(err)
	}
	assertSnapshot(t, store.Current(), SourceEmbedded, "v0.1.0", CodeEmbeddedSnapshot)

	writeStoreIndex(t, dir, storeIndexFixture(t, "v0.2.0"))
	assertSnapshot(t, store.Current(), SourceCache, "v0.2.0", "")

	if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte(`{"bad":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	assertSnapshot(t, store.Current(), SourceCache, "v0.2.0", CodeContentInvalid)

	if err := os.Remove(filepath.Join(dir, "index.json")); err != nil {
		t.Fatal(err)
	}
	assertSnapshot(t, store.Current(), SourceEmbedded, "v0.1.0", CodeEmbeddedSnapshot)
}

func TestOpenStoreDoesNotCreateCacheDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing", "cache")
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	assertSnapshot(t, store.Current(), SourceEmbedded, "v0.1.0", CodeEmbeddedSnapshot)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("cache directory was created: %v", err)
	}
}

func TestStoreDoesNotReparseAnUnchangedFailedFingerprint(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte(`{"bad":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	parser := func(source string, data []byte) (Index, error) {
		calls.Add(1)
		return Parse(source, data)
	}
	store, err := openStore(dir, embeddedIndex, parser)
	if err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("parse calls after open=%d", got)
	}
	store.Current()
	store.Current()
	if got := calls.Load(); got != 2 {
		t.Fatalf("unchanged invalid cache reparsed: %d", got)
	}
}

func TestStoreReloadsAtomicReplacementWithSameSizeAndMtime(t *testing.T) {
	dir := t.TempDir()
	first := storeIndexFixture(t, "v0.2.0")
	second := storeIndexFixture(t, "v0.3.0")
	if len(first) != len(second) {
		t.Fatalf("fixture sizes differ: %d %d", len(first), len(second))
	}
	stamp := time.Unix(1_800_000_000, 0)
	writeStoreIndexAt(t, dir, first, stamp)
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	assertSnapshot(t, store.Current(), SourceCache, "v0.2.0", "")
	writeStoreIndexAt(t, dir, second, stamp)
	assertSnapshot(t, store.Current(), SourceCache, "v0.3.0", "")
}

func TestStoreConcurrentReadersObserveOnlyCompleteSnapshots(t *testing.T) {
	dir := t.TempDir()
	writeStoreIndex(t, dir, storeIndexFixture(t, "v0.2.0"))
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan string, 64)
	var readers sync.WaitGroup
	for range 64 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			<-start
			results <- store.Current().Index.Metadata.Release
		}()
	}
	close(start)
	writeStoreIndex(t, dir, storeIndexFixture(t, "v0.3.0"))
	readers.Wait()
	close(results)
	for release := range results {
		if release != "v0.2.0" && release != "v0.3.0" {
			t.Fatalf("partial release=%q", release)
		}
	}
}

func TestStoreAndCatalogReturnDeepCopies(t *testing.T) {
	dir := t.TempDir()
	writeStoreIndex(t, dir, storeIndexFixture(t, "v0.2.0"))
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	first := store.Current()
	first.Index.Packages[0].Categories[0] = "changed"
	first.Index.Packages[0].Versions[0].Manifest.Registrations[0].Nodes[0].Type = "changed.node"
	second := store.Current()
	if second.Index.Packages[0].Categories[0] == "changed" || second.Index.Packages[0].Versions[0].Manifest.Registrations[0].Nodes[0].Type == "changed.node" {
		t.Fatal("Store.Current leaked mutable state")
	}

	catalog := NewCatalog(store, fixtureRuntime())
	status := catalog.Status()
	if status.Source != SourceCache || status.Release != "v0.2.0" || status.PackageCount != 1 || status.CompatiblePackageCount != 1 || status.Stale || status.WarningCode != nil {
		t.Fatalf("status=%+v", status)
	}
	search, err := catalog.Search(Query{CompatibleOnly: true, Limit: 50})
	if err != nil || search.Total != 1 {
		t.Fatalf("search=%+v err=%v", search, err)
	}
	detail, err := catalog.Get("github.com/example/agent-nodes")
	if err != nil || detail.Name != "github.com/example/agent-nodes" {
		t.Fatalf("detail=%+v err=%v", detail, err)
	}
}

func TestResolveCacheDirUsesDefaultAndRejectsUnsafeConfiguredPaths(t *testing.T) {
	defaultDir, err := ResolveCacheDir("")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(defaultDir) || filepath.Base(defaultDir) != "node-index" || filepath.Base(filepath.Dir(defaultDir)) != "agent-studio" {
		t.Fatalf("default cache dir=%q", defaultDir)
	}
	explicit := filepath.Join(t.TempDir(), "node-index")
	if got, err := ResolveCacheDir(explicit); err != nil || got != explicit {
		t.Fatalf("explicit=%q err=%v", got, err)
	}
	for _, configured := range []string{"relative/cache", explicit + string(os.PathSeparator) + ".."} {
		if _, err := ResolveCacheDir(configured); err == nil {
			t.Fatalf("accepted configured path %q", configured)
		}
	}
}

func storeIndexFixture(t *testing.T, release string) []byte {
	t.Helper()
	var index map[string]any
	if err := json.Unmarshal(readFixture(t, "valid.json"), &index); err != nil {
		t.Fatal(err)
	}
	index["metadata"].(map[string]any)["release"] = release
	raw, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(raw, '\n')
}

func writeStoreIndex(t *testing.T, dir string, data []byte) {
	t.Helper()
	writeStoreIndexAt(t, dir, data, time.Now())
}

func writeStoreIndexAt(t *testing.T, dir string, data []byte, stamp time.Time) {
	t.Helper()
	temporary, err := os.CreateTemp(dir, ".store-test-*")
	if err != nil {
		t.Fatal(err)
	}
	temporaryName := temporary.Name()
	t.Cleanup(func() { _ = os.Remove(temporaryName) })
	if _, err := temporary.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := temporary.Chmod(0o600); err != nil {
		t.Fatal(err)
	}
	if err := temporary.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(temporaryName, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporaryName, filepath.Join(dir, "index.json")); err != nil {
		t.Fatal(err)
	}
}

func assertSnapshot(t *testing.T, snapshot Snapshot, source SnapshotSource, release string, warning Code) {
	t.Helper()
	if snapshot.Source != source || snapshot.Index.Metadata.Release != release || snapshot.WarningCode != warning {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}
