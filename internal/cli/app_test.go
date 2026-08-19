package cli

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/yyl1212/agent-studio/internal/buildinfo"
)

func TestRunTopLevelCommands(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantOut  string
		wantErr  string
	}{
		{name: "empty shows help", wantCode: 0, wantOut: "doctor\ngenerate\nnode init\nnode package init\nnode test\nversion\n"},
		{name: "help", args: []string{"help"}, wantCode: 0, wantOut: "doctor\ngenerate\nnode init\nnode package init\nnode test\nversion\n"},
		{name: "unknown", args: []string{"missing"}, wantCode: 2, wantErr: "unknown command \"missing\"\n"},
		{name: "missing node subcommand", args: []string{"node"}, wantCode: 2, wantErr: "node requires init, package, or test\n"},
		{name: "unknown node subcommand", args: []string{"node", "missing"}, wantCode: 2, wantErr: "unknown node command \"missing\"\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := Run(context.Background(), test.args, &stdout, &stderr)
			if code != test.wantCode || stdout.String() != test.wantOut || stderr.String() != test.wantErr {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunVersionReportsBuildAndProtocolVersions(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(context.Background(), []string{"version"}, &stdout, &stderr, appDependencies{
		buildInfo: func() buildinfo.Info {
			return buildinfo.Info{
				Version: "v0.2.0-rc.1", SDKVersion: "0.2.0",
				APIVersion: "agent-studio.dev/v1alpha1",
				Revision:   "abc123", Dirty: true,
			}
		},
	})
	want := "agent-studio v0.2.0-rc.1 (sdk 0.2.0; api agent-studio.dev/v1alpha1; commit abc123; dirty true)\n"
	if code != 0 || stdout.String() != want || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunNodeTestRequiresPackage(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(context.Background(), []string{"node", "test"}, &stdout, &stderr, appDependencies{})
	if code != 2 || stdout.Len() != 0 || stderr.String() != "node test requires exactly one package\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunNodePackageInitRoutesRequiredFlags(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	called := false
	code := run(context.Background(), []string{
		"node", "package", "init",
		"--display-name", "Example Nodes",
		"--description", "示例节点",
		"--license", "Apache-2.0",
		"--repository", "https://example.com/nodes",
		"--runtime-min", "v0.2.0",
		"--runtime-max-exclusive", "v0.4.0",
	}, &stdout, &stderr, appDependencies{
		workingDir: func() (string, error) { return "/repo/subdir", nil },
		nodePackageInit: func(_ context.Context, start string, input packageInitInput) error {
			called = true
			if start != "/repo/subdir" || input.DisplayName != "Example Nodes" || input.Description != "示例节点" ||
				input.License != "Apache-2.0" || input.Repository != "https://example.com/nodes" ||
				input.RuntimeMin != "v0.2.0" || input.RuntimeMaxExclusive != "v0.4.0" {
				t.Fatalf("start=%q input=%+v", start, input)
			}
			return nil
		},
	})
	if code != 0 || !called || stdout.String() != "created agent-studio.node-package.json\n" || stderr.Len() != 0 {
		t.Fatalf("code=%d called=%t stdout=%q stderr=%q", code, called, stdout.String(), stderr.String())
	}
}

func TestRunNodePackageInitRejectsMissingRequiredFlags(t *testing.T) {
	called := false
	var stderr bytes.Buffer
	code := run(context.Background(), []string{
		"node", "package", "init",
		"--display-name", "Example Nodes",
	}, io.Discard, &stderr, appDependencies{
		nodePackageInit: func(context.Context, string, packageInitInput) error {
			called = true
			return nil
		},
	})
	if code != 2 || called || !bytes.Contains(stderr.Bytes(), []byte("required")) {
		t.Fatalf("code=%d called=%t stderr=%q", code, called, stderr.String())
	}
}

func TestRunRejectsUnexpectedArguments(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{args: []string{"help", "unexpected"}, want: "help takes no arguments\n"},
		{args: []string{"version", "unexpected"}, want: "version takes no arguments\n"},
		{args: []string{"doctor", "unexpected"}, want: "doctor takes no arguments\n"},
		{args: []string{"generate", "unexpected"}, want: "generate takes no arguments\n"},
	} {
		t.Run(test.args[0], func(t *testing.T) {
			var stderr bytes.Buffer
			code := run(context.Background(), test.args, io.Discard, &stderr, appDependencies{})
			if code != 2 || stderr.String() != test.want {
				t.Fatalf("code=%d stderr=%q", code, stderr.String())
			}
		})
	}
}

func TestRunNodeTestDelegatesFromWorkingDirectory(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	called := false
	code := run(context.Background(), []string{"node", "test", "./extensions/echo"}, &stdout, &stderr, appDependencies{
		workingDir: func() (string, error) { return "/repo/subdir", nil },
		nodeTest: func(_ context.Context, start, packageArg string, gotStdout, gotStderr io.Writer) int {
			called = true
			if start != "/repo/subdir" || packageArg != "./extensions/echo" || gotStdout != &stdout || gotStderr != &stderr {
				t.Fatalf("start=%q package=%q stdout=%T stderr=%T", start, packageArg, gotStdout, gotStderr)
			}
			return 7
		},
	})
	if code != 7 || !called {
		t.Fatalf("code=%d called=%v", code, called)
	}
}

func TestRunDoctorPrintsChecksAndReturnsFailure(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(context.Background(), []string{"doctor"}, &stdout, &stderr, appDependencies{
		workingDir: func() (string, error) { return "/repo", nil },
		diagnose: func(context.Context, string) []CheckResult {
			return []CheckResult{
				{Name: "go", Status: checkOK, Detail: "go1.26.5"},
				{Name: "docker", Status: checkFail, Detail: "daemon unavailable"},
			}
		},
	})
	if code != 1 {
		t.Fatalf("code=%d", code)
	}
	if got, want := stdout.String(), "[ok] go: go1.26.5\n[fail] docker: daemon unavailable\n"; got != want {
		t.Fatalf("stdout=%q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr=%q", stderr.String())
	}
}
