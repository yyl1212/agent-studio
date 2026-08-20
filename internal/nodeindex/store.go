package nodeindex

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type SnapshotSource string

const (
	SourceEmbedded SnapshotSource = "embedded"
	SourceCache    SnapshotSource = "cache"
)

type Snapshot struct {
	Index       Index
	Source      SnapshotSource
	WarningCode Code
}

type Status struct {
	Source                 SnapshotSource `json:"source"`
	Release                string         `json:"release"`
	GeneratedAt            time.Time      `json:"generatedAt"`
	PackageCount           int            `json:"packageCount"`
	CompatiblePackageCount int            `json:"compatiblePackageCount"`
	RuntimeVersion         string         `json:"runtimeVersion"`
	NodeAPI                string         `json:"nodeAPI"`
	Stale                  bool           `json:"stale"`
	WarningCode            *Code          `json:"warningCode"`
}

type parseIndexFunc func(string, []byte) (Index, error)

type fingerprintState uint8

const (
	fingerprintMissing fingerprintState = iota
	fingerprintPresent
)

type fileFingerprint struct {
	state   fingerprintState
	info    os.FileInfo
	size    int64
	modTime int64
}

func (fingerprint fileFingerprint) equal(other fileFingerprint) bool {
	if fingerprint.state != other.state {
		return false
	}
	if fingerprint.state == fingerprintMissing {
		return true
	}
	return fingerprint.size == other.size && fingerprint.modTime == other.modTime && os.SameFile(fingerprint.info, other.info)
}

type Store struct {
	cacheFile string
	embedded  Index
	parse     parseIndexFunc

	mu              sync.RWMutex
	current         Snapshot
	observed        fileFingerprint
	observedPresent bool
}

func ResolveCacheDir(configured string) (string, error) {
	if configured == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("resolve user cache directory: %w", err)
		}
		configured = filepath.Join(base, "agent-studio", "node-index")
	}
	if !filepath.IsAbs(configured) || filepath.Clean(configured) != configured {
		return "", fmt.Errorf("node index cache directory must be an absolute clean path")
	}
	return configured, nil
}

func OpenStore(cacheDir string) (*Store, error) {
	return openStore(cacheDir, embeddedIndex, Parse)
}

func openStoreWithEmbedded(cacheDir string, embedded []byte) (*Store, error) {
	return openStore(cacheDir, embedded, Parse)
}

func openStore(cacheDir string, embedded []byte, parser parseIndexFunc) (*Store, error) {
	index, err := parser("embedded node index", embedded)
	if err != nil {
		return nil, fmt.Errorf("open embedded node index: %w", err)
	}
	store := &Store{
		cacheFile: filepath.Join(cacheDir, "index.json"),
		embedded:  cloneIndex(index),
		parse:     parser,
		current: Snapshot{
			Index:       cloneIndex(index),
			Source:      SourceEmbedded,
			WarningCode: CodeEmbeddedSnapshot,
		},
	}
	store.Current()
	return store, nil
}

func (store *Store) Current() Snapshot {
	fingerprint, statErr := statFingerprint(store.cacheFile)
	store.mu.RLock()
	if statErr == nil && store.observedPresent && store.observed.equal(fingerprint) {
		current := cloneSnapshot(store.current)
		store.mu.RUnlock()
		return current
	}
	store.mu.RUnlock()

	store.mu.Lock()
	defer store.mu.Unlock()
	fingerprint, statErr = statFingerprint(store.cacheFile)
	if statErr == nil && store.observedPresent && store.observed.equal(fingerprint) {
		return cloneSnapshot(store.current)
	}
	if statErr != nil {
		store.current.WarningCode = CodeContentInvalid
		store.observedPresent = false
		return cloneSnapshot(store.current)
	}
	if fingerprint.state == fingerprintMissing {
		store.current = Snapshot{Index: cloneIndex(store.embedded), Source: SourceEmbedded, WarningCode: CodeEmbeddedSnapshot}
		store.observed = fingerprint
		store.observedPresent = true
		return cloneSnapshot(store.current)
	}

	raw, err := os.ReadFile(store.cacheFile)
	if err != nil {
		store.current.WarningCode = CodeContentInvalid
		store.observed = fingerprint
		store.observedPresent = true
		return cloneSnapshot(store.current)
	}
	index, err := store.parse("cached node index", raw)
	if err != nil {
		store.current.WarningCode = CodeContentInvalid
		store.observed = fingerprint
		store.observedPresent = true
		return cloneSnapshot(store.current)
	}
	store.current = Snapshot{Index: cloneIndex(index), Source: SourceCache}
	store.observed = fingerprint
	store.observedPresent = true
	return cloneSnapshot(store.current)
}

func statFingerprint(name string) (fileFingerprint, error) {
	info, err := os.Stat(name)
	if os.IsNotExist(err) {
		return fileFingerprint{state: fingerprintMissing}, nil
	}
	if err != nil {
		return fileFingerprint{}, err
	}
	if !info.Mode().IsRegular() {
		return fileFingerprint{}, fmt.Errorf("node index cache is not a regular file")
	}
	return fileFingerprint{state: fingerprintPresent, info: info, size: info.Size(), modTime: info.ModTime().UnixNano()}, nil
}

func cloneSnapshot(input Snapshot) Snapshot {
	return Snapshot{Index: cloneIndex(input.Index), Source: input.Source, WarningCode: input.WarningCode}
}

func cloneIndex(input Index) Index {
	output := input
	output.Packages = make([]Package, len(input.Packages))
	for packageIndex, pkg := range input.Packages {
		output.Packages[packageIndex] = pkg
		output.Packages[packageIndex].Categories = append([]string{}, pkg.Categories...)
		output.Packages[packageIndex].Keywords = append([]string{}, pkg.Keywords...)
		output.Packages[packageIndex].Versions = make([]PackageVersion, len(pkg.Versions))
		for versionIndex, version := range pkg.Versions {
			output.Packages[packageIndex].Versions[versionIndex] = clonePackageVersion(version)
		}
	}
	return output
}

type Catalog struct {
	store   *Store
	runtime Runtime
}

func NewCatalog(store *Store, runtime Runtime) *Catalog {
	return &Catalog{store: store, runtime: runtime}
}

func (catalog *Catalog) Status() Status {
	snapshot := catalog.store.Current()
	compatible := 0
	for _, pkg := range snapshot.Index.Packages {
		if Recommend(pkg, catalog.runtime).Version != nil {
			compatible++
		}
	}
	status := Status{
		Source:                 snapshot.Source,
		Release:                snapshot.Index.Metadata.Release,
		GeneratedAt:            snapshot.Index.Metadata.GeneratedAt,
		PackageCount:           len(snapshot.Index.Packages),
		CompatiblePackageCount: compatible,
		RuntimeVersion:         catalog.runtime.Version,
		NodeAPI:                catalog.runtime.NodeAPI,
		Stale:                  snapshot.Source == SourceEmbedded || snapshot.WarningCode != "",
	}
	if snapshot.WarningCode != "" {
		warning := snapshot.WarningCode
		status.WarningCode = &warning
	}
	return status
}

func (catalog *Catalog) Search(query Query) (SearchResult, error) {
	return Search(catalog.store.Current().Index, catalog.runtime, query)
}

func (catalog *Catalog) Get(name string) (PackageDetail, error) {
	return Detail(catalog.store.Current().Index, catalog.runtime, name)
}
