//go:build darwin || linux

package nodeindex

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRefreshLockIsNonBlockingAndReleased(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	releaseFirst, err := tryRefreshLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tryRefreshLock(dir); CodeOf(err) != CodeRefreshInProgress {
		t.Fatalf("second lock err=%v code=%q", err, CodeOf(err))
	}
	if err := releaseFirst(); err != nil {
		t.Fatal(err)
	}
	releaseSecond, err := tryRefreshLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := releaseSecond(); err != nil {
		t.Fatal(err)
	}

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	lockInfo, err := os.Stat(filepath.Join(dir, "refresh.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm()&0o077 != 0 || lockInfo.Mode().Perm()&0o077 != 0 {
		t.Fatalf("permissions dir=%#o lock=%#o", dirInfo.Mode().Perm(), lockInfo.Mode().Perm())
	}
}
