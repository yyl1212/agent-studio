package agentnode

func SupportsAPIVersion(version string) bool {
	return version == APIVersion
}
