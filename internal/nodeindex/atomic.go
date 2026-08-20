package nodeindex

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	atomicTemporaryPrefix = ".index.json.tmp-"
	staleTemporaryAge     = 24 * time.Hour
)

func writeAtomic(cacheDir string, data []byte) error {
	return writeAtomicWithRename(cacheDir, data, os.Rename)
}

func writeAtomicWithRename(cacheDir string, data []byte, rename func(string, string) error) (returnErr error) {
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(cacheDir, atomicTemporaryPrefix+"*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	closed := false
	defer func() {
		if !closed {
			returnErr = errors.Join(returnErr, temporary.Close())
		}
		returnErr = errors.Join(returnErr, removeExactTemporary(temporaryName))
	}()

	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := io.Copy(temporary, bytes.NewReader(data)); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		closed = true
		return err
	}
	closed = true
	if err := rename(temporaryName, filepath.Join(cacheDir, "index.json")); err != nil {
		return err
	}
	if directory, err := os.Open(cacheDir); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}

func cleanupStaleAtomicTemps(cacheDir string, now time.Time) {
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return
	}
	cutoff := now.Add(-staleTemporaryAge)
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), atomicTemporaryPrefix) {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || !info.ModTime().Before(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(cacheDir, entry.Name()))
	}
}

func removeExactTemporary(name string) error {
	err := os.Remove(name)
	if err == nil || os.IsNotExist(err) {
		return nil
	}
	return err
}
