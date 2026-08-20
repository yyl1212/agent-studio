//go:build darwin || linux

package nodeindex

import (
	"errors"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

func tryRefreshLock(cacheDir string) (func() error, error) {
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return nil, coded(CodeCacheWriteFailed, "node index cache directory could not be created", err)
	}
	file, err := os.OpenFile(filepath.Join(cacheDir, "refresh.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, coded(CodeCacheWriteFailed, "node index refresh lock could not be opened", err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, coded(CodeRefreshInProgress, "node index refresh is already in progress", err)
		}
		return nil, coded(CodeCacheWriteFailed, "node index refresh lock could not be acquired", err)
	}

	var once sync.Once
	var releaseErr error
	return func() error {
		once.Do(func() {
			releaseErr = errors.Join(unix.Flock(int(file.Fd()), unix.LOCK_UN), file.Close())
		})
		return releaseErr
	}, nil
}
