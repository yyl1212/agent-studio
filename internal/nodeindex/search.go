package nodeindex

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/yyl1212/agent-studio/internal/nodepackage"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

const (
	MaxQueryLength  = 128
	MaxSearchLimit  = 100
	MaxSearchOffset = 10000
)

type VersionSummary struct {
	Version       string                    `json:"version"`
	Source        Source                    `json:"source"`
	Lifecycle     Lifecycle                 `json:"lifecycle"`
	Compatibility nodepackage.Compatibility `json:"compatibility"`
}

type PackageSummary struct {
	Name               string          `json:"name"`
	DisplayName        string          `json:"displayName"`
	Description        string          `json:"description"`
	License            string          `json:"license"`
	Repository         string          `json:"repository"`
	Categories         []string        `json:"categories"`
	Keywords           []string        `json:"keywords"`
	RecommendedVersion *VersionSummary `json:"recommendedVersion"`
	Reasons            []Reason        `json:"reasons"`
}

type SearchResult struct {
	Release string           `json:"release"`
	Total   int              `json:"total"`
	Offset  int              `json:"offset"`
	Limit   int              `json:"limit"`
	Items   []PackageSummary `json:"items"`
}

type VersionAssessment struct {
	Version    string   `json:"version"`
	Compatible bool     `json:"compatible"`
	Reasons    []Reason `json:"reasons"`
}

type PackageDetail struct {
	Name               string              `json:"name"`
	Categories         []string            `json:"categories"`
	Keywords           []string            `json:"keywords"`
	Versions           []PackageVersion    `json:"versions"`
	RecommendedVersion *VersionSummary     `json:"recommendedVersion"`
	Reasons            []Reason            `json:"reasons"`
	Assessments        []VersionAssessment `json:"assessments"`
}

type scoredPackage struct {
	item PackageSummary
	rank int
}

func Search(index Index, runtime Runtime, query Query) (SearchResult, error) {
	tokens, categories, err := validateQuery(query)
	if err != nil {
		return SearchResult{}, err
	}

	scored := make([]scoredPackage, 0, len(index.Packages))
	for _, pkg := range index.Packages {
		if !matchesAnyCategory(pkg.Categories, categories) {
			continue
		}
		recommendation := Recommend(pkg, runtime)
		if query.CompatibleOnly && recommendation.Version == nil {
			continue
		}
		representative := recommendation.Version
		if representative == nil {
			representative = highestVisibleVersion(pkg.Versions)
		}
		if representative == nil {
			continue
		}
		rank, matches := scorePackage(pkg, *representative, tokens)
		if !matches {
			continue
		}
		scored = append(scored, scoredPackage{
			item: packageSummary(pkg, *representative, recommendation),
			rank: rank,
		})
	}

	slices.SortFunc(scored, func(left, right scoredPackage) int {
		if left.rank != right.rank {
			return right.rank - left.rank
		}
		return strings.Compare(left.item.Name, right.item.Name)
	})
	total := len(scored)
	start := min(query.Offset, total)
	end := min(start+query.Limit, total)
	items := make([]PackageSummary, 0, end-start)
	for _, result := range scored[start:end] {
		items = append(items, result.item)
	}
	return SearchResult{Release: index.Metadata.Release, Total: total, Offset: query.Offset, Limit: query.Limit, Items: items}, nil
}

func Detail(index Index, runtime Runtime, name string) (PackageDetail, error) {
	if strings.TrimSpace(name) != name || name == "" || utf8.RuneCountInString(name) > nodepackage.MaxModulePathLength || module.CheckPath(name) != nil {
		return PackageDetail{}, coded(CodeContentInvalid, "node package query is invalid", nil)
	}
	for _, pkg := range index.Packages {
		if pkg.Name != name {
			continue
		}
		recommendation := Recommend(pkg, runtime)
		versions := make([]PackageVersion, 0, len(pkg.Versions))
		assessments := make([]VersionAssessment, 0, len(pkg.Versions))
		for _, version := range pkg.Versions {
			if version.Review.Status != "approved" {
				continue
			}
			versions = append(versions, clonePackageVersion(version))
			assessments = append(assessments, assessVersion(version, runtime))
		}
		return PackageDetail{
			Name:               pkg.Name,
			Categories:         append([]string{}, pkg.Categories...),
			Keywords:           append([]string{}, pkg.Keywords...),
			Versions:           versions,
			RecommendedVersion: recommendationSummary(recommendation.Version),
			Reasons:            append([]Reason{}, recommendation.Reasons...),
			Assessments:        assessments,
		}, nil
	}
	return PackageDetail{}, coded(CodeNotFound, "node package was not found", nil)
}

func validateQuery(query Query) ([]string, []string, error) {
	if !utf8.ValidString(query.Text) || utf8.RuneCountInString(query.Text) > MaxQueryLength ||
		query.Limit < 1 || query.Limit > MaxSearchLimit || query.Offset < 0 || query.Offset > MaxSearchOffset {
		return nil, nil, coded(CodeContentInvalid, "node index query is invalid", nil)
	}
	normalizedText := strings.ToLower(strings.TrimSpace(query.Text))
	tokens := append([]string{}, strings.Fields(normalizedText)...)
	categories := make([]string, 0, len(query.Categories))
	seen := make(map[string]struct{}, len(query.Categories))
	for _, category := range query.Categories {
		normalized := strings.ToLower(strings.TrimSpace(category))
		if !categoryPattern.MatchString(normalized) || utf8.RuneCountInString(normalized) > MaxCategoryLength {
			return nil, nil, coded(CodeContentInvalid, "node index query is invalid", nil)
		}
		if _, exists := seen[normalized]; !exists {
			seen[normalized] = struct{}{}
			categories = append(categories, normalized)
		}
	}
	return tokens, categories, nil
}

func matchesAnyCategory(packageCategories, queryCategories []string) bool {
	if len(queryCategories) == 0 {
		return true
	}
	for _, queryCategory := range queryCategories {
		if slices.Contains(packageCategories, queryCategory) {
			return true
		}
	}
	return false
}

func scorePackage(pkg Package, version PackageVersion, tokens []string) (int, bool) {
	moduleName := strings.ToLower(pkg.Name)
	displayName := strings.ToLower(version.Manifest.Metadata.DisplayName)
	description := strings.ToLower(version.Manifest.Metadata.Description)
	categories := lowercaseValues(pkg.Categories)
	keywords := lowercaseValues(pkg.Keywords)
	nodeTypes := make([]string, 0)
	for _, registration := range version.Manifest.Registrations {
		for _, node := range registration.Nodes {
			nodeTypes = append(nodeTypes, strings.ToLower(node.Type))
		}
	}

	total := 0
	for _, token := range tokens {
		rank := 0
		if moduleName == token {
			rank = 400
		} else if strings.HasPrefix(moduleName, token) || strings.HasPrefix(displayName, token) {
			rank = 300
		}
		if rank < 200 && slices.Contains(keywords, token) {
			rank = 200
		}
		if rank < 100 && containsAny(token, moduleName, displayName, description, categories, keywords, nodeTypes) {
			rank = 100
		}
		if rank == 0 {
			return 0, false
		}
		total += rank
	}
	return total, true
}

func containsAny(token, moduleName, displayName, description string, groups ...[]string) bool {
	if strings.Contains(moduleName, token) || strings.Contains(displayName, token) || strings.Contains(description, token) {
		return true
	}
	for _, group := range groups {
		for _, value := range group {
			if strings.Contains(value, token) {
				return true
			}
		}
	}
	return false
}

func lowercaseValues(values []string) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = strings.ToLower(value)
	}
	return result
}

func highestVisibleVersion(versions []PackageVersion) *PackageVersion {
	visible := make([]PackageVersion, 0, len(versions))
	for _, version := range versions {
		if version.Review.Status == "approved" && version.Lifecycle.Status != "withdrawn" && semver.IsValid(version.Version) {
			visible = append(visible, version)
		}
	}
	if len(visible) == 0 {
		return nil
	}
	slices.SortFunc(visible, func(left, right PackageVersion) int { return semver.Compare(left.Version, right.Version) })
	selected := clonePackageVersion(visible[len(visible)-1])
	return &selected
}

func packageSummary(pkg Package, representative PackageVersion, recommendation Recommendation) PackageSummary {
	metadata := representative.Manifest.Metadata
	return PackageSummary{
		Name:               pkg.Name,
		DisplayName:        metadata.DisplayName,
		Description:        metadata.Description,
		License:            metadata.License,
		Repository:         metadata.Repository,
		Categories:         append([]string{}, pkg.Categories...),
		Keywords:           append([]string{}, pkg.Keywords...),
		RecommendedVersion: recommendationSummary(recommendation.Version),
		Reasons:            append([]Reason{}, recommendation.Reasons...),
	}
}

func recommendationSummary(version *PackageVersion) *VersionSummary {
	if version == nil {
		return nil
	}
	return &VersionSummary{
		Version:       version.Version,
		Source:        version.Source,
		Lifecycle:     version.Lifecycle,
		Compatibility: version.Manifest.Compatibility,
	}
}

func assessVersion(version PackageVersion, runtime Runtime) VersionAssessment {
	assessment := VersionAssessment{Version: version.Version, Reasons: []Reason{}}
	current, err := normalizeRuntime(runtime.Version)
	if err != nil {
		assessment.Reasons = append(assessment.Reasons, ReasonRuntimeInvalid)
		return assessment
	}
	if version.Review.Status != "approved" || version.Lifecycle.Status != "active" ||
		!semver.IsValid(version.Version) || semver.Prerelease(version.Version) != "" {
		assessment.Reasons = append(assessment.Reasons, ReasonNoActiveStableVersion)
		return assessment
	}
	if version.Manifest.Compatibility.NodeAPI != runtime.NodeAPI {
		assessment.Reasons = append(assessment.Reasons, ReasonNodeAPIMismatch)
		return assessment
	}
	minimum := version.Manifest.Compatibility.Runtime.MinVersion
	maximum := version.Manifest.Compatibility.Runtime.MaxVersionExclusive
	if !semver.IsValid(minimum) || !semver.IsValid(maximum) || semver.Compare(maximum, minimum) <= 0 {
		assessment.Reasons = append(assessment.Reasons, ReasonNoActiveStableVersion)
		return assessment
	}
	if semver.Compare(current, minimum) < 0 {
		assessment.Reasons = append(assessment.Reasons, ReasonRuntimeTooOld)
		return assessment
	}
	if semver.Compare(current, maximum) >= 0 {
		assessment.Reasons = append(assessment.Reasons, ReasonRuntimeTooNew)
		return assessment
	}
	assessment.Compatible = true
	return assessment
}
