package backup

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestCodedErrorDoesNotExposeCause(t *testing.T) {
	cause := errors.New("postgres://agent:secret@example/db")
	err := Wrap(CodeCreateFailed, "open source", cause)
	if CodeOf(err) != CodeCreateFailed {
		t.Fatalf("CodeOf() = %q; want %q", CodeOf(err), CodeCreateFailed)
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "postgres://") {
		t.Fatalf("Error() exposed cause: %v", err)
	}
	if !errors.Is(err, cause) {
		t.Fatal("errors.Is() did not preserve the programmatic cause")
	}
}

func TestCodeOfFindsCodedErrorThroughWrapping(t *testing.T) {
	err := fmt.Errorf("backup command failed: %w", Wrap(CodeChecksumMismatch, "verify archive checksum", errors.New("sensitive bytes")))
	if got := CodeOf(err); got != CodeChecksumMismatch {
		t.Fatalf("CodeOf() = %q; want %q", got, CodeChecksumMismatch)
	}
	if got := CodeOf(errors.New("plain")); got != "" {
		t.Fatalf("CodeOf(plain) = %q; want empty", got)
	}
}

func TestStableErrorCodesAreDistinct(t *testing.T) {
	codes := []Code{
		CodeArchiveInvalid, CodeChecksumMismatch, CodeFormatUnsupported, CodeRuntimeTooOld,
		CodeSchemaNotCurrent, CodeSizeLimit, CodeAPIRunning, CodeTargetNotEmpty,
		CodeReferenceInvalid, CodeCreateFailed, CodeRestoreFailed,
	}
	seen := make(map[Code]struct{}, len(codes))
	for _, code := range codes {
		if code == "" {
			t.Fatal("empty stable error code")
		}
		if _, exists := seen[code]; exists {
			t.Fatalf("duplicate stable error code %q", code)
		}
		seen[code] = struct{}{}
	}
}
