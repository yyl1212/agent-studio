package nodepackage

const (
	APIVersion = "agent-studio.dev/v1alpha1"
	Kind       = "NodePackage"
	Filename   = "agent-studio.node-package.json"

	MaxManifestBytes     = 256 << 10
	MaxRegistrations     = 128
	MaxNodes             = 512
	MaxModulePathLength  = 512
	MaxDisplayNameLength = 128
	MaxDescriptionLength = 2048
	MaxLicenseLength     = 128
	MaxRepositoryLength  = 2048
	MaxNodeAPILength     = 128
	MaxNodeTypeLength    = 256
	MaxVersionLength     = 128
)

type Manifest struct {
	APIVersion    string         `json:"apiVersion"`
	Kind          string         `json:"kind"`
	Metadata      Metadata       `json:"metadata"`
	Compatibility Compatibility  `json:"compatibility"`
	Registrations []Registration `json:"registrations"`
}

type Metadata struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	License     string `json:"license"`
	Repository  string `json:"repository"`
}

type Compatibility struct {
	NodeAPI string       `json:"nodeAPI"`
	Runtime RuntimeRange `json:"runtime"`
}

type RuntimeRange struct {
	MinVersion          string `json:"minVersion"`
	MaxVersionExclusive string `json:"maxVersionExclusive"`
}

type Registration struct {
	Package string    `json:"package"`
	Nodes   []NodeRef `json:"nodes"`
}

type NodeRef struct {
	Type    string `json:"type"`
	Version string `json:"version"`
}
