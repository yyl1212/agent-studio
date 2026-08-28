package backup

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublishAtomicDoesNotOverwriteAndUses0600(t *testing.T) {
	directory := t.TempDir()
	output := filepath.Join(directory, "instance.asbak")
	if err := os.WriteFile(output, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := publishAtomic(output, func(file *os.File) error {
		_, err := file.WriteString("replace")
		return err
	})
	if err == nil {
		t.Fatal("expected an existing-output error")
	}
	got, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "keep" {
		t.Fatalf("output=%q", got)
	}
	assertNoAtomicTemporaries(t, directory)

	fresh := filepath.Join(directory, "fresh.asbak")
	if err := publishAtomic(fresh, func(file *os.File) error {
		_, err := file.WriteString("archive")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode=%#o", got)
	}
}

func TestPublishAtomicCleansTemporaryOnWriteFailure(t *testing.T) {
	directory := t.TempDir()
	output := filepath.Join(directory, "instance.asbak")
	want := errors.New("write failed")
	if err := publishAtomic(output, func(*os.File) error { return want }); !errors.Is(err, want) {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("output err=%v", err)
	}
	assertNoAtomicTemporaries(t, directory)
}

func TestPublishAtomicCleansTemporaryOnPanic(t *testing.T) {
	directory := t.TempDir()
	output := filepath.Join(directory, "instance.asbak")
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected panic")
			}
		}()
		_ = publishAtomic(output, func(*os.File) error { panic("boom") })
	}()
	assertNoAtomicTemporaries(t, directory)
}

func assertNoAtomicTemporaries(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".agent-studio-backup-") {
			t.Fatalf("temporary file remains: %s", entry.Name())
		}
	}
}
