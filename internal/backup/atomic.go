package backup

import (
	"errors"
	"os"
	"path/filepath"
)

func publishAtomic(output string, write func(*os.File) error) error {
	if _, err := os.Lstat(output); err == nil {
		return errors.New("backup output already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	parent := filepath.Dir(output)
	temporary, err := os.CreateTemp(parent, ".agent-studio-backup-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(name)
	}()

	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if err := write(temporary); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Link(name, output); err != nil {
		return err
	}

	directory, err := os.Open(parent)
	if err != nil {
		_ = os.Remove(output)
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil || closeErr != nil {
		_ = os.Remove(output)
		if syncErr != nil {
			return syncErr
		}
		return closeErr
	}
	return nil
}
