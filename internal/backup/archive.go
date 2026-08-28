package backup

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	manifestPath   = "manifest.json"
	checksumsPath  = "checksums.txt"
	archiveEntries = 2 + 6
)

var runtimeVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$`)

type TableWriter func(context.Context, io.Writer) (TableManifest, error)

func WriteArchive(ctx context.Context, output string, base Manifest, writers map[TableName]TableWriter) (Summary, error) {
	if err := ctx.Err(); err != nil {
		return Summary{}, err
	}
	if err := validateBaseManifest(base); err != nil {
		return Summary{}, err
	}
	if len(writers) != len(TableOrder) {
		return Summary{}, Wrap(CodeArchiveInvalid, "validate table writers", nil)
	}

	var result Manifest
	err := publishAtomicContext(ctx, output, func(file *os.File) error {
		zipWriter := zip.NewWriter(file)
		closed := false
		defer func() {
			if !closed {
				_ = zipWriter.Close()
			}
		}()

		result = base
		result.CreatedAt = result.CreatedAt.UTC()
		result.Tables = make([]TableManifest, 0, len(TableOrder))
		var total uint64
		for _, name := range TableOrder {
			if err := ctx.Err(); err != nil {
				return err
			}
			writer, ok := writers[name]
			if !ok || writer == nil {
				return Wrap(CodeArchiveInvalid, "find table writer", nil)
			}
			path, _ := tablePath(name)
			part, err := createZipPart(zipWriter, path, result.CreatedAt)
			if err != nil {
				return err
			}
			observed := newObservingWriter(ctx, part)
			declared, err := writer(ctx, observed)
			if err != nil {
				return err
			}
			if err := observed.finish(); err != nil {
				return err
			}
			actual := TableManifest{
				Name: name, Path: path, Records: observed.records,
				UncompressedBytes: observed.bytes, Digest: observed.digest(),
			}
			if declared.Name != actual.Name || declared.Path != actual.Path || declared.Records != actual.Records || declared.UncompressedBytes != actual.UncompressedBytes {
				return Wrap(CodeArchiveInvalid, "validate table writer result", nil)
			}
			if declared.Digest != actual.Digest {
				return Wrap(CodeChecksumMismatch, "validate table writer digest", nil)
			}
			if actual.UncompressedBytes > MaxArchiveBytes || total > MaxArchiveBytes-actual.UncompressedBytes {
				return Wrap(CodeSizeLimit, "validate archive size", nil)
			}
			total += actual.UncompressedBytes
			result.Tables = append(result.Tables, actual)
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		dataLines := checksumLines(result.Tables)
		result.DatasetDigest = datasetDigest(result.Tables)
		if err := validateManifest(result); err != nil {
			return err
		}
		manifestBody, err := json.Marshal(result)
		if err != nil {
			return Wrap(CodeCreateFailed, "encode backup manifest", err)
		}
		if len(manifestBody) > MaxManifestBytes {
			return Wrap(CodeSizeLimit, "validate manifest size", nil)
		}
		manifestPart, err := createZipPart(zipWriter, manifestPath, result.CreatedAt)
		if err != nil {
			return err
		}
		if _, err := manifestPart.Write(manifestBody); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		allLines := append([]string{checksumLine(digestBytes(manifestBody), manifestPath)}, dataLines...)
		sort.Slice(allLines, func(i, j int) bool { return checksumPath(allLines[i]) < checksumPath(allLines[j]) })
		checksumsBody := []byte(strings.Join(allLines, ""))
		if len(checksumsBody) > MaxChecksumsBytes {
			return Wrap(CodeSizeLimit, "validate checksums size", nil)
		}
		metadataBytes := uint64(len(manifestBody) + len(checksumsBody))
		if total > MaxArchiveBytes-metadataBytes {
			return Wrap(CodeSizeLimit, "validate archive size", nil)
		}
		checksumsPart, err := createZipPart(zipWriter, checksumsPath, result.CreatedAt)
		if err != nil {
			return err
		}
		if _, err := checksumsPart.Write(checksumsBody); err != nil {
			return err
		}
		if err := zipWriter.Close(); err != nil {
			return err
		}
		closed = true
		if err := ctx.Err(); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if CodeOf(err) != "" || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return Summary{}, err
		}
		return Summary{}, Wrap(CodeCreateFailed, "publish backup archive", err)
	}
	info, err := os.Stat(output)
	if err != nil {
		return Summary{}, Wrap(CodeCreateFailed, "inspect backup output", err)
	}
	return summaryFromManifest(output, info.Size(), result), nil
}

func createZipPart(writer *zip.Writer, name string, modified time.Time) (io.Writer, error) {
	header := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: modified.UTC()}
	header.SetMode(0o600)
	part, err := writer.CreateHeader(header)
	if err != nil {
		return nil, Wrap(CodeCreateFailed, "create backup archive entry", err)
	}
	return part, nil
}

type observingWriter struct {
	ctx         context.Context
	destination io.Writer
	hash        hash.Hash
	bytes       uint64
	records     uint64
	line        []byte
}

func newObservingWriter(ctx context.Context, destination io.Writer) *observingWriter {
	return &observingWriter{ctx: ctx, destination: destination, hash: sha256.New(), line: make([]byte, 0, 4096)}
}

func (writer *observingWriter) Write(body []byte) (int, error) {
	if err := writer.ctx.Err(); err != nil {
		return 0, err
	}
	if uint64(len(body)) > MaxArchiveBytes-writer.bytes {
		return 0, Wrap(CodeSizeLimit, "write backup table", nil)
	}
	for _, value := range body {
		writer.line = append(writer.line, value)
		if len(writer.line) > MaxRecordBytes+1 {
			return 0, Wrap(CodeSizeLimit, "validate backup record size", nil)
		}
		if value == '\n' {
			if err := validateJSONObject(writer.line[:len(writer.line)-1]); err != nil {
				return 0, err
			}
			writer.records++
			writer.line = writer.line[:0]
		}
	}
	written, err := writer.destination.Write(body)
	if written > 0 {
		_, _ = writer.hash.Write(body[:written])
		writer.bytes += uint64(written)
	}
	return written, err
}

func (writer *observingWriter) finish() error {
	if len(writer.line) != 0 {
		return Wrap(CodeArchiveInvalid, "validate final backup record newline", nil)
	}
	return nil
}

func (writer *observingWriter) digest() string {
	return digestPrefix + hex.EncodeToString(writer.hash.Sum(nil))
}

type Archive struct {
	file     *os.File
	reader   *zip.Reader
	manifest Manifest
	files    map[string]*zip.File
	summary  Summary
}

func OpenArchive(ctx context.Context, path string) (*Archive, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, Wrap(CodeArchiveInvalid, "open backup archive", err)
	}
	if !info.Mode().IsRegular() {
		return nil, Wrap(CodeArchiveInvalid, "validate backup archive file", nil)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, Wrap(CodeArchiveInvalid, "open backup archive", err)
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = file.Close()
		}
	}()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, Wrap(CodeArchiveInvalid, "inspect opened backup archive", err)
	}
	if err := validateOpenedArchive(info, openedInfo); err != nil {
		return nil, err
	}
	if err := preflightZIPDirectory(file, openedInfo.Size()); err != nil {
		return nil, err
	}
	reader, err := zip.NewReader(file, openedInfo.Size())
	if err != nil {
		return nil, Wrap(CodeArchiveInvalid, "parse backup archive", err)
	}
	files, err := validateZipEntries(reader.File)
	if err != nil {
		return nil, err
	}
	manifestBody, err := readZipPart(files[manifestPath], MaxManifestBytes)
	if err != nil {
		return nil, err
	}
	manifest, err := decodeManifest(manifestBody)
	if err != nil {
		return nil, err
	}
	checksumsBody, err := readZipPart(files[checksumsPath], MaxChecksumsBytes)
	if err != nil {
		return nil, err
	}
	if err := validateChecksums(manifest, manifestBody, checksumsBody); err != nil {
		return nil, err
	}
	for _, table := range manifest.Tables {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		part, err := files[table.Path].Open()
		if err != nil {
			return nil, Wrap(CodeArchiveInvalid, "open backup table", err)
		}
		err = verifyJSONL(ctx, part, table.UncompressedBytes, table.Records, table.Digest, nil)
		closeErr := part.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, Wrap(CodeArchiveInvalid, "close backup table", closeErr)
		}
	}
	archive := &Archive{
		file: file, reader: reader, manifest: manifest, files: files,
		summary: summaryFromManifest(path, openedInfo.Size(), manifest),
	}
	closeOnError = false
	return archive, nil
}

func validateOpenedArchive(selected, opened os.FileInfo) error {
	if !opened.Mode().IsRegular() || !os.SameFile(selected, opened) {
		return Wrap(CodeArchiveInvalid, "validate opened backup archive", nil)
	}
	return nil
}

func (archive *Archive) Summary() Summary {
	result := archive.summary
	result.Tables = append([]TableManifest(nil), archive.summary.Tables...)
	return result
}

func (archive *Archive) OpenTable(name TableName) (io.ReadCloser, error) {
	path, err := tablePath(name)
	if err != nil {
		return nil, Wrap(CodeArchiveInvalid, "select backup table", err)
	}
	var expected *TableManifest
	for index := range archive.manifest.Tables {
		if archive.manifest.Tables[index].Name == name {
			expected = &archive.manifest.Tables[index]
			break
		}
	}
	if expected == nil {
		return nil, Wrap(CodeArchiveInvalid, "find backup table", nil)
	}
	part, err := archive.files[path].Open()
	if err != nil {
		return nil, Wrap(CodeArchiveInvalid, "open backup table", err)
	}
	return &verifiedTableReadCloser{source: part, expectedBytes: expected.UncompressedBytes, expectedDigest: expected.Digest, hash: sha256.New()}, nil
}

func (archive *Archive) ReadTable(ctx context.Context, name TableName, visit func(json.RawMessage) error) error {
	reader, err := archive.OpenTable(name)
	if err != nil {
		return err
	}
	err = verifyJSONL(ctx, reader, archive.table(name).UncompressedBytes, archive.table(name).Records, archive.table(name).Digest, visit)
	closeErr := reader.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func (archive *Archive) table(name TableName) TableManifest {
	for _, table := range archive.manifest.Tables {
		if table.Name == name {
			return table
		}
	}
	return TableManifest{}
}

func (archive *Archive) Close() error { return archive.file.Close() }

type verifiedTableReadCloser struct {
	source         io.ReadCloser
	hash           hash.Hash
	bytes          uint64
	expectedBytes  uint64
	expectedDigest string
	verified       bool
}

func (reader *verifiedTableReadCloser) Read(body []byte) (int, error) {
	count, err := reader.source.Read(body)
	if count > 0 {
		_, _ = reader.hash.Write(body[:count])
		reader.bytes += uint64(count)
		if reader.bytes > reader.expectedBytes {
			return count, Wrap(CodeChecksumMismatch, "verify backup table size", nil)
		}
	}
	if err == io.EOF {
		if reader.bytes != reader.expectedBytes || digestPrefix+hex.EncodeToString(reader.hash.Sum(nil)) != reader.expectedDigest {
			return count, Wrap(CodeChecksumMismatch, "verify backup table content", nil)
		}
		reader.verified = true
	} else if err != nil {
		return count, Wrap(CodeChecksumMismatch, "read backup table", err)
	}
	return count, err
}

func (reader *verifiedTableReadCloser) Close() error {
	closeErr := reader.source.Close()
	if !reader.verified {
		return Wrap(CodeArchiveInvalid, "close partially read backup table", closeErr)
	}
	if closeErr != nil {
		return Wrap(CodeArchiveInvalid, "close backup table", closeErr)
	}
	return nil
}

func verifyJSONL(ctx context.Context, source io.Reader, expectedBytes, expectedRecords uint64, expectedDigest string, visit func(json.RawMessage) error) error {
	hasher := sha256.New()
	counting := &countingReader{source: source, limit: expectedBytes}
	reader := bufio.NewReaderSize(io.TeeReader(counting, hasher), 64<<10)
	var records uint64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, eof, err := readBoundedLine(reader)
		if err != nil {
			return err
		}
		if eof && len(line) == 0 {
			break
		}
		if len(line) == 0 || line[len(line)-1] != '\n' {
			return Wrap(CodeArchiveInvalid, "validate final backup record newline", nil)
		}
		record := line[:len(line)-1]
		if err := validateJSONObject(record); err != nil {
			return err
		}
		records++
		if visit != nil {
			if err := visit(json.RawMessage(record)); err != nil {
				return err
			}
		}
		if eof {
			break
		}
	}
	if counting.bytes != expectedBytes || records != expectedRecords {
		return Wrap(CodeArchiveInvalid, "validate backup table metadata", nil)
	}
	if digestPrefix+hex.EncodeToString(hasher.Sum(nil)) != expectedDigest {
		return Wrap(CodeChecksumMismatch, "validate backup table digest", nil)
	}
	return nil
}

type countingReader struct {
	source io.Reader
	bytes  uint64
	limit  uint64
}

func (reader *countingReader) Read(body []byte) (int, error) {
	if reader.bytes > reader.limit {
		return 0, Wrap(CodeChecksumMismatch, "validate backup table size", nil)
	}
	remaining := reader.limit - reader.bytes
	if uint64(len(body)) > remaining+1 {
		body = body[:remaining+1]
	}
	count, err := reader.source.Read(body)
	reader.bytes += uint64(count)
	if reader.bytes > reader.limit {
		return count, Wrap(CodeChecksumMismatch, "validate backup table size", nil)
	}
	return count, err
}

func readBoundedLine(reader *bufio.Reader) ([]byte, bool, error) {
	line := make([]byte, 0, 4096)
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(line)+len(fragment) > MaxRecordBytes+1 {
			return nil, false, Wrap(CodeSizeLimit, "validate backup record size", nil)
		}
		line = append(line, fragment...)
		switch {
		case err == nil:
			return line, false, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return line, true, nil
		default:
			if CodeOf(err) != "" {
				return nil, false, err
			}
			return nil, false, Wrap(CodeArchiveInvalid, "read backup record", err)
		}
	}
}

func validateJSONObject(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil || object == nil {
		return Wrap(CodeArchiveInvalid, "decode backup record", err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Wrap(CodeArchiveInvalid, "validate backup record boundary", err)
	}
	return nil
}

func validateBaseManifest(manifest Manifest) error {
	if len(manifest.Tables) != 0 || manifest.DatasetDigest != "" {
		return Wrap(CodeArchiveInvalid, "validate base manifest", nil)
	}
	copy := manifest
	copy.Tables = make([]TableManifest, len(TableOrder))
	for index, name := range TableOrder {
		path, _ := tablePath(name)
		copy.Tables[index] = TableManifest{Name: name, Path: path, Digest: digestPrefix + strings.Repeat("0", 64)}
	}
	copy.DatasetDigest = digestPrefix + strings.Repeat("0", 64)
	return validateManifest(copy)
}

func validateManifest(manifest Manifest) error {
	if manifest.APIVersion != APIVersion {
		return Wrap(CodeFormatUnsupported, "validate backup api version", nil)
	}
	if manifest.CreatedAt.IsZero() || manifest.CreatedAt.Location() != time.UTC {
		return Wrap(CodeArchiveInvalid, "validate backup creation time", nil)
	}
	if !runtimeVersionPattern.MatchString(manifest.RuntimeVersion) || manifest.DatabaseMigrationVersion <= 0 || !manifest.IncludesRuns {
		return Wrap(CodeArchiveInvalid, "validate backup manifest metadata", nil)
	}
	if !canonicalDigest(manifest.DatasetDigest) || len(manifest.Tables) != len(TableOrder) {
		return Wrap(CodeArchiveInvalid, "validate backup manifest tables", nil)
	}
	var total uint64
	for index, name := range TableOrder {
		table := manifest.Tables[index]
		path, _ := tablePath(name)
		if table.Name != name || table.Path != path || !canonicalDigest(table.Digest) {
			return Wrap(CodeArchiveInvalid, "validate backup table manifest", nil)
		}
		if table.UncompressedBytes > MaxArchiveBytes || total > MaxArchiveBytes-table.UncompressedBytes {
			return Wrap(CodeSizeLimit, "validate archive size", nil)
		}
		total += table.UncompressedBytes
	}
	return nil
}

func decodeManifest(body []byte) (Manifest, error) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(body, &keys); err != nil {
		return Manifest{}, Wrap(CodeArchiveInvalid, "decode backup manifest", err)
	}
	want := []string{"apiVersion", "createdAt", "runtimeVersion", "databaseMigrationVersion", "includesRuns", "datasetDigest", "tables"}
	if len(keys) != len(want) {
		return Manifest{}, Wrap(CodeArchiveInvalid, "validate backup manifest fields", nil)
	}
	for _, key := range want {
		if _, ok := keys[key]; !ok {
			return Manifest{}, Wrap(CodeArchiveInvalid, "validate backup manifest fields", nil)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, Wrap(CodeArchiveInvalid, "decode backup manifest", err)
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func validateChecksums(manifest Manifest, manifestBody, body []byte) error {
	if len(body) == 0 || body[len(body)-1] != '\n' {
		return Wrap(CodeArchiveInvalid, "validate backup checksums newline", nil)
	}
	lines := strings.Split(string(body[:len(body)-1]), "\n")
	if len(lines) != archiveEntries-1 {
		return Wrap(CodeArchiveInvalid, "validate backup checksum count", nil)
	}
	actual := make(map[string]string, len(lines))
	previous := ""
	for _, line := range lines {
		if len(line) < 67 || line[64:66] != "  " || !canonicalDigestHex(line[:64]) {
			return Wrap(CodeArchiveInvalid, "parse backup checksum", nil)
		}
		path := line[66:]
		if path == "" || path <= previous {
			return Wrap(CodeArchiveInvalid, "validate backup checksum order", nil)
		}
		previous = path
		if _, exists := actual[path]; exists {
			return Wrap(CodeArchiveInvalid, "validate backup checksum paths", nil)
		}
		actual[path] = line[:64]
	}
	expected := map[string]string{manifestPath: digestHex(manifestBody)}
	for _, table := range manifest.Tables {
		expected[table.Path] = strings.TrimPrefix(table.Digest, digestPrefix)
	}
	if len(actual) != len(expected) {
		return Wrap(CodeArchiveInvalid, "validate backup checksum paths", nil)
	}
	for path, digest := range expected {
		if actual[path] != digest {
			return Wrap(CodeChecksumMismatch, "validate backup checksum", nil)
		}
	}
	if got := datasetDigest(manifest.Tables); got != manifest.DatasetDigest {
		return Wrap(CodeChecksumMismatch, "validate backup dataset digest", nil)
	}
	return nil
}

func checksumLines(tables []TableManifest) []string {
	lines := make([]string, 0, len(tables))
	for _, table := range tables {
		lines = append(lines, checksumLine(table.Digest, table.Path))
	}
	sort.Slice(lines, func(i, j int) bool { return checksumPath(lines[i]) < checksumPath(lines[j]) })
	return lines
}

func datasetDigest(tables []TableManifest) string {
	var canonical strings.Builder
	for _, table := range tables {
		canonical.WriteString(checksumLine(table.Digest, table.Path))
	}
	return digestBytes([]byte(canonical.String()))
}

func checksumLine(digest, path string) string {
	return strings.TrimPrefix(digest, digestPrefix) + "  " + path + "\n"
}
func checksumPath(line string) string {
	if len(line) < 66 {
		return ""
	}
	return strings.TrimSuffix(line[66:], "\n")
}

func canonicalDigest(value string) bool {
	return strings.HasPrefix(value, digestPrefix) && canonicalDigestHex(strings.TrimPrefix(value, digestPrefix))
}

func canonicalDigestHex(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func digestBytes(body []byte) string {
	return digestPrefix + digestHex(body)
}

func digestHex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func readZipPart(file *zip.File, limit int) ([]byte, error) {
	if file.UncompressedSize64 > uint64(limit) {
		return nil, Wrap(CodeSizeLimit, "validate backup metadata size", nil)
	}
	reader, err := file.Open()
	if err != nil {
		return nil, Wrap(CodeArchiveInvalid, "open backup metadata", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	closeErr := reader.Close()
	if readErr != nil {
		return nil, Wrap(CodeArchiveInvalid, "read backup metadata", readErr)
	}
	if closeErr != nil {
		return nil, Wrap(CodeArchiveInvalid, "close backup metadata", closeErr)
	}
	if len(body) > limit {
		return nil, Wrap(CodeSizeLimit, "validate backup metadata size", nil)
	}
	return body, nil
}

func validateZipEntries(entries []*zip.File) (map[string]*zip.File, error) {
	if len(entries) != archiveEntries {
		return nil, Wrap(CodeArchiveInvalid, "validate backup entry count", nil)
	}
	expected := map[string]bool{manifestPath: true, checksumsPath: true}
	for _, name := range TableOrder {
		path, _ := tablePath(name)
		expected[path] = true
	}
	files := make(map[string]*zip.File, len(entries))
	var total uint64
	for _, file := range entries {
		name := file.Name
		if filepath.IsAbs(name) || strings.Contains(name, "\\") || strings.HasSuffix(name, "/") || strings.Contains(name, "../") || !expected[name] {
			return nil, Wrap(CodeArchiveInvalid, "validate backup entry path", nil)
		}
		if _, duplicate := files[name]; duplicate {
			return nil, Wrap(CodeArchiveInvalid, "validate duplicate backup entry", nil)
		}
		if file.Method != zip.Deflate || file.Flags&1 != 0 || !file.Mode().IsRegular() || file.Mode().Perm() != 0o600 {
			return nil, Wrap(CodeArchiveInvalid, "validate backup entry metadata", nil)
		}
		if file.UncompressedSize64 > MaxArchiveBytes || total > MaxArchiveBytes-file.UncompressedSize64 {
			return nil, Wrap(CodeSizeLimit, "validate archive size", nil)
		}
		total += file.UncompressedSize64
		files[name] = file
	}
	return files, nil
}

func preflightZIPDirectory(reader io.ReaderAt, size int64) error {
	if size < 22 {
		return Wrap(CodeArchiveInvalid, "locate zip directory", nil)
	}
	windowSize := int64(22 + 65535)
	if size < windowSize {
		windowSize = size
	}
	window := make([]byte, windowSize)
	if _, err := reader.ReadAt(window, size-windowSize); err != nil {
		return Wrap(CodeArchiveInvalid, "read zip directory", err)
	}
	index := bytes.LastIndex(window, []byte{'P', 'K', 5, 6})
	if index < 0 || index+22 > len(window) {
		return Wrap(CodeArchiveInvalid, "locate zip directory", nil)
	}
	eocdOffset := size - windowSize + int64(index)
	eocd := window[index:]
	commentLength := int(binary.LittleEndian.Uint16(eocd[20:22]))
	if index+22+commentLength != len(window) {
		return Wrap(CodeArchiveInvalid, "validate zip directory trailer", nil)
	}
	disk := binary.LittleEndian.Uint16(eocd[4:6])
	centralDisk := binary.LittleEndian.Uint16(eocd[6:8])
	diskEntries := uint64(binary.LittleEndian.Uint16(eocd[8:10]))
	entries := uint64(binary.LittleEndian.Uint16(eocd[10:12]))
	centralSize := uint64(binary.LittleEndian.Uint32(eocd[12:16]))
	centralOffset := uint64(binary.LittleEndian.Uint32(eocd[16:20]))
	zip64 := diskEntries == 0xffff || entries == 0xffff || centralSize == 0xffffffff || centralOffset == 0xffffffff
	if zip64 {
		if eocdOffset < 20 {
			return Wrap(CodeArchiveInvalid, "locate zip64 directory", nil)
		}
		locator := make([]byte, 20)
		if _, err := reader.ReadAt(locator, eocdOffset-20); err != nil || !bytes.Equal(locator[:4], []byte{'P', 'K', 6, 7}) {
			return Wrap(CodeArchiveInvalid, "read zip64 directory locator", err)
		}
		if binary.LittleEndian.Uint32(locator[4:8]) != 0 || binary.LittleEndian.Uint32(locator[16:20]) != 1 {
			return Wrap(CodeArchiveInvalid, "validate zip64 disks", nil)
		}
		recordOffset := binary.LittleEndian.Uint64(locator[8:16])
		locatorOffset := uint64(eocdOffset - 20)
		if recordOffset > uint64(size) || uint64(size)-recordOffset < 56 || recordOffset > locatorOffset || locatorOffset-recordOffset < 56 {
			return Wrap(CodeArchiveInvalid, "validate zip64 directory offset", nil)
		}
		record := make([]byte, 56)
		if _, err := reader.ReadAt(record, int64(recordOffset)); err != nil || !bytes.Equal(record[:4], []byte{'P', 'K', 6, 6}) {
			return Wrap(CodeArchiveInvalid, "read zip64 directory", err)
		}
		recordSize := binary.LittleEndian.Uint64(record[4:12])
		if recordSize < 44 || recordSize > locatorOffset-recordOffset-12 || binary.LittleEndian.Uint32(record[16:20]) != 0 || binary.LittleEndian.Uint32(record[20:24]) != 0 {
			return Wrap(CodeArchiveInvalid, "validate zip64 directory", nil)
		}
		diskEntries = binary.LittleEndian.Uint64(record[24:32])
		entries = binary.LittleEndian.Uint64(record[32:40])
		centralSize = binary.LittleEndian.Uint64(record[40:48])
		centralOffset = binary.LittleEndian.Uint64(record[48:56])
	}
	if disk != 0 || centralDisk != 0 || diskEntries != entries {
		return Wrap(CodeArchiveInvalid, "validate zip disks", nil)
	}
	if entries != archiveEntries {
		return Wrap(CodeArchiveInvalid, "validate backup entry count", nil)
	}
	if centralSize > MaxCentralDirectoryBytes {
		return Wrap(CodeSizeLimit, "validate zip directory size", nil)
	}
	if centralOffset > uint64(size) || centralSize > uint64(size)-centralOffset || centralOffset+centralSize > uint64(eocdOffset) {
		return Wrap(CodeArchiveInvalid, "validate zip directory bounds", nil)
	}
	return nil
}

func summaryFromManifest(path string, compressed int64, manifest Manifest) Summary {
	var uncompressed uint64
	for _, table := range manifest.Tables {
		uncompressed += table.UncompressedBytes
	}
	return Summary{
		Path: path, APIVersion: manifest.APIVersion, CreatedAt: manifest.CreatedAt,
		RuntimeVersion: manifest.RuntimeVersion, MigrationVersion: manifest.DatabaseMigrationVersion,
		DatasetDigest: manifest.DatasetDigest, CompressedBytes: compressed,
		UncompressedBytes: uncompressed, Tables: append([]TableManifest(nil), manifest.Tables...),
	}
}
