package agentnode

type Capability string

const (
	CapabilityNetwork         Capability = "network"
	CapabilitySecrets         Capability = "secrets"
	CapabilityFilesystemRead  Capability = "filesystem-read"
	CapabilityFilesystemWrite Capability = "filesystem-write"
)
