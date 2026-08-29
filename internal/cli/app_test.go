package cli

import (
	"bytes"
	"context"
	"io"
	"slices"
	"strings"
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
		{name: "empty shows help", wantCode: 0, wantOut: "backup create\nbackup inspect\ndoctor\ngenerate\nnode index refresh\nnode index status\nnode info\nnode init\nnode inspect\nnode package init\nnode search\nnode test\nversion\n"},
		{name: "help", args: []string{"help"}, wantCode: 0, wantOut: "backup create\nbackup inspect\ndoctor\ngenerate\nnode index refresh\nnode index status\nnode info\nnode init\nnode inspect\nnode package init\nnode search\nnode test\nversion\n"},
		{name: "unknown", args: []string{"missing"}, wantCode: 2, wantErr: "unknown command \"missing\"\n"},
		{name: "missing node subcommand", args: []string{"node"}, wantCode: 2, wantErr: "node requires index, info, init, inspect, package, search, or test\n"},
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

func TestRunRoutesBackup(t *testing.T) {
	called := []string{}
	code := run(context.Background(), []string{"backup", "inspect", "--json", "snapshot.asbak"}, io.Discard, io.Discard, appDependencies{
		backup: func(_ context.Context, args []string, _, _ io.Writer) int {
			called = append([]string(nil), args...)
			return 7
		},
	})
	if code != 7 || !slices.Equal(called, []string{"inspect", "--json", "snapshot.asbak"}) {
		t.Fatalf("code=%d called=%v", code, called)
	}
}

func TestRunRoutesNodeIndex(t *testing.T) {
	called := []string{}
	code := run(context.Background(), []string{"node", "index", "status", "--json"}, io.Discard, io.Discard, appDependencies{
		nodeIndex: func(_ context.Context, args []string, _, _ io.Writer) int {
			called = append([]string(nil), args...)
			return 7
		},
	})
	if code != 7 || !slices.Equal(called, []string{"status", "--json"}) {
		t.Fatalf("code=%d called=%v", code, called)
	}
}

func TestRunRoutesNodeSearch(t *testing.T) {
	for _, test := range []struct {
		args []string
		want []string
	}{
		{args: []string{"node", "search", "--json", "echo"}, want: []string{"search", "--json", "echo"}},
		{args: []string{"node", "info", "--json", "github.com/example/nodes"}, want: []string{"info", "--json", "github.com/example/nodes"}},
	} {
		t.Run(strings.Join(test.args, "_"), func(t *testing.T) {
			called := []string{}
			code := run(context.Background(), test.args, io.Discard, io.Discard, appDependencies{
				nodeSearch: func(_ context.Context, args []string, _, _ io.Writer) int {
					called = append([]string(nil), args...)
					return 7
				},
			})
			if code != 7 || !slices.Equal(called, test.want) {
				t.Fatalf("code=%d called=%v", code, called)
			}
		})
	}
}

func TestRunNodeInspectRoutesSupportedArguments(t *testing.T) {
	for _, test := range []struct {
		args       []string
		jsonOutput bool
	}{
		{args: []string{"node", "inspect", "example.com/nodes/echo"}},
		{args: []string{"node", "inspect", "--json", "example.com/nodes/echo"}, jsonOutput: true},
	} {
		t.Run(strings.Join(test.args, "_"), func(t *testing.T) {
			called := false
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run(context.Background(), test.args, &stdout, &stderr, appDependencies{
				workingDir: func() (string, error) { return "/repo/subdir", nil },
				nodeInspect: func(_ context.Context, start, importPath string, jsonOutput bool, gotStdout, gotStderr io.Writer) int {
					called = true
					if start != "/repo/subdir" || importPath != "example.com/nodes/echo" || jsonOutput != test.jsonOutput || gotStdout != &stdout || gotStderr != &stderr {
						t.Fatalf("start=%q path=%q json=%t", start, importPath, jsonOutput)
					}
					return 7
				},
			})
			if code != 7 || !called {
				t.Fatalf("code=%d called=%t", code, called)
			}
		})
	}
}

func TestRunNodeInspectRejectsInvalidArguments(t *testing.T) {
	for _, args := range [][]string{
		{"node", "inspect"},
		{"node", "inspect", "--unknown", "example.com/nodes/echo"},
		{"node", "inspect", "one", "two"},
	} {
		var stderr bytes.Buffer
		code := run(context.Background(), args, io.Discard, &stderr, appDependencies{})
		if code != 2 || stderr.String() != "node inspect usage: node inspect [--json] <import-path>\n" {
			t.Fatalf("args=%v code=%d stderr=%q", args, code, stderr.String())
		}
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
