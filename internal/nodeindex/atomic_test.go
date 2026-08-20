package nodeindex

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWriteReplacesIndexWithPrivateRegularFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	if err := writeAtomic(dir, []byte("verified index")); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(dir, "index.json")
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(name)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "verified index" || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("raw=%q mode=%v", raw, info.Mode())
	}
	assertNoAtomicTemps(t, dir)
}

func TestAtomicWriteRenameFailurePreservesOldIndexAndCleansExactTemp(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "index.json")
	if err := os.WriteFile(name, []byte("old index"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("rename failed")
	err := writeAtomicWithRename(dir, []byte("new index"), func(_, _ string) error { return wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("err=%v", err)
	}
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "old index" {
		t.Fatalf("old index changed: %q", raw)
	}
	assertNoAtomicTemps(t, dir)
}

func assertNoAtomicTemps(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".index.json.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary files=%v", matches)
	}
}
