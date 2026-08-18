package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestNodeTestUsesOfflineCGOFreeGoCommands(t *testing.T) {
	root := nodeTestRoot(t)
	var calls []processCall
	runner := func(_ context.Context, call processCall) error {
		calls = append(calls, call)
		if call.Stdout != nil {
			_, _ = io.WriteString(call.Stdout, "ok\n")
		}
		return nil
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runNodeTest(context.Background(), root, "./extensions/echo", &stdout, &stderr, runner)
	if code != 0 || stderr.Len() != 0 || stdout.String() != "ok\nok\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if len(calls) != 2 {
		t.Fatalf("calls=%#v", calls)
	}
	assertProcessCall(t, calls[0], "go", []string{"list", "-mod=readonly", "./extensions/echo"}, map[string]string{"CGO_ENABLED": "0", "GOPROXY": "off"})
	assertProcessCall(t, calls[1], "go", []string{"test", "./extensions/echo", "-count=1"}, map[string]string{"CGO_ENABLED": "0", "GOFLAGS": "-mod=mod"})
}

func TestNodeTestAcceptsCurrentModuleImportPath(t *testing.T) {
	root := nodeTestRoot(t)
	var first processCall
	code := runNodeTest(context.Background(), root, "example.com/project/extensions/echo", io.Discard, io.Discard, func(_ context.Context, call processCall) error {
		if first.Name == "" {
			first = call
		}
		return nil
	})
	if code != 0 || len(first.Args) != 3 || first.Args[2] != "example.com/project/extensions/echo" {
		t.Fatalf("code=%d first=%#v", code, first)
	}
}

func TestNodeTestRejectsPackageOutsideExtensions(t *testing.T) {
	root := nodeTestRoot(t)
	called := false
	var stderr bytes.Buffer
	code := runNodeTest(context.Background(), root, "../outside", io.Discard, &stderr, func(context.Context, processCall) error {
		called = true
		return nil
	})
	if code != 2 || called || stderr.String() != "node test: package must be an immediate child of extensions/\n" {
		t.Fatalf("code=%d called=%v stderr=%q", code, called, stderr.String())
	}
}

func TestNodeTestStopsAfterGoListFailureAndPreservesOutput(t *testing.T) {
	root := nodeTestRoot(t)
	wantErr := errors.New("exit status 1")
	calls := 0
	var stderr bytes.Buffer
	code := runNodeTest(context.Background(), root, "./extensions/echo", io.Discard, &stderr, func(_ context.Context, call processCall) error {
		calls++
		_, _ = io.WriteString(call.Stderr, "package unavailable\n")
		return wantErr
	})
	if code != 1 || calls != 1 || stderr.String() != "package unavailable\n" {
		t.Fatalf("code=%d calls=%d stderr=%q", code, calls, stderr.String())
	}
}

func TestNodeTestPreservesGoTestFailureOutput(t *testing.T) {
	root := nodeTestRoot(t)
	calls := 0
	var stderr bytes.Buffer
	code := runNodeTest(context.Background(), root, "./extensions/echo", io.Discard, &stderr, func(_ context.Context, call processCall) error {
		calls++
		if calls == 2 {
			_, _ = io.WriteString(call.Stderr, "test failed\n")
			return errors.New("exit status 1")
		}
		return nil
	})
	if code != 1 || calls != 2 || stderr.String() != "test failed\n" {
		t.Fatalf("code=%d calls=%d stderr=%q", code, calls, stderr.String())
	}
}

func TestNodeTestRejectsNestedExtensionPackage(t *testing.T) {
	root := nodeTestRoot(t)
	for _, packageArg := range []string{"./extensions/team/echo", "example.com/project/extensions/team/echo"} {
		t.Run(packageArg, func(t *testing.T) {
			code := runNodeTest(context.Background(), root, packageArg, io.Discard, io.Discard, func(context.Context, processCall) error {
				t.Fatal("runner must not be called")
				return nil
			})
			if code != 2 {
				t.Fatalf("code=%d", code)
			}
		})
	}
}

func nodeTestRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/project\n\ngo 1.26.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func assertProcessCall(t *testing.T, call processCall, name string, args []string, environment map[string]string) {
	t.Helper()
	if call.Name != name || !equalStrings(call.Args, args) {
		t.Fatalf("call=%#v, want name=%q args=%v", call, name, args)
	}
	for key, want := range environment {
		if got := call.Environment[key]; got != want {
			t.Fatalf("environment[%q]=%q, want %q", key, got, want)
		}
	}
}
