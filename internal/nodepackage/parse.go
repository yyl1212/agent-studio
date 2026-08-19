package nodepackage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"unicode/utf8"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

type manifestWire struct {
	APIVersion    *string             `json:"apiVersion"`
	Kind          *string             `json:"kind"`
	Metadata      *metadataWire       `json:"metadata"`
	Compatibility *compatibilityWire  `json:"compatibility"`
	Registrations *[]registrationWire `json:"registrations"`
}

type metadataWire struct {
	Name        *string `json:"name"`
	DisplayName *string `json:"displayName"`
	Description *string `json:"description"`
	License     *string `json:"license"`
	Repository  *string `json:"repository"`
}

type compatibilityWire struct {
	NodeAPI *string           `json:"nodeAPI"`
	Runtime *runtimeRangeWire `json:"runtime"`
}

type runtimeRangeWire struct {
	MinVersion          *string `json:"minVersion"`
	MaxVersionExclusive *string `json:"maxVersionExclusive"`
}

type registrationWire struct {
	Package *string        `json:"package"`
	Nodes   *[]nodeRefWire `json:"nodes"`
}

type nodeRefWire struct {
	Type    *string `json:"type"`
	Version *string `json:"version"`
}

func Parse(source string, data []byte) (Manifest, error) {
	if len(data) > MaxManifestBytes {
		return Manifest{}, fmt.Errorf("parse node package manifest %s: file exceeds %d bytes", source, MaxManifestBytes)
	}
	if !utf8.Valid(data) {
		return Manifest{}, fmt.Errorf("parse node package manifest %s: invalid UTF-8", source)
	}
	var wire manifestWire
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return Manifest{}, fmt.Errorf("parse node package manifest %s: %w", source, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return Manifest{}, fmt.Errorf("parse node package manifest %s: trailing JSON: %w", source, err)
	}
	manifest, err := wire.manifest()
	if err != nil {
		return Manifest{}, fmt.Errorf("validate node package manifest %s: %w", source, err)
	}
	normalized := cloneAndNormalize(manifest)
	if err := validate(normalized); err != nil {
		return Manifest{}, fmt.Errorf("validate node package manifest %s: %w", source, err)
	}
	return normalized, nil
}

func Encode(manifest Manifest) ([]byte, error) {
	normalized := cloneAndNormalize(manifest)
	if err := validate(normalized); err != nil {
		return nil, fmt.Errorf("validate node package manifest: %w", err)
	}
	encoded, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode node package manifest: %w", err)
	}
	return append(encoded, '\n'), nil
}

func AddRegistration(manifest Manifest, registration Registration) (Manifest, error) {
	updated := cloneAndNormalize(manifest)
	updated.Registrations = append(updated.Registrations, Registration{
		Package: registration.Package,
		Nodes:   append([]NodeRef(nil), registration.Nodes...),
	})
	updated = cloneAndNormalize(updated)
	if err := validate(updated); err != nil {
		return Manifest{}, fmt.Errorf("add node package registration: %w", err)
	}
	return updated, nil
}

func cloneAndNormalize(input Manifest) Manifest {
	output := input
	output.Registrations = append([]Registration(nil), input.Registrations...)
	if output.Registrations == nil {
		output.Registrations = []Registration{}
	}
	for index := range output.Registrations {
		output.Registrations[index].Nodes = append([]NodeRef(nil), input.Registrations[index].Nodes...)
		sort.Slice(output.Registrations[index].Nodes, func(left, right int) bool {
			if output.Registrations[index].Nodes[left].Type != output.Registrations[index].Nodes[right].Type {
				return output.Registrations[index].Nodes[left].Type < output.Registrations[index].Nodes[right].Type
			}
			return output.Registrations[index].Nodes[left].Version < output.Registrations[index].Nodes[right].Version
		})
	}
	sort.Slice(output.Registrations, func(left, right int) bool {
		return output.Registrations[left].Package < output.Registrations[right].Package
	})
	return output
}

func validate(manifest Manifest) error {
	if manifest.APIVersion != APIVersion {
		return fmt.Errorf("apiVersion must be %q", APIVersion)
	}
	if manifest.Kind != Kind {
		return fmt.Errorf("kind must be %q", Kind)
	}
	if err := validateMetadata(manifest.Metadata); err != nil {
		return err
	}
	if err := validateCompatibility(manifest.Compatibility); err != nil {
		return err
	}
	if len(manifest.Registrations) > MaxRegistrations {
		return fmt.Errorf("registrations exceeds %d items", MaxRegistrations)
	}
	packages := make(map[string]struct{}, len(manifest.Registrations))
	nodes := make(map[string]struct{})
	totalNodes := 0
	for index, registration := range manifest.Registrations {
		path := fmt.Sprintf("registrations[%d]", index)
		if err := validateString(path+".package", registration.Package, 1, MaxModulePathLength); err != nil {
			return err
		}
		if err := module.CheckImportPath(registration.Package); err != nil {
			return fmt.Errorf("%s.package is not a valid Go import path: %w", path, err)
		}
		if registration.Package != manifest.Metadata.Name && !hasPathPrefix(registration.Package, manifest.Metadata.Name) {
			return fmt.Errorf("%s.package must belong to module %q", path, manifest.Metadata.Name)
		}
		if _, exists := packages[registration.Package]; exists {
			return fmt.Errorf("duplicate registration package %q", registration.Package)
		}
		packages[registration.Package] = struct{}{}
		if len(registration.Nodes) == 0 {
			return fmt.Errorf("%s.nodes must contain at least one node", path)
		}
		totalNodes += len(registration.Nodes)
		if totalNodes > MaxNodes {
			return fmt.Errorf("node declarations exceeds %d items", MaxNodes)
		}
		for nodeIndex, node := range registration.Nodes {
			nodePath := fmt.Sprintf("%s.nodes[%d]", path, nodeIndex)
			if err := validateString(nodePath+".type", node.Type, 1, MaxNodeTypeLength); err != nil {
				return err
			}
			if err := validateString(nodePath+".version", node.Version, 1, MaxVersionLength); err != nil {
				return err
			}
			key := node.Type + "@" + node.Version
			if _, exists := nodes[key]; exists {
				return fmt.Errorf("duplicate node %q", key)
			}
			nodes[key] = struct{}{}
		}
	}
	return nil
}

func validateMetadata(metadata Metadata) error {
	if err := validateString("metadata.name", metadata.Name, 1, MaxModulePathLength); err != nil {
		return err
	}
	if err := module.CheckPath(metadata.Name); err != nil {
		return fmt.Errorf("metadata.name is not a valid Go module path: %w", err)
	}
	for _, field := range []struct {
		path  string
		value string
		min   int
		max   int
	}{
		{path: "metadata.displayName", value: metadata.DisplayName, min: 1, max: MaxDisplayNameLength},
		{path: "metadata.description", value: metadata.Description, max: MaxDescriptionLength},
		{path: "metadata.license", value: metadata.License, min: 1, max: MaxLicenseLength},
		{path: "metadata.repository", value: metadata.Repository, min: 1, max: MaxRepositoryLength},
	} {
		if err := validateString(field.path, field.value, field.min, field.max); err != nil {
			return err
		}
	}
	parsed, err := url.Parse(metadata.Repository)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("metadata.repository must be an HTTPS URL without credentials, query, or fragment")
	}
	return nil
}

func validateCompatibility(compatibility Compatibility) error {
	if err := validateString("compatibility.nodeAPI", compatibility.NodeAPI, 1, MaxNodeAPILength); err != nil {
		return err
	}
	if err := validateString("compatibility.runtime.minVersion", compatibility.Runtime.MinVersion, 1, MaxVersionLength); err != nil {
		return err
	}
	if err := validateString("compatibility.runtime.maxVersionExclusive", compatibility.Runtime.MaxVersionExclusive, 1, MaxVersionLength); err != nil {
		return err
	}
	if !semver.IsValid(compatibility.Runtime.MinVersion) {
		return errors.New("compatibility.runtime.minVersion must be valid Go SemVer")
	}
	if !semver.IsValid(compatibility.Runtime.MaxVersionExclusive) {
		return errors.New("compatibility.runtime.maxVersionExclusive must be valid Go SemVer")
	}
	if semver.Compare(compatibility.Runtime.MaxVersionExclusive, compatibility.Runtime.MinVersion) <= 0 {
		return errors.New("compatibility.runtime.maxVersionExclusive must be greater than minVersion")
	}
	return nil
}

func validateString(path, value string, minimum, maximum int) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s must be valid UTF-8", path)
	}
	length := utf8.RuneCountInString(value)
	if length < minimum || length > maximum {
		return fmt.Errorf("%s length must be between %d and %d", path, minimum, maximum)
	}
	return nil
}

func hasPathPrefix(value, prefix string) bool {
	return len(value) > len(prefix) && value[:len(prefix)] == prefix && value[len(prefix)] == '/'
}

func (wire manifestWire) manifest() (Manifest, error) {
	if wire.APIVersion == nil || wire.Kind == nil || wire.Metadata == nil || wire.Compatibility == nil || wire.Registrations == nil {
		return Manifest{}, errors.New("apiVersion, kind, metadata, compatibility, and registrations are required")
	}
	metadata, err := wire.Metadata.metadata()
	if err != nil {
		return Manifest{}, err
	}
	compatibility, err := wire.Compatibility.compatibility()
	if err != nil {
		return Manifest{}, err
	}
	registrations := make([]Registration, 0, len(*wire.Registrations))
	for index, registration := range *wire.Registrations {
		converted, convertErr := registration.registration(index)
		if convertErr != nil {
			return Manifest{}, convertErr
		}
		registrations = append(registrations, converted)
	}
	return Manifest{
		APIVersion: *wire.APIVersion, Kind: *wire.Kind, Metadata: metadata,
		Compatibility: compatibility, Registrations: registrations,
	}, nil
}

func (wire metadataWire) metadata() (Metadata, error) {
	if wire.Name == nil || wire.DisplayName == nil || wire.Description == nil || wire.License == nil || wire.Repository == nil {
		return Metadata{}, errors.New("metadata.name, displayName, description, license, and repository are required")
	}
	return Metadata{
		Name: *wire.Name, DisplayName: *wire.DisplayName, Description: *wire.Description,
		License: *wire.License, Repository: *wire.Repository,
	}, nil
}

func (wire compatibilityWire) compatibility() (Compatibility, error) {
	if wire.NodeAPI == nil || wire.Runtime == nil {
		return Compatibility{}, errors.New("compatibility.nodeAPI and runtime are required")
	}
	runtimeRange, err := wire.Runtime.runtimeRange()
	if err != nil {
		return Compatibility{}, err
	}
	return Compatibility{NodeAPI: *wire.NodeAPI, Runtime: runtimeRange}, nil
}

func (wire runtimeRangeWire) runtimeRange() (RuntimeRange, error) {
	if wire.MinVersion == nil || wire.MaxVersionExclusive == nil {
		return RuntimeRange{}, errors.New("compatibility.runtime.minVersion and maxVersionExclusive are required")
	}
	return RuntimeRange{MinVersion: *wire.MinVersion, MaxVersionExclusive: *wire.MaxVersionExclusive}, nil
}

func (wire registrationWire) registration(index int) (Registration, error) {
	if wire.Package == nil || wire.Nodes == nil {
		return Registration{}, fmt.Errorf("registrations[%d].package and nodes are required", index)
	}
	nodes := make([]NodeRef, 0, len(*wire.Nodes))
	for nodeIndex, node := range *wire.Nodes {
		if node.Type == nil || node.Version == nil {
			return Registration{}, fmt.Errorf("registrations[%d].nodes[%d].type and version are required", index, nodeIndex)
		}
		nodes = append(nodes, NodeRef{Type: *node.Type, Version: *node.Version})
	}
	return Registration{Package: *wire.Package, Nodes: nodes}, nil
}
