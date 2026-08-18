package cli

import (
	"bytes"
	"context"
	"io"
	"testing"
)

func TestRunTopLevelCommands(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantOut  string
		wantErr  string
	}{
		{name: "empty shows help", wantCode: 0, wantOut: "doctor\ngenerate\nnode init\nnode test\nversion\n"},
		{name: "help", args: []string{"help"}, wantCode: 0, wantOut: "doctor\ngenerate\nnode init\nnode test\nversion\n"},
		{name: "version", args: []string{"version"}, wantCode: 0, wantOut: "agent-studio 0.2.0 (agent-studio.dev/v1alpha1)\n"},
		{name: "unknown", args: []string{"missing"}, wantCode: 2, wantErr: "unknown command \"missing\"\n"},
		{name: "missing node subcommand", args: []string{"node"}, wantCode: 2, wantErr: "node requires init or test\n"},
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

func TestRunNodeTestRequiresPackage(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(context.Background(), []string{"node", "test"}, &stdout, &stderr, appDependencies{})
	if code != 2 || stdout.Len() != 0 || stderr.String() != "node test requires exactly one package\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
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
