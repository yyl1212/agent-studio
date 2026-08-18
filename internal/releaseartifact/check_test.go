package releaseartifact

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const (
	fixtureVersion = "v0.2.0-rc.2"
	fixtureCommit  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

type fixtureOptions struct {
	omitArchive     string
	badChecksum     string
	badSBOMChecksum string
	invalidSPDX     string
	unsafePath      string
	wrongOutput     string
	missingFile     string
	nonExec         string
	extraTarget     bool
	extraSBOM       bool
	symlinkArchive  string
}

type fixtureTarget struct {
	goos   string
	goarch string
}

var fixtureTargets = []fixtureTarget{
	{goos: "linux", goarch: "amd64"},
	{goos: "linux", goarch: "arm64"},
	{goos: "darwin", goarch: "amd64"},
	{goos: "darwin", goarch: "arm64"},
}

func TestVerifyCollection(t *testing.T) {
	dist := makeFixture(t, fixtureOptions{})
	if err := VerifyCollection(Config{DistDir: dist, Version: fixtureVersion}); err != nil {
		t.Fatalf("VerifyCollection() error = %v", err)
	}
}

func TestVerifyCollectionRejectsMissingTarget(t *testing.T) {
	dist := makeFixture(t, fixtureOptions{omitArchive: "linux_arm64"})
	err := VerifyCollection(Config{DistDir: dist, Version: fixtureVersion})
	assertErrorContains(t, err, "missing archive")
}

func TestVerifyCollectionRejectsExtraArchive(t *testing.T) {
	dist := makeFixture(t, fixtureOptions{extraTarget: true})
	err := VerifyCollection(Config{DistDir: dist, Version: fixtureVersion})
	assertErrorContains(t, err, "unexpected archive")
}

func TestVerifyCollectionRejectsExtraSBOM(t *testing.T) {
	dist := makeFixture(t, fixtureOptions{extraSBOM: true})
	err := VerifyCollection(Config{DistDir: dist, Version: fixtureVersion})
	assertErrorContains(t, err, "unexpected SBOM")
}

func TestVerifyCollectionRejectsChecksumMismatch(t *testing.T) {
	dist := makeFixture(t, fixtureOptions{badChecksum: "darwin_amd64"})
	err := VerifyCollection(Config{DistDir: dist, Version: fixtureVersion})
	assertErrorContains(t, err, "checksum mismatch")
}

func TestVerifyCollectionRejectsSBOMChecksumMismatch(t *testing.T) {
	dist := makeFixture(t, fixtureOptions{badSBOMChecksum: "darwin_amd64"})
	err := VerifyCollection(Config{DistDir: dist, Version: fixtureVersion})
	assertErrorContains(t, err, "checksum mismatch")
}

func TestVerifyCollectionRejectsInvalidSPDX(t *testing.T) {
	dist := makeFixture(t, fixtureOptions{invalidSPDX: "linux_amd64"})
	err := VerifyCollection(Config{DistDir: dist, Version: fixtureVersion})
	assertErrorContains(t, err, "invalid SPDX JSON")
}

func TestVerifyCollectionRejectsSymlinkArchive(t *testing.T) {
	dist := makeFixture(t, fixtureOptions{symlinkArchive: "linux_amd64"})
	err := VerifyCollection(Config{DistDir: dist, Version: fixtureVersion})
	assertErrorContains(t, err, "archive is not a regular file")
}

func TestVerifyTargetExecutesVersionCommand(t *testing.T) {
	dist := makeFixture(t, fixtureOptions{})
	err := VerifyTarget(Config{
		DistDir: dist,
		Version: fixtureVersion,
		GOOS:    "linux",
		GOARCH:  "amd64",
		Commit:  fixtureCommit,
	})
	if err != nil {
		t.Fatalf("VerifyTarget() error = %v", err)
	}
}

func TestVerifyTargetRejectsUnsafeArchivePath(t *testing.T) {
	dist := makeFixture(t, fixtureOptions{unsafePath: "linux_amd64"})
	err := VerifyTarget(Config{
		DistDir: dist,
		Version: fixtureVersion,
		GOOS:    "linux",
		GOARCH:  "amd64",
		Commit:  fixtureCommit,
	})
	assertErrorContains(t, err, "unsafe archive path")
}

func TestVerifyTargetRejectsWrongVersionOutput(t *testing.T) {
	dist := makeFixture(t, fixtureOptions{wrongOutput: "linux_amd64"})
	err := VerifyTarget(Config{
		DistDir: dist,
		Version: fixtureVersion,
		GOOS:    "linux",
		GOARCH:  "amd64",
		Commit:  fixtureCommit,
	})
	assertErrorContains(t, err, "version output mismatch")
}

func TestVerifyTargetRejectsMissingArchiveMember(t *testing.T) {
	dist := makeFixture(t, fixtureOptions{missingFile: "linux_amd64"})
	err := VerifyTarget(Config{
		DistDir: dist,
		Version: fixtureVersion,
		GOOS:    "linux",
		GOARCH:  "amd64",
		Commit:  fixtureCommit,
	})
	assertErrorContains(t, err, "archive members mismatch")
}

func TestVerifyTargetRejectsNonExecutableCLI(t *testing.T) {
	dist := makeFixture(t, fixtureOptions{nonExec: "linux_amd64"})
	err := VerifyTarget(Config{
		DistDir: dist,
		Version: fixtureVersion,
		GOOS:    "linux",
		GOARCH:  "amd64",
		Commit:  fixtureCommit,
	})
	assertErrorContains(t, err, "agent-studio is not executable")
}

func assertErrorContains(t *testing.T, err error, expected string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), expected) {
		t.Fatalf("error = %v, want substring %q", err, expected)
	}
}

func makeFixture(t *testing.T, options fixtureOptions) string {
	t.Helper()
	dist := t.TempDir()
	checksums := make([]string, 0, len(fixtureTargets))

	for _, target := range fixtureTargets {
		key := target.goos + "_" + target.goarch
		if options.omitArchive == key {
			continue
		}
		archiveName := fmt.Sprintf("agent-studio_%s_%s_%s.tar.gz", fixtureVersion, target.goos, target.goarch)
		archivePath := filepath.Join(dist, archiveName)
		writeArchive(t, archivePath, key, options)
		if options.symlinkArchive == key {
			payloadPath := archivePath + ".payload"
			if err := os.Rename(archivePath, payloadPath); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Base(payloadPath), archivePath); err != nil {
				t.Fatal(err)
			}
		}
		digest := fileDigest(t, archivePath)
		if options.badChecksum == key {
			digest = strings.Repeat("0", 64)
		}
		checksums = append(checksums, digest+"  "+archiveName)
		sbomPath := archivePath + ".spdx.json"
		writeSPDX(t, sbomPath, archiveName, options.invalidSPDX == key)
		sbomDigest := fileDigest(t, sbomPath)
		if options.badSBOMChecksum == key {
			sbomDigest = strings.Repeat("0", 64)
		}
		checksums = append(checksums, sbomDigest+"  "+filepath.Base(sbomPath))
	}

	if options.extraTarget {
		extraName := "agent-studio_" + fixtureVersion + "_windows_amd64.tar.gz"
		writeArchive(t, filepath.Join(dist, extraName), "windows_amd64", fixtureOptions{})
	}
	if options.extraSBOM {
		writeSPDX(t, filepath.Join(dist, "agent-studio_"+fixtureVersion+"_windows_amd64.tar.gz.spdx.json"), "extra", false)
	}

	sort.Strings(checksums)
	if err := os.WriteFile(filepath.Join(dist, "checksums.txt"), []byte(strings.Join(checksums, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dist
}

func writeArchive(t *testing.T, archivePath, key string, options fixtureOptions) {
	t.Helper()
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)

	versionOutput := fmt.Sprintf(
		"agent-studio %s (sdk 0.2.0; api agent-studio.dev/v1alpha1; commit %s; dirty false)",
		fixtureVersion,
		fixtureCommit,
	)
	if options.wrongOutput == key {
		versionOutput = "agent-studio v9.9.9"
	}
	mode := int64(0o755)
	if options.nonExec == key {
		mode = 0o644
	}
	members := []struct {
		name    string
		mode    int64
		content string
	}{
		{name: "agent-studio", mode: mode, content: "#!/bin/sh\nprintf '%s\\n' '" + versionOutput + "'\n"},
		{name: "README.md", mode: 0o644, content: "# Agent Studio\n"},
		{name: "LICENSE", mode: 0o644, content: "Apache-2.0\n"},
	}
	if options.missingFile == key {
		members = members[:2]
	}
	if options.unsafePath == key {
		members = append(members, struct {
			name    string
			mode    int64
			content string
		}{name: "../escape", mode: 0o644, content: "escape\n"})
	}

	for _, member := range members {
		header := &tar.Header{
			Name: member.name,
			Mode: member.mode,
			Size: int64(len(member.content)),
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write([]byte(member.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeSPDX(t *testing.T, path, archiveName string, invalid bool) {
	t.Helper()
	content := fmt.Sprintf("{\"spdxVersion\":\"SPDX-2.3\",\"name\":%q}\n", archiveName)
	if invalid {
		content = "{\"spdxVersion\":\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fileDigest(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(content))
}
