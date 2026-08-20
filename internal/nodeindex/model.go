package nodeindex

import (
	"errors"
	"time"

	"github.com/yyl1212/agent-studio/internal/nodepackage"
)

const (
	APIVersion = "agent-studio.dev/v1alpha1"
	Kind       = "NodePackageIndex"

	MaxIndexBytes         = 4 << 20
	MaxPackages           = 1000
	MaxVersionsPerPackage = 20
	MaxCategories         = 8
	MaxKeywords           = 16
	MaxCategoryLength     = 32
	MaxKeywordLength      = 64
	MaxTagLength          = 512
	MaxModuleDirLength    = 1024
	MaxLifecycleMessage   = 2048
)

type Index struct {
	APIVersion string        `json:"apiVersion"`
	Kind       string        `json:"kind"`
	Metadata   IndexMetadata `json:"metadata"`
	Packages   []Package     `json:"packages"`
}

type IndexMetadata struct {
	Release      string    `json:"release"`
	GeneratedAt  time.Time `json:"generatedAt"`
	SourceCommit string    `json:"sourceCommit"`
}

type Package struct {
	Name       string           `json:"name"`
	Categories []string         `json:"categories"`
	Keywords   []string         `json:"keywords"`
	Versions   []PackageVersion `json:"versions"`
}

type Source struct {
	Repository     string `json:"repository"`
	ModuleDir      string `json:"moduleDir"`
	Tag            string `json:"tag"`
	Commit         string `json:"commit"`
	ManifestDigest string `json:"manifestDigest"`
}

type Review struct {
	Status      string    `json:"status"`
	ReviewedAt  time.Time `json:"reviewedAt"`
	IndexCommit string    `json:"indexCommit"`
}

type Lifecycle struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

type PackageVersion struct {
	Version   string               `json:"version"`
	Source    Source               `json:"source"`
	Review    Review               `json:"review"`
	Lifecycle Lifecycle            `json:"lifecycle"`
	Manifest  nodepackage.Manifest `json:"manifest"`
}

type Runtime struct {
	Version string
	NodeAPI string
}

type Query struct {
	Text           string
	Categories     []string
	CompatibleOnly bool
	Limit          int
	Offset         int
}

type Code string

const (
	CodeRateLimited        Code = "INDEX_RATE_LIMITED"
	CodeReleaseNotFound    Code = "INDEX_RELEASE_NOT_FOUND"
	CodeReleaseInvalid     Code = "INDEX_RELEASE_INVALID"
	CodeAssetInvalid       Code = "INDEX_ASSET_INVALID"
	CodeDigestMismatch     Code = "INDEX_DIGEST_MISMATCH"
	CodeSchemaUnsupported  Code = "INDEX_SCHEMA_UNSUPPORTED"
	CodeContentInvalid     Code = "INDEX_CONTENT_INVALID"
	CodeRefreshInProgress  Code = "INDEX_REFRESH_IN_PROGRESS"
	CodeReleaseDowngrade   Code = "INDEX_RELEASE_DOWNGRADE"
	CodeCacheWriteFailed   Code = "INDEX_CACHE_WRITE_FAILED"
	CodeRefreshUnsupported Code = "INDEX_REFRESH_UNSUPPORTED"
	CodeNotFound           Code = "NODE_PACKAGE_NOT_FOUND"
	CodeEmbeddedSnapshot   Code = "INDEX_EMBEDDED_SNAPSHOT"
)

type codedError struct {
	code    Code
	message string
	cause   error
}

func (err *codedError) Error() string {
	return err.message
}

func (err *codedError) Unwrap() error {
	return err.cause
}

func coded(code Code, message string, cause error) error {
	return &codedError{code: code, message: message, cause: cause}
}

func CodeOf(err error) Code {
	var target *codedError
	if errors.As(err, &target) {
		return target.code
	}
	return ""
}
