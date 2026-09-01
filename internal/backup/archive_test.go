package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestWriteAndOpenArchiveRoundTrip(t *testing.T) {
	output := filepath.Join(t.TempDir(), "instance.asbak")
	createdAt := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	summary, err := WriteArchive(context.Background(), output, manifestFixture(createdAt), tableWritersFixture([]byte("{\"id\":1}\n")))
	if err != nil {
		t.Fatal(err)
	}
	if summary.Path != output || summary.CompressedBytes <= 0 || len(summary.Tables) != len(TableOrderV1Alpha1) {
		t.Fatalf("summary=%+v", summary)
	}

	archive, err := OpenArchive(context.Background(), output)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	if archive.Summary().DatasetDigest != summary.DatasetDigest {
		t.Fatalf("summary=%+v", archive.Summary())
	}
	var records []json.RawMessage
	if err := archive.ReadTable(context.Background(), TableWorkflows, func(record json.RawMessage) error {
		records = append(records, append(json.RawMessage(nil), record...))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || string(records[0]) != `{"id":1}` {
		t.Fatalf("records=%q", records)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%#o", info.Mode().Perm())
	}
}

func TestWriteArchiveIsDeterministic(t *testing.T) {
	directory := t.TempDir()
	createdAt := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	var contents [][]byte
	for index := 0; index < 2; index++ {
		path := filepath.Join(directory, string(rune('a'+index))+".asbak")
		if _, err := WriteArchive(context.Background(), path, manifestFixture(createdAt), tableWritersFixture([]byte("{}\n"))); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		contents = append(contents, body)
	}
	if !bytes.Equal(contents[0], contents[1]) {
		t.Fatal("fixed input did not produce a byte-identical archive")
	}
}

func TestDatasetDigestUsesFixedTableOrder(t *testing.T) {
	tables := make([]TableManifest, 0, len(TableOrder))
	var canonical strings.Builder
	for index, name := range TableOrder {
		path, _ := tablePath(name)
		digestValue := digestPrefix + strings.Repeat(string(rune('a'+index)), 64)
		tables = append(tables, TableManifest{Name: name, Path: path, Digest: digestValue})
		canonical.WriteString(checksumLine(digestValue, path))
	}
	if got, want := datasetDigest(tables), digestBytes([]byte(canonical.String())); got != want {
		t.Fatalf("dataset digest=%q want=%q", got, want)
	}
}

func TestWriteArchiveRejectsWriterMismatchAndInvalidJSONL(t *testing.T) {
	for _, test := range []struct {
		name   string
		body   []byte
		mutate func(*TableManifest)
		code   Code
	}{
		{name: "missing final newline", body: []byte("{}"), code: CodeArchiveInvalid},
		{name: "trailing json", body: []byte("{} {}\n"), code: CodeArchiveInvalid},
		{name: "wrong count", body: []byte("{}\n"), mutate: func(item *TableManifest) { item.Records++ }, code: CodeArchiveInvalid},
		{name: "wrong bytes", body: []byte("{}\n"), mutate: func(item *TableManifest) { item.UncompressedBytes++ }, code: CodeArchiveInvalid},
		{name: "wrong digest", body: []byte("{}\n"), mutate: func(item *TableManifest) { item.Digest = strings.Repeat("0", 64) }, code: CodeChecksumMismatch},
	} {
		t.Run(test.name, func(t *testing.T) {
			writers := tableWritersFixture(test.body)
			original := writers[TableWorkflows]
			writers[TableWorkflows] = func(ctx context.Context, writer io.Writer) (TableManifest, error) {
				item, err := original(ctx, writer)
				if test.mutate != nil {
					test.mutate(&item)
				}
				return item, err
			}
			_, err := WriteArchive(context.Background(), filepath.Join(t.TempDir(), "bad.asbak"), manifestFixture(time.Now().UTC()), writers)
			if CodeOf(err) != test.code {
				t.Fatalf("code=%q err=%v", CodeOf(err), err)
			}
		})
	}
}

func TestWriteArchiveHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := WriteArchive(ctx, filepath.Join(t.TempDir(), "cancelled.asbak"), manifestFixture(time.Now().UTC()), tableWritersFixture([]byte("{}\n")))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestWriteArchiveCancellationAfterLastTableDoesNotPublish(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	writers := tableWritersFixture([]byte("{}\n"))
	last := TableWorkflowDraftCheckpoints
	original := writers[last]
	writers[last] = func(ctx context.Context, writer io.Writer) (TableManifest, error) {
		manifest, err := original(ctx, writer)
		cancel()
		return manifest, err
	}
	output := filepath.Join(t.TempDir(), "cancelled.asbak")
	_, err := WriteArchive(ctx, output, manifestFixture(time.Now().UTC()), writers)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("output err=%v", statErr)
	}
}

func TestWriteArchiveDoesNotOverwriteExistingOutput(t *testing.T) {
	output := filepath.Join(t.TempDir(), "instance.asbak")
	if err := os.WriteFile(output, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := WriteArchive(context.Background(), output, manifestFixture(time.Now().UTC()), tableWritersFixture([]byte("{}\n")))
	if CodeOf(err) != CodeCreateFailed {
		t.Fatalf("code=%q err=%v", CodeOf(err), err)
	}
	body, readErr := os.ReadFile(output)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(body) != "keep" {
		t.Fatalf("output=%q", body)
	}
}

func TestOpenArchiveRejectsUnsafeEntries(t *testing.T) {
	for _, name := range []string{"/absolute", "../escape", `data\\runs.jsonl`, "data/", "extra.txt"} {
		t.Run(name, func(t *testing.T) {
			path := writeZipFixture(t, []zipFixtureEntry{{name: name, body: []byte("x"), mode: 0o600}})
			_, err := OpenArchive(context.Background(), path)
			if CodeOf(err) != CodeArchiveInvalid {
				t.Fatalf("code=%q err=%v", CodeOf(err), err)
			}
		})
	}
}

func TestOpenArchiveRejectsSymlinkAndNonRegularInput(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, []byte("not zip"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{link, directory} {
		if _, err := OpenArchive(context.Background(), path); CodeOf(err) != CodeArchiveInvalid {
			t.Fatalf("path=%q code=%q err=%v", path, CodeOf(err), err)
		}
	}
}

func TestValidateOpenedArchiveRejectsPathReplacement(t *testing.T) {
	directory := t.TempDir()
	first := filepath.Join(directory, "first")
	second := filepath.Join(directory, "second")
	if err := os.WriteFile(first, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	selected, _ := os.Lstat(first)
	opened, _ := os.Stat(second)
	if err := validateOpenedArchive(selected, opened); CodeOf(err) != CodeArchiveInvalid {
		t.Fatalf("code=%q err=%v", CodeOf(err), err)
	}
}

func TestValidateZipEntriesAcceptsBothVersionCountsAndRejectsDuplicateExtraAndSymlink(t *testing.T) {
	valid := zipEntriesFixture()
	for _, test := range []struct {
		name    string
		entries []*zip.File
	}{
		{name: "duplicate", entries: append(append([]*zip.File(nil), valid[:len(valid)-1]...), valid[0])},
		{name: "extra", entries: append(append([]*zip.File(nil), valid...), zipFileFixture("extra.txt", 0o600))},
		{name: "symlink", entries: replaceZipEntry(valid, 0, zipFileFixture(manifestPath, os.ModeSymlink|0o600))},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := validateZipEntries(test.entries); CodeOf(err) != CodeArchiveInvalid {
				t.Fatalf("code=%q err=%v", CodeOf(err), err)
			}
		})
	}
	if _, err := validateZipEntries(valid[:len(valid)-1]); err != nil {
		t.Fatalf("legacy entry count: %v", err)
	}
}

func TestDecodeManifestRejectsUnknownFieldsAndInvalidDigest(t *testing.T) {
	valid := manifestFixture(time.Now().UTC())
	valid.DatasetDigest = digestPrefix + strings.Repeat("0", 64)
	for _, name := range TableOrderV1Alpha1 {
		path, _ := tablePath(name)
		valid.Tables = append(valid.Tables, TableManifest{Name: name, Path: path, Digest: digestPrefix + strings.Repeat("0", 64)})
	}
	body, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	withUnknown := append(append([]byte(nil), body[:len(body)-1]...), []byte(`,"unknown":true}`)...)
	if _, err := decodeManifest(withUnknown); CodeOf(err) != CodeArchiveInvalid {
		t.Fatalf("unknown field code=%q err=%v", CodeOf(err), err)
	}
	valid.Tables[0].Digest = strings.Repeat("G", 64)
	body, err = json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeManifest(body); CodeOf(err) != CodeArchiveInvalid {
		t.Fatalf("invalid digest code=%q err=%v", CodeOf(err), err)
	}
}

func TestPreflightZIP64RejectsUnsafeDirectoryDeclarations(t *testing.T) {
	for _, test := range []struct {
		name        string
		entries     uint64
		centralSize uint64
		want        Code
	}{
		{name: "too many entries", entries: archiveEntries + 1, centralSize: 1, want: CodeArchiveInvalid},
		{name: "central directory too large", entries: archiveEntries, centralSize: MaxCentralDirectoryBytes + 1, want: CodeSizeLimit},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := zip64DirectoryFixture(test.entries, test.centralSize)
			err := preflightZIPDirectory(bytes.NewReader(body), int64(len(body)))
			if CodeOf(err) != test.want {
				t.Fatalf("code=%q err=%v", CodeOf(err), err)
			}
		})
	}
}

func TestOpenArchiveRejectsPartialTableRead(t *testing.T) {
	output := filepath.Join(t.TempDir(), "instance.asbak")
	if _, err := WriteArchive(context.Background(), output, manifestFixture(time.Now().UTC()), tableWritersFixture([]byte("{\"id\":1}\n"))); err != nil {
		t.Fatal(err)
	}
	archive, err := OpenArchive(context.Background(), output)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	reader, err := archive.OpenTable(TableWorkflows)
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 1)
	if _, err := reader.Read(buffer); err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); CodeOf(err) != CodeArchiveInvalid {
		t.Fatalf("code=%q err=%v", CodeOf(err), err)
	}
}

func TestRecordLimitAcceptsBoundaryAndRejectsOverflow(t *testing.T) {
	for _, test := range []struct {
		name string
		size int
		want Code
	}{
		{name: "boundary", size: MaxRecordBytes},
		{name: "overflow", size: MaxRecordBytes + 1, want: CodeSizeLimit},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := append([]byte(`{"value":"`), bytes.Repeat([]byte("a"), test.size-len(`{"value":""}`))...)
			body = append(body, []byte("\"}\n")...)
			err := verifyJSONL(context.Background(), bytes.NewReader(body), uint64(len(body)), 1, digest(body), nil)
			if CodeOf(err) != test.want {
				t.Fatalf("code=%q err=%v", CodeOf(err), err)
			}
		})
	}
}

func TestVerifyJSONLRejectsActualBytesAtDeclaredBoundary(t *testing.T) {
	declared := []byte("{}\n")
	actual := []byte("{}\n{}\n")
	err := verifyJSONL(context.Background(), bytes.NewReader(actual), uint64(len(declared)), 1, digest(declared), nil)
	if CodeOf(err) != CodeChecksumMismatch {
		t.Fatalf("code=%q err=%v", CodeOf(err), err)
	}
}

func TestOpenArchiveRejectsManifestRecordCountMismatch(t *testing.T) {
	body := []byte("{}\n")
	tables := make([]TableManifest, 0, len(TableOrder))
	entries := make([]zipFixtureEntry, 0, archiveEntries)
	for _, name := range TableOrder {
		path, _ := tablePath(name)
		records := uint64(1)
		if name == TableWorkflows {
			records = 2
		}
		tables = append(tables, TableManifest{Name: name, Path: path, Records: records, UncompressedBytes: uint64(len(body)), Digest: digest(body)})
		entries = append(entries, zipFixtureEntry{name: path, body: body, mode: 0o600})
	}
	manifest := manifestFixture(time.Now().UTC())
	manifest.Tables = tables
	manifest.DatasetDigest = datasetDigest(tables)
	manifestBody, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	checksumLines := append([]string{checksumLine(digest(manifestBody), manifestPath)}, checksumLines(tables)...)
	sort.Slice(checksumLines, func(i, j int) bool { return checksumPath(checksumLines[i]) < checksumPath(checksumLines[j]) })
	checksumBody := []byte(strings.Join(checksumLines, ""))
	entries = append(entries,
		zipFixtureEntry{name: manifestPath, body: manifestBody, mode: 0o600},
		zipFixtureEntry{name: checksumsPath, body: checksumBody, mode: 0o600},
	)
	if _, err := OpenArchive(context.Background(), writeZipFixture(t, entries)); CodeOf(err) != CodeArchiveInvalid {
		t.Fatalf("code=%q err=%v", CodeOf(err), err)
	}
}

func FuzzOpenArchive(f *testing.F) {
	f.Add([]byte("not a zip"))
	f.Add([]byte{'P', 'K', 5, 6, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		path := filepath.Join(t.TempDir(), "input.asbak")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		archive, err := OpenArchive(context.Background(), path)
		if err == nil {
			if err := archive.Close(); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func manifestFixture(createdAt time.Time) Manifest {
	return Manifest{
		APIVersion:               APIVersionV1Alpha1,
		CreatedAt:                createdAt,
		RuntimeVersion:           "0.5.0-test",
		DatabaseMigrationVersion: 6,
		IncludesRuns:             true,
	}
}

func tableWritersFixture(body []byte) map[TableName]TableWriter {
	writers := make(map[TableName]TableWriter, len(TableOrderV1Alpha1))
	for _, name := range TableOrderV1Alpha1 {
		name := name
		writers[name] = func(ctx context.Context, writer io.Writer) (TableManifest, error) {
			if err := ctx.Err(); err != nil {
				return TableManifest{}, err
			}
			if _, err := writer.Write(body); err != nil {
				return TableManifest{}, err
			}
			path, _ := tablePath(name)
			return TableManifest{Name: name, Path: path, Records: 1, UncompressedBytes: uint64(len(body)), Digest: digest(body)}, nil
		}
	}
	return writers
}

func digest(body []byte) string {
	sum := sha256.Sum256(body)
	return digestPrefix + hex.EncodeToString(sum[:])
}

type zipFixtureEntry struct {
	name string
	body []byte
	mode os.FileMode
}

func writeZipFixture(t *testing.T, entries []zipFixtureEntry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.asbak")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		header.SetMode(entry.mode)
		part, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(entry.name, "/") {
			if _, err := part.Write(entry.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func zipEntriesFixture() []*zip.File {
	entries := []*zip.File{zipFileFixture(manifestPath, 0o600), zipFileFixture(checksumsPath, 0o600)}
	for _, name := range TableOrder {
		path, _ := tablePath(name)
		entries = append(entries, zipFileFixture(path, 0o600))
	}
	return entries
}

func zipFileFixture(name string, mode os.FileMode) *zip.File {
	header := zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetMode(mode)
	return &zip.File{FileHeader: header}
}

func replaceZipEntry(entries []*zip.File, index int, replacement *zip.File) []*zip.File {
	result := append([]*zip.File(nil), entries...)
	result[index] = replacement
	return result
}

func zip64DirectoryFixture(entries, centralSize uint64) []byte {
	const recordOffset = 1
	body := make([]byte, recordOffset+56+20+22)
	record := body[recordOffset : recordOffset+56]
	copy(record[:4], []byte{'P', 'K', 6, 6})
	binary.LittleEndian.PutUint64(record[4:12], 44)
	binary.LittleEndian.PutUint64(record[24:32], entries)
	binary.LittleEndian.PutUint64(record[32:40], entries)
	binary.LittleEndian.PutUint64(record[40:48], centralSize)
	binary.LittleEndian.PutUint64(record[48:56], 0)
	locator := body[recordOffset+56 : recordOffset+56+20]
	copy(locator[:4], []byte{'P', 'K', 6, 7})
	binary.LittleEndian.PutUint64(locator[8:16], recordOffset)
	binary.LittleEndian.PutUint32(locator[16:20], 1)
	eocd := body[len(body)-22:]
	copy(eocd[:4], []byte{'P', 'K', 5, 6})
	binary.LittleEndian.PutUint16(eocd[8:10], 0xffff)
	binary.LittleEndian.PutUint16(eocd[10:12], 0xffff)
	binary.LittleEndian.PutUint32(eocd[12:16], 0xffffffff)
	binary.LittleEndian.PutUint32(eocd[16:20], 0xffffffff)
	return body
}
