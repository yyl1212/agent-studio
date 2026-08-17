package cli

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
)

func TestDiagnoseClassifiesRequiredToolsAndPorts(t *testing.T) {
	deps, listeners := healthyDoctorDeps()
	deps.Command = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "go" {
			return []byte("go version go1.25.0 linux/amd64"), nil
		}
		return healthyDoctorCommand(name, args...)
	}
	results := Diagnose(context.Background(), "/repo", deps)
	assertCheck(t, results, "go", checkFail)
	assertCheck(t, results, "node", checkOK)
	assertCheck(t, results, "port 8080", checkOK)
	for _, listener := range listeners {
		if !listener.closed {
			t.Fatal("successful port probe was not closed")
		}
	}
}

func TestDiagnoseReportsDockerDaemonFailure(t *testing.T) {
	deps, _ := healthyDoctorDeps()
	deps.Command = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "docker" && len(args) > 0 && args[0] == "info" {
			return nil, errors.New("cannot connect")
		}
		return healthyDoctorCommand(name, args...)
	}
	results := Diagnose(context.Background(), "/repo", deps)
	assertCheck(t, results, "docker", checkFail)
}

func TestDiagnoseReportsOccupiedPortAndStoppedDatabaseAsWarnings(t *testing.T) {
	deps, _ := healthyDoctorDeps()
	deps.Listen = func(_ string, address string) (net.Listener, error) {
		if strings.HasSuffix(address, ":8080") {
			return nil, errors.New("address already in use")
		}
		return &doctorTestListener{}, nil
	}
	deps.Command = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "docker" && equalStrings(args, []string{"compose", "ps", "--status", "running", "--services", "db"}) {
			return []byte(""), nil
		}
		return healthyDoctorCommand(name, args...)
	}
	results := Diagnose(context.Background(), "/repo", deps)
	assertCheck(t, results, "port 8080", checkWarn)
	assertCheck(t, results, "postgres", checkWarn)
}

func TestDiagnoseChecksNodeManifestWhenPresent(t *testing.T) {
	tests := []struct {
		name     string
		readFile func(string) ([]byte, error)
		status   string
	}{
		{name: "missing", readFile: func(string) ([]byte, error) { return nil, os.ErrNotExist }, status: checkWarn},
		{name: "valid", readFile: func(string) ([]byte, error) {
			return []byte("apiVersion: agent-studio.dev/v1alpha1\nnodes: []\n"), nil
		}, status: checkOK},
		{name: "invalid", readFile: func(string) ([]byte, error) {
			return []byte("apiVersion: v2\nnodes: []\n"), nil
		}, status: checkFail},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			deps, _ := healthyDoctorDeps()
			deps.ReadFile = test.readFile
			results := Diagnose(context.Background(), "/repo", deps)
			assertCheck(t, results, "manifest", test.status)
		})
	}
}

func healthyDoctorDeps() (DoctorDeps, []*doctorTestListener) {
	listeners := make([]*doctorTestListener, 0, 3)
	return DoctorDeps{
		LookPath: func(name string) (string, error) { return "/bin/" + name, nil },
		Command: func(_ context.Context, name string, args ...string) ([]byte, error) {
			return healthyDoctorCommand(name, args...)
		},
		Listen: func(string, string) (net.Listener, error) {
			listener := &doctorTestListener{}
			listeners = append(listeners, listener)
			return listener, nil
		},
		ReadFile: func(string) ([]byte, error) { return nil, os.ErrNotExist },
	}, listeners
}

func healthyDoctorCommand(name string, args ...string) ([]byte, error) {
	switch name {
	case "go":
		return []byte("go version go1.26.5 darwin/arm64"), nil
	case "node":
		return []byte("v24.4.0"), nil
	case "corepack":
		return []byte("0.34.0"), nil
	case "docker":
		switch {
		case equalStrings(args, []string{"info", "--format", "{{.ServerVersion}}"}):
			return []byte("28.3.0"), nil
		case equalStrings(args, []string{"compose", "version", "--short"}):
			return []byte("2.40.0"), nil
		case equalStrings(args, []string{"compose", "ps", "--status", "running", "--services", "db"}):
			return []byte("db\n"), nil
		case equalStrings(args, []string{"compose", "exec", "-T", "db", "pg_isready", "-U", "agent", "-d", "agent_studio"}):
			return []byte("accepting connections"), nil
		}
	}
	return nil, errors.New("unexpected command")
}

func assertCheck(t *testing.T, results []CheckResult, name, status string) {
	t.Helper()
	for _, result := range results {
		if result.Name == name {
			if result.Status != status {
				t.Fatalf("check %q status=%q detail=%q, want %q", name, result.Status, result.Detail, status)
			}
			return
		}
	}
	t.Fatalf("check %q missing: %#v", name, results)
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

type doctorTestListener struct {
	closed bool
}

func (listener *doctorTestListener) Accept() (net.Conn, error) {
	return nil, errors.New("not implemented")
}
func (listener *doctorTestListener) Close() error { listener.closed = true; return nil }
func (*doctorTestListener) Addr() net.Addr        { return doctorTestAddress("127.0.0.1:0") }

type doctorTestAddress string

func (address doctorTestAddress) Network() string { return "tcp" }
func (address doctorTestAddress) String() string  { return string(address) }

var _ net.Listener = (*doctorTestListener)(nil)
var _ net.Addr = doctorTestAddress("")
