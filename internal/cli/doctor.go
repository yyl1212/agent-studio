package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/yyl1212/agent-studio/internal/buildinfo"
	"github.com/yyl1212/agent-studio/internal/nodemanifest"
	"github.com/yyl1212/agent-studio/internal/nodepackage"
)

const (
	checkOK   = "ok"
	checkWarn = "warn"
	checkFail = "fail"
)

type DoctorDeps struct {
	LookPath    func(string) (string, error)
	Command     func(context.Context, string, ...string) ([]byte, error)
	Listen      func(string, string) (net.Listener, error)
	ReadFile    func(string) ([]byte, error)
	InspectMany func(context.Context, string, []string) []nodepackage.Inspection
}

type CheckResult struct {
	Name   string
	Status string
	Detail string
}

func Diagnose(ctx context.Context, root string, deps DoctorDeps) []CheckResult {
	results := make([]CheckResult, 0, 10)
	results = append(results, checkVersion(ctx, deps, "go", []string{"version"}, 1, 26, goVersion))
	results = append(results, checkVersion(ctx, deps, "node", []string{"--version"}, 24, 0, nodeVersion))
	results = append(results, checkAvailable(ctx, deps, "corepack", []string{"--version"}))

	docker := checkAvailable(ctx, deps, "docker", []string{"info", "--format", "{{.ServerVersion}}"})
	results = append(results, docker)
	compose := CheckResult{Name: "docker compose", Status: checkFail, Detail: "docker daemon unavailable"}
	if docker.Status == checkOK {
		compose = checkCompose(ctx, deps)
	}
	results = append(results, compose)
	manifestCheck, manifest, manifestValid := checkManifest(root, deps)
	results = append(results, manifestCheck)
	if manifestValid {
		results = append(results, checkNodePackages(ctx, root, manifest, deps)...)
	}
	results = append(results, checkPostgres(ctx, deps, docker.Status == checkOK && compose.Status == checkOK))

	for _, port := range []string{"5432", "8080", "5173"} {
		results = append(results, checkPort(deps, port))
	}
	return results
}

func checkManifest(root string, deps DoctorDeps) (CheckResult, nodemanifest.Manifest, bool) {
	path := filepath.Join(root, "agent-studio.nodes.yaml")
	data, err := deps.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return CheckResult{Name: "manifest", Status: checkWarn, Detail: "agent-studio.nodes.yaml not found"}, nodemanifest.Manifest{}, false
	}
	if err != nil {
		return CheckResult{Name: "manifest", Status: checkFail, Detail: err.Error()}, nodemanifest.Manifest{}, false
	}
	manifest, err := nodemanifest.Parse(path, data)
	if err != nil {
		return CheckResult{Name: "manifest", Status: checkFail, Detail: err.Error()}, nodemanifest.Manifest{}, false
	}
	return CheckResult{Name: "manifest", Status: checkOK, Detail: fmt.Sprintf("%s; %d node packages", manifest.APIVersion, len(manifest.Nodes))}, manifest, true
}

func checkNodePackages(ctx context.Context, root string, manifest nodemanifest.Manifest, deps DoctorDeps) []CheckResult {
	paths := make([]string, 0, len(manifest.Nodes))
	for _, node := range manifest.Nodes {
		paths = append(paths, node.Package)
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return []CheckResult{}
	}
	if deps.InspectMany == nil {
		results := make([]CheckResult, 0, len(paths))
		for _, importPath := range paths {
			results = append(results, CheckResult{
				Name: "node package " + importPath, Status: checkFail, Detail: "inspection unavailable",
			})
		}
		return results
	}
	inspections := deps.InspectMany(ctx, root, paths)
	results := make([]CheckResult, 0, len(paths))
	for index, importPath := range paths {
		result := CheckResult{Name: "node package " + importPath, Status: checkOK, Detail: "compatible"}
		if index >= len(inspections) {
			result.Status = checkFail
			result.Detail = "inspection result missing"
			results = append(results, result)
			continue
		}
		diagnostics := nodepackage.SortDiagnostics(inspections[index].Diagnostics)
		messages := make([]string, 0, len(diagnostics))
		for _, diagnostic := range diagnostics {
			messages = append(messages, diagnostic.Message)
			if diagnostic.Severity == nodepackage.SeverityError {
				result.Status = checkFail
			} else if result.Status == checkOK {
				result.Status = checkWarn
			}
		}
		if len(messages) > 0 {
			result.Detail = strings.Join(messages, "; ")
		}
		results = append(results, result)
	}
	return results
}

func checkVersion(ctx context.Context, deps DoctorDeps, name string, args []string, minimumMajor, minimumMinor int, parse func(string) (int, int, bool)) CheckResult {
	if _, err := deps.LookPath(name); err != nil {
		return CheckResult{Name: name, Status: checkFail, Detail: "not found"}
	}
	output, err := deps.Command(ctx, name, args...)
	if err != nil {
		return CheckResult{Name: name, Status: checkFail, Detail: err.Error()}
	}
	detail := strings.TrimSpace(string(output))
	major, minor, ok := parse(detail)
	if !ok {
		return CheckResult{Name: name, Status: checkFail, Detail: "unrecognized version: " + detail}
	}
	if major < minimumMajor || major == minimumMajor && minor < minimumMinor {
		return CheckResult{Name: name, Status: checkFail, Detail: fmt.Sprintf("%s; require >= %d.%d", detail, minimumMajor, minimumMinor)}
	}
	return CheckResult{Name: name, Status: checkOK, Detail: detail}
}

func checkAvailable(ctx context.Context, deps DoctorDeps, name string, args []string) CheckResult {
	if _, err := deps.LookPath(name); err != nil {
		return CheckResult{Name: name, Status: checkFail, Detail: "not found"}
	}
	output, err := deps.Command(ctx, name, args...)
	if err != nil {
		return CheckResult{Name: name, Status: checkFail, Detail: err.Error()}
	}
	return CheckResult{Name: name, Status: checkOK, Detail: strings.TrimSpace(string(output))}
}

func checkCompose(ctx context.Context, deps DoctorDeps) CheckResult {
	output, err := deps.Command(ctx, "docker", "compose", "version", "--short")
	if err != nil {
		return CheckResult{Name: "docker compose", Status: checkFail, Detail: err.Error()}
	}
	detail := strings.TrimPrefix(strings.TrimSpace(string(output)), "v")
	major, _, ok := dottedVersion(detail)
	if !ok || major < 2 {
		return CheckResult{Name: "docker compose", Status: checkFail, Detail: "require Docker Compose v2; found " + detail}
	}
	return CheckResult{Name: "docker compose", Status: checkOK, Detail: detail}
}

func checkPostgres(ctx context.Context, deps DoctorDeps, dockerReady bool) CheckResult {
	if !dockerReady {
		return CheckResult{Name: "postgres", Status: checkWarn, Detail: "not checked because Docker is unavailable"}
	}
	output, err := deps.Command(ctx, "docker", "compose", "ps", "--status", "running", "--services", "db")
	if err != nil || !containsLine(string(output), "db") {
		return CheckResult{Name: "postgres", Status: checkWarn, Detail: "database container is not running"}
	}
	output, err = deps.Command(ctx, "docker", "compose", "exec", "-T", "db", "pg_isready", "-U", "agent", "-d", "agent_studio")
	if err != nil {
		return CheckResult{Name: "postgres", Status: checkWarn, Detail: "database is not ready"}
	}
	return CheckResult{Name: "postgres", Status: checkOK, Detail: strings.TrimSpace(string(output))}
}

func checkPort(deps DoctorDeps, port string) CheckResult {
	name := "port " + port
	listener, err := deps.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		return CheckResult{Name: name, Status: checkWarn, Detail: "already in use"}
	}
	if err := listener.Close(); err != nil {
		return CheckResult{Name: name, Status: checkWarn, Detail: "probe close failed: " + err.Error()}
	}
	return CheckResult{Name: name, Status: checkOK, Detail: "available"}
}

var goVersionPattern = regexp.MustCompile(`\bgo([0-9]+)\.([0-9]+)`) // go version go1.26.5

func goVersion(value string) (int, int, bool) {
	match := goVersionPattern.FindStringSubmatch(value)
	if len(match) != 3 {
		return 0, 0, false
	}
	return parsedVersion(match[1], match[2])
}

func nodeVersion(value string) (int, int, bool) {
	return dottedVersion(strings.TrimPrefix(strings.TrimSpace(value), "v"))
}

func dottedVersion(value string) (int, int, bool) {
	parts := strings.Split(value, ".")
	if len(parts) < 2 {
		return 0, 0, false
	}
	return parsedVersion(parts[0], parts[1])
}

func parsedVersion(majorValue, minorValue string) (int, int, bool) {
	major, majorErr := strconv.Atoi(majorValue)
	minor, minorErr := strconv.Atoi(minorValue)
	return major, minor, majorErr == nil && minorErr == nil
}

func containsLine(value, want string) bool {
	for _, line := range strings.Split(value, "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}

func defaultDoctorDeps(root string) DoctorDeps {
	inspector := nodepackage.NewInspector(buildinfo.Current())
	return DoctorDeps{
		LookPath: exec.LookPath,
		Command: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			command := exec.CommandContext(ctx, name, args...)
			command.Dir = root
			command.Env = append(os.Environ(), "CGO_ENABLED=0")
			return command.CombinedOutput()
		},
		Listen:      net.Listen,
		ReadFile:    os.ReadFile,
		InspectMany: inspector.InspectMany,
	}
}
