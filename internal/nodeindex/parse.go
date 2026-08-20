package nodeindex

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yyl1212/agent-studio/internal/nodepackage"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

const maxJSONDepth = 64

var (
	stableSemverPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	gitOIDPattern       = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)
	digestPattern       = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	categoryPattern     = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	githubRepoPattern   = regexp.MustCompile(`^https://github[.]com/[A-Za-z0-9]([A-Za-z0-9-]{0,37}[A-Za-z0-9])?/[A-Za-z0-9._-]+$`)
	utcSecondPattern    = regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])-(0[1-9]|[12][0-9]|3[01])T([01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9]Z$`)
)

//go:embed assets/index.json
var embeddedIndex []byte

type indexWire struct {
	APIVersion *string            `json:"apiVersion"`
	Kind       *string            `json:"kind"`
	Metadata   *indexMetadataWire `json:"metadata"`
	Packages   *[]packageWire     `json:"packages"`
}

type indexMetadataWire struct {
	Release      *string `json:"release"`
	GeneratedAt  *string `json:"generatedAt"`
	SourceCommit *string `json:"sourceCommit"`
}

type packageWire struct {
	Name       *string               `json:"name"`
	Categories *[]string             `json:"categories"`
	Keywords   *[]string             `json:"keywords"`
	Versions   *[]packageVersionWire `json:"versions"`
}

type packageVersionWire struct {
	Version   *string          `json:"version"`
	Source    *sourceWire      `json:"source"`
	Review    *reviewWire      `json:"review"`
	Lifecycle *lifecycleWire   `json:"lifecycle"`
	Manifest  *json.RawMessage `json:"manifest"`
}

type sourceWire struct {
	Repository     *string `json:"repository"`
	ModuleDir      *string `json:"moduleDir"`
	Tag            *string `json:"tag"`
	Commit         *string `json:"commit"`
	ManifestDigest *string `json:"manifestDigest"`
}

type reviewWire struct {
	Status      *string `json:"status"`
	ReviewedAt  *string `json:"reviewedAt"`
	IndexCommit *string `json:"indexCommit"`
}

type lifecycleWire struct {
	Status  *string `json:"status"`
	Message *string `json:"message"`
}

func Parse(source string, data []byte) (Index, error) {
	_ = source
	if len(data) > MaxIndexBytes || !utf8.Valid(data) {
		return Index{}, invalidContent(nil)
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return Index{}, invalidContent(err)
	}

	var wire indexWire
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return Index{}, invalidContent(err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Index{}, invalidContent(err)
	}

	index, err := wire.index()
	if err != nil {
		return Index{}, err
	}
	if err := validateIndex(index); err != nil {
		return Index{}, invalidContent(err)
	}
	return index, nil
}

func (wire indexWire) index() (Index, error) {
	if wire.APIVersion == nil || wire.Kind == nil || wire.Metadata == nil || wire.Packages == nil {
		return Index{}, invalidContent(nil)
	}
	if *wire.APIVersion != APIVersion || *wire.Kind != Kind {
		return Index{}, coded(CodeSchemaUnsupported, "node index schema is unsupported", nil)
	}
	metadata, err := wire.Metadata.metadata()
	if err != nil {
		return Index{}, err
	}
	packages := make([]Package, 0, len(*wire.Packages))
	for _, packageWire := range *wire.Packages {
		converted, convertErr := packageWire.pkg()
		if convertErr != nil {
			return Index{}, convertErr
		}
		packages = append(packages, converted)
	}
	return Index{APIVersion: *wire.APIVersion, Kind: *wire.Kind, Metadata: metadata, Packages: packages}, nil
}

func (wire indexMetadataWire) metadata() (IndexMetadata, error) {
	if wire.Release == nil || wire.GeneratedAt == nil || wire.SourceCommit == nil {
		return IndexMetadata{}, invalidContent(nil)
	}
	generatedAt, err := parseUTCTimestamp(*wire.GeneratedAt)
	if err != nil {
		return IndexMetadata{}, invalidContent(err)
	}
	return IndexMetadata{Release: *wire.Release, GeneratedAt: generatedAt, SourceCommit: *wire.SourceCommit}, nil
}

func (wire packageWire) pkg() (Package, error) {
	if wire.Name == nil || wire.Categories == nil || wire.Keywords == nil || wire.Versions == nil {
		return Package{}, invalidContent(nil)
	}
	versions := make([]PackageVersion, 0, len(*wire.Versions))
	for _, versionWire := range *wire.Versions {
		version, err := versionWire.packageVersion()
		if err != nil {
			return Package{}, err
		}
		versions = append(versions, version)
	}
	return Package{
		Name:       *wire.Name,
		Categories: append([]string{}, (*wire.Categories)...),
		Keywords:   append([]string{}, (*wire.Keywords)...),
		Versions:   versions,
	}, nil
}

func (wire packageVersionWire) packageVersion() (PackageVersion, error) {
	if wire.Version == nil || wire.Source == nil || wire.Review == nil || wire.Lifecycle == nil || wire.Manifest == nil {
		return PackageVersion{}, invalidContent(nil)
	}
	source, err := wire.Source.source()
	if err != nil {
		return PackageVersion{}, err
	}
	review, err := wire.Review.review()
	if err != nil {
		return PackageVersion{}, err
	}
	lifecycle, err := wire.Lifecycle.lifecycle()
	if err != nil {
		return PackageVersion{}, err
	}
	manifest, err := parseManifest(*wire.Manifest)
	if err != nil {
		return PackageVersion{}, err
	}
	return PackageVersion{Version: *wire.Version, Source: source, Review: review, Lifecycle: lifecycle, Manifest: manifest}, nil
}

func (wire sourceWire) source() (Source, error) {
	if wire.Repository == nil || wire.ModuleDir == nil || wire.Tag == nil || wire.Commit == nil || wire.ManifestDigest == nil {
		return Source{}, invalidContent(nil)
	}
	return Source{
		Repository: *wire.Repository, ModuleDir: *wire.ModuleDir, Tag: *wire.Tag,
		Commit: *wire.Commit, ManifestDigest: *wire.ManifestDigest,
	}, nil
}

func (wire reviewWire) review() (Review, error) {
	if wire.Status == nil || wire.ReviewedAt == nil || wire.IndexCommit == nil {
		return Review{}, invalidContent(nil)
	}
	reviewedAt, err := parseUTCTimestamp(*wire.ReviewedAt)
	if err != nil {
		return Review{}, invalidContent(err)
	}
	return Review{Status: *wire.Status, ReviewedAt: reviewedAt, IndexCommit: *wire.IndexCommit}, nil
}

func (wire lifecycleWire) lifecycle() (Lifecycle, error) {
	if wire.Status == nil || wire.Message == nil {
		return Lifecycle{}, invalidContent(nil)
	}
	return Lifecycle{Status: *wire.Status, Message: *wire.Message}, nil
}

func parseManifest(raw json.RawMessage) (nodepackage.Manifest, error) {
	manifest, err := nodepackage.Parse("index manifest", raw)
	if err != nil {
		return nodepackage.Manifest{}, invalidContent(err)
	}
	var original nodepackage.Manifest
	if err := json.Unmarshal(raw, &original); err != nil || !manifestOrderIsCanonical(original) {
		return nodepackage.Manifest{}, invalidContent(err)
	}
	return manifest, nil
}

func validateIndex(index Index) error {
	if err := validateText(index.Metadata.Release, 1, nodepackage.MaxVersionLength); err != nil ||
		!stableSemverPattern.MatchString(index.Metadata.Release) || !semver.IsValid(index.Metadata.Release) {
		return errors.New("metadata.release is invalid")
	}
	if !gitOIDPattern.MatchString(index.Metadata.SourceCommit) {
		return errors.New("metadata.sourceCommit is invalid")
	}
	if len(index.Packages) > MaxPackages {
		return errors.New("packages exceeds budget")
	}
	for packageIndex := range index.Packages {
		pkg := index.Packages[packageIndex]
		if packageIndex > 0 && index.Packages[packageIndex-1].Name >= pkg.Name {
			return errors.New("packages order is invalid")
		}
		if err := validatePackage(pkg); err != nil {
			return err
		}
	}
	return nil
}

func validatePackage(pkg Package) error {
	if err := validateText(pkg.Name, 1, nodepackage.MaxModulePathLength); err != nil || module.CheckPath(pkg.Name) != nil {
		return errors.New("packages.name is invalid")
	}
	if err := validateCategories(pkg.Categories); err != nil {
		return err
	}
	if err := validateKeywords(pkg.Keywords); err != nil {
		return err
	}
	if len(pkg.Versions) == 0 || len(pkg.Versions) > MaxVersionsPerPackage {
		return errors.New("packages.versions exceeds budget")
	}
	for versionIndex := range pkg.Versions {
		version := pkg.Versions[versionIndex]
		if !semver.IsValid(version.Version) || utf8.RuneCountInString(version.Version) > nodepackage.MaxVersionLength {
			return errors.New("packages.versions.version is invalid")
		}
		if versionIndex > 0 && semver.Compare(pkg.Versions[versionIndex-1].Version, version.Version) >= 0 {
			return errors.New("packages.versions order is invalid")
		}
		if err := validatePackageVersion(pkg.Name, version); err != nil {
			return err
		}
	}
	return nil
}

func validatePackageVersion(packageName string, version PackageVersion) error {
	if err := validateSource(version.Source); err != nil {
		return err
	}
	if version.Review.Status != "approved" || !gitOIDPattern.MatchString(version.Review.IndexCommit) {
		return errors.New("packages.versions.review is invalid")
	}
	if err := validateLifecycle(version.Lifecycle); err != nil {
		return err
	}
	if version.Manifest.Metadata.Name != packageName || version.Manifest.Metadata.Repository != version.Source.Repository {
		return errors.New("packages.versions.manifest identity is invalid")
	}
	return nil
}

func validateSource(source Source) error {
	if !githubRepoPattern.MatchString(source.Repository) {
		return errors.New("packages.versions.source.repository is invalid")
	}
	parsed, err := url.Parse(source.Repository)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "github.com" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("packages.versions.source.repository is invalid")
	}
	if err := validateModuleDir(source.ModuleDir); err != nil {
		return err
	}
	if err := validateText(source.Tag, 1, MaxTagLength); err != nil || containsControl(source.Tag) {
		return errors.New("packages.versions.source.tag is invalid")
	}
	if !gitOIDPattern.MatchString(source.Commit) || !digestPattern.MatchString(source.ManifestDigest) {
		return errors.New("packages.versions.source digest is invalid")
	}
	return nil
}

func validateModuleDir(moduleDir string) error {
	if err := validateText(moduleDir, 1, MaxModuleDirLength); err != nil || strings.Contains(moduleDir, `\`) || path.IsAbs(moduleDir) || path.Clean(moduleDir) != moduleDir {
		return errors.New("packages.versions.source.moduleDir is invalid")
	}
	if moduleDir == "." {
		return nil
	}
	for _, segment := range strings.Split(moduleDir, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return errors.New("packages.versions.source.moduleDir is invalid")
		}
	}
	return nil
}

func validateCategories(values []string) error {
	if len(values) > MaxCategories {
		return errors.New("packages.categories exceeds budget")
	}
	for index, value := range values {
		if !categoryPattern.MatchString(value) || utf8.RuneCountInString(value) > MaxCategoryLength ||
			(index > 0 && values[index-1] >= value) {
			return errors.New("packages.categories is invalid")
		}
	}
	return nil
}

func validateKeywords(values []string) error {
	if len(values) > MaxKeywords {
		return errors.New("packages.keywords exceeds budget")
	}
	for index, value := range values {
		if err := validateText(value, 1, MaxKeywordLength); err != nil || strings.TrimSpace(value) != value || strings.ToLower(value) != value ||
			containsControl(value) || (index > 0 && values[index-1] >= value) {
			return errors.New("packages.keywords is invalid")
		}
	}
	return nil
}

func validateLifecycle(lifecycle Lifecycle) error {
	if err := validateText(lifecycle.Message, 0, MaxLifecycleMessage); err != nil {
		return errors.New("packages.versions.lifecycle.message is invalid")
	}
	switch lifecycle.Status {
	case "active":
		if lifecycle.Message != "" {
			return errors.New("packages.versions.lifecycle is invalid")
		}
	case "deprecated", "withdrawn":
		if lifecycle.Message == "" || strings.ContainsAny(lifecycle.Message, "<>") {
			return errors.New("packages.versions.lifecycle is invalid")
		}
	default:
		return errors.New("packages.versions.lifecycle is invalid")
	}
	return nil
}

func manifestOrderIsCanonical(manifest nodepackage.Manifest) bool {
	for registrationIndex := range manifest.Registrations {
		registration := manifest.Registrations[registrationIndex]
		if registrationIndex > 0 && manifest.Registrations[registrationIndex-1].Package >= registration.Package {
			return false
		}
		for nodeIndex := range registration.Nodes {
			if nodeIndex == 0 {
				continue
			}
			previous := registration.Nodes[nodeIndex-1]
			current := registration.Nodes[nodeIndex]
			if previous.Type > current.Type || (previous.Type == current.Type && previous.Version >= current.Version) {
				return false
			}
		}
	}
	return true
}

func parseUTCTimestamp(value string) (time.Time, error) {
	if !utcSecondPattern.MatchString(value) {
		return time.Time{}, errors.New("timestamp is invalid")
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil || parsed.Format(time.RFC3339) != value {
		return time.Time{}, errors.New("timestamp is invalid")
	}
	return parsed, nil
}

func validateText(value string, minimum, maximum int) error {
	if !utf8.ValidString(value) {
		return errors.New("text is invalid")
	}
	length := utf8.RuneCountInString(value)
	if length < minimum || length > maximum {
		return errors.New("text length is invalid")
	}
	return nil
}

func containsControl(value string) bool {
	return strings.ContainsFunc(value, unicode.IsControl)
}

func invalidContent(cause error) error {
	return coded(CodeContentInvalid, "node index content is invalid", cause)
}

func rejectDuplicateKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanJSONValue(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder, depth int) error {
	if depth > maxJSONDepth {
		return errors.New("JSON nesting exceeds budget")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, keyErr := decoder.Token()
			if keyErr != nil {
				return keyErr
			}
			key, isString := keyToken.(string)
			if !isString {
				return errors.New("object key is invalid")
			}
			if _, duplicate := seen[key]; duplicate {
				return errors.New("duplicate object key")
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, closeErr := decoder.Token()
		if closeErr != nil || closing != json.Delim('}') {
			return errors.New("object is not closed")
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, closeErr := decoder.Token()
		if closeErr != nil || closing != json.Delim(']') {
			return errors.New("array is not closed")
		}
	default:
		return fmt.Errorf("unexpected delimiter")
	}
	return nil
}
