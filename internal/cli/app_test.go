package cli

import (
	"bytes"
	"context"
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
		{name: "reserved node test", args: []string{"node", "test"}, wantCode: 1, wantErr: "node test is not implemented\n"},
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
