package releaseartifact

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type Config struct {
	DistDir string
	Version string
	GOOS    string
	GOARCH  string
	Commit  string
}

type target struct {
	GOOS   string
	GOARCH string
}

var supportedTargets = []target{
	{GOOS: "linux", GOARCH: "amd64"},
	{GOOS: "linux", GOARCH: "arm64"},
	{GOOS: "darwin", GOARCH: "amd64"},
	{GOOS: "darwin", GOARCH: "arm64"},
}

var checksumLinePattern = regexp.MustCompile(`^([[:xdigit:]]{64})  ([^/\\]+)$`)

func VerifyCollection(config Config) error {
	if config.DistDir == "" {
		return errors.New("dist directory is required")
	}
	if config.Version == "" {
		return errors.New("version is required")
	}

	expectedArchives := make(map[string]struct{}, len(supportedTargets))
	expectedSBOMs := make(map[string]struct{}, len(supportedTargets))
	expectedChecksums := make(map[string]struct{}, len(supportedTargets)*2)
	archiveNames := make([]string, 0, len(supportedTargets))
	for _, target := range supportedTargets {
		archiveName := archiveName(config.Version, target.GOOS, target.GOARCH)
		sbomName := archiveName + ".spdx.json"
		expectedArchives[archiveName] = struct{}{}
		expectedSBOMs[sbomName] = struct{}{}
		expectedChecksums[archiveName] = struct{}{}
		expectedChecksums[sbomName] = struct{}{}
		archiveNames = append(archiveNames, archiveName)
	}

	entries, err := os.ReadDir(config.DistDir)
	if err != nil {
		return fmt.Errorf("read dist directory: %w", err)
	}
	actualArchives := make(map[string]struct{})
	actualSBOMs := make(map[string]struct{})
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		switch {
		case strings.HasSuffix(name, ".tar.gz"):
			if err := requireRegularEntry(entry, "archive"); err != nil {
				return err
			}
			actualArchives[name] = struct{}{}
		case strings.HasSuffix(name, ".tar.gz.spdx.json"):
			if err := requireRegularEntry(entry, "SBOM"); err != nil {
				return err
			}
			actualSBOMs[name] = struct{}{}
		}
	}
	if err := compareFileSet("archive", expectedArchives, actualArchives); err != nil {
		return err
	}
	if err := compareFileSet("SBOM", expectedSBOMs, actualSBOMs); err != nil {
		return err
	}

	checksumPath := filepath.Join(config.DistDir, "checksums.txt")
	if err := requireRegularFile(checksumPath, "checksums.txt"); err != nil {
		return err
	}
	checksums, err := readChecksums(checksumPath)
	if err != nil {
		return err
	}
	if err := compareFileSet("checksum entry", expectedChecksums, checksumKeys(checksums)); err != nil {
		return err
	}

	sort.Strings(archiveNames)
	for _, archiveName := range archiveNames {
		sbomName := archiveName + ".spdx.json"
		for _, fileName := range []string{archiveName, sbomName} {
			if err := verifyDigest(filepath.Join(config.DistDir, fileName), fileName, checksums[fileName]); err != nil {
				return err
			}
		}
		if err := verifySPDX(filepath.Join(config.DistDir, sbomName), archiveName); err != nil {
			return err
		}
	}
	return nil
}

func verifyDigest(filePath, fileName string, expectedDigest []byte) error {
	actualDigest, err := digestFile(filePath)
	if err != nil {
		return fmt.Errorf("hash release file %s: %w", fileName, err)
	}
	if subtle.ConstantTimeCompare(actualDigest, expectedDigest) != 1 {
		return fmt.Errorf("checksum mismatch for %s", fileName)
	}
	return nil
}

func requireRegularEntry(entry os.DirEntry, kind string) error {
	info, err := entry.Info()
	if err != nil {
		return fmt.Errorf("inspect %s %s: %w", kind, entry.Name(), err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file: %s", kind, entry.Name())
	}
	return nil
}

func requireRegularFile(filePath, kind string) error {
	info, err := os.Lstat(filePath)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", kind, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", kind)
	}
	return nil
}

func VerifyTarget(config Config) error {
	if err := VerifyCollection(config); err != nil {
		return err
	}
	if !isSupportedTarget(config.GOOS, config.GOARCH) {
		return fmt.Errorf("unsupported target %s/%s", config.GOOS, config.GOARCH)
	}
	if config.Commit == "" {
		return errors.New("commit is required")
	}

	archivePath := filepath.Join(config.DistDir, archiveName(config.Version, config.GOOS, config.GOARCH))
	tempDir, err := os.MkdirTemp("", "agent-studio-release-artifact-")
	if err != nil {
		return fmt.Errorf("create extraction directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	if err := extractTarget(archivePath, tempDir); err != nil {
		return err
	}
	cliPath := filepath.Join(tempDir, "agent-studio")
	info, err := os.Stat(cliPath)
	if err != nil {
		return fmt.Errorf("stat agent-studio: %w", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return errors.New("agent-studio is not executable")
	}

	command := exec.Command(cliPath, "version")
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("execute agent-studio version: %w: %s", err, strings.TrimSpace(string(output)))
	}
	actual := strings.TrimSpace(string(output))
	expected := fmt.Sprintf(
		"agent-studio %s (sdk 0.2.0; api agent-studio.dev/v1alpha1; commit %s; dirty false)",
		config.Version,
		config.Commit,
	)
	if actual != expected {
		return fmt.Errorf("version output mismatch: got %q want %q", actual, expected)
	}
	return nil
}

func compareFileSet(kind string, expected, actual map[string]struct{}) error {
	expectedNames := sortedSetKeys(expected)
	for _, name := range expectedNames {
		if _, ok := actual[name]; !ok {
			return fmt.Errorf("missing %s %s", kind, name)
		}
	}
	actualNames := sortedSetKeys(actual)
	for _, name := range actualNames {
		if _, ok := expected[name]; !ok {
			return fmt.Errorf("unexpected %s %s", kind, name)
		}
	}
	return nil
}

func sortedSetKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func checksumKeys(checksums map[string][]byte) map[string]struct{} {
	keys := make(map[string]struct{}, len(checksums))
	for name := range checksums {
		keys[name] = struct{}{}
	}
	return keys
}

func readChecksums(checksumPath string) (map[string][]byte, error) {
	file, err := os.Open(checksumPath)
	if err != nil {
		return nil, fmt.Errorf("open checksums.txt: %w", err)
	}
	defer file.Close()

	checksums := make(map[string][]byte)
	scanner := bufio.NewScanner(file)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		matches := checksumLinePattern.FindStringSubmatch(scanner.Text())
		if matches == nil {
			return nil, fmt.Errorf("invalid checksums.txt line %d", lineNumber)
		}
		if _, exists := checksums[matches[2]]; exists {
			return nil, fmt.Errorf("duplicate checksum entry %s", matches[2])
		}
		digest, err := hex.DecodeString(matches[1])
		if err != nil {
			return nil, fmt.Errorf("decode checksum for %s: %w", matches[2], err)
		}
		checksums[matches[2]] = digest
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read checksums.txt: %w", err)
	}
	return checksums, nil
}

func digestFile(filePath string) ([]byte, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return nil, err
	}
	return hash.Sum(nil), nil
}

func verifySPDX(sbomPath, archiveName string) error {
	file, err := os.Open(sbomPath)
	if err != nil {
		return fmt.Errorf("open SPDX for %s: %w", archiveName, err)
	}
	defer file.Close()

	var document struct {
		SPDXVersion string `json:"spdxVersion"`
		Name        string `json:"name"`
	}
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&document); err != nil {
		return fmt.Errorf("invalid SPDX JSON for %s: %w", archiveName, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("invalid SPDX JSON for %s: trailing JSON", archiveName)
		}
		return fmt.Errorf("invalid SPDX JSON for %s: %w", archiveName, err)
	}
	if !strings.HasPrefix(document.SPDXVersion, "SPDX-") {
		return fmt.Errorf("invalid SPDX version for %s: %q", archiveName, document.SPDXVersion)
	}
	if document.Name != archiveName {
		return fmt.Errorf("SPDX name mismatch for %s: got %q", archiveName, document.Name)
	}
	return nil
}

func extractTarget(archivePath, destination string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open gzip archive: %w", err)
	}
	defer gzipReader.Close()

	expectedMembers := map[string]struct{}{
		"agent-studio": {},
		"README.md":    {},
		"LICENSE":      {},
	}
	seen := make(map[string]struct{}, len(expectedMembers))
	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		clean := path.Clean(header.Name)
		if clean != header.Name || clean == "." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
			return fmt.Errorf("unsafe archive path %q", header.Name)
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return fmt.Errorf("archive member %q is not a regular file", header.Name)
		}
		if _, ok := expectedMembers[header.Name]; !ok {
			return fmt.Errorf("archive members mismatch: unexpected %q", header.Name)
		}
		if _, duplicate := seen[header.Name]; duplicate {
			return fmt.Errorf("archive members mismatch: duplicate %q", header.Name)
		}
		seen[header.Name] = struct{}{}

		outputPath := filepath.Join(destination, header.Name)
		output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(header.Mode)&0o777)
		if err != nil {
			return fmt.Errorf("create archive member %q: %w", header.Name, err)
		}
		_, copyErr := io.Copy(output, tarReader)
		closeErr := output.Close()
		if copyErr != nil {
			return fmt.Errorf("extract archive member %q: %w", header.Name, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close archive member %q: %w", header.Name, closeErr)
		}
	}
	if err := compareFileSet("archive member", expectedMembers, seen); err != nil {
		return fmt.Errorf("archive members mismatch: %w", err)
	}
	return nil
}

func archiveName(version, goos, goarch string) string {
	return fmt.Sprintf("agent-studio_%s_%s_%s.tar.gz", version, goos, goarch)
}

func isSupportedTarget(goos, goarch string) bool {
	for _, target := range supportedTargets {
		if target.GOOS == goos && target.GOARCH == goarch {
			return true
		}
	}
	return false
}
