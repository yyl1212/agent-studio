package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/yyl1212/agent-studio/internal/buildinfo"
	"github.com/yyl1212/agent-studio/internal/nodeindex"
)

const (
	nodeSearchUsage      = "node search usage: node search [--category NAME] [--all] [--json] [QUERY]\nnode info usage: node info [--version VERSION] [--json] <module-path>\n"
	nodeReviewDisclaimer = "审核说明：收录表示元数据已经审核，不代表代码安全；安装和执行前请人工审查来源。\n"
)

type nodeSearchCatalog interface {
	Search(nodeindex.Query) (nodeindex.SearchResult, error)
	Get(string) (nodeindex.PackageDetail, error)
}

type nodeSearchDependencies struct {
	configuredCacheDir func() string
	resolveCacheDir    func(string) (string, error)
	runtime            func() nodeindex.Runtime
	openCatalog        func(string, nodeindex.Runtime) (nodeSearchCatalog, error)
}

type repeatedCategories []string

func (categories *repeatedCategories) String() string {
	return strings.Join(*categories, ",")
}

func (categories *repeatedCategories) Set(value string) error {
	*categories = append(*categories, value)
	return nil
}

func nodeSearchCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return runNodeSearch(ctx, args, stdout, stderr, nodeSearchDependencies{
		configuredCacheDir: func() string { return os.Getenv("AGENT_STUDIO_NODE_INDEX_CACHE_DIR") },
		resolveCacheDir:    nodeindex.ResolveCacheDir,
		runtime: func() nodeindex.Runtime {
			info := buildinfo.Current()
			return nodeindex.Runtime{Version: info.Version, NodeAPI: info.APIVersion}
		},
		openCatalog: func(cacheDir string, runtime nodeindex.Runtime) (nodeSearchCatalog, error) {
			store, err := nodeindex.OpenStore(cacheDir)
			if err != nil {
				return nil, err
			}
			return nodeindex.NewCatalog(store, runtime), nil
		},
	})
}

func runNodeSearch(ctx context.Context, args []string, stdout, stderr io.Writer, dependencies nodeSearchDependencies) int {
	if len(args) == 0 || (args[0] != "search" && args[0] != "info") {
		_, _ = io.WriteString(stderr, nodeSearchUsage)
		return 2
	}
	command := args[0]
	jsonOutput, query, moduleName, version, ok := parseNodeSearchArguments(command, args[1:])
	if !ok {
		_, _ = io.WriteString(stderr, nodeSearchUsage)
		return 2
	}
	cacheDir, err := dependencies.resolveCacheDir(dependencies.configuredCacheDir())
	if err != nil {
		return writeNodeIndexError(stderr, jsonOutput, err)
	}
	runtime := dependencies.runtime()
	catalog, err := dependencies.openCatalog(cacheDir, runtime)
	if err != nil {
		return writeNodeIndexError(stderr, jsonOutput, err)
	}

	switch command {
	case "search":
		result, err := catalog.Search(query)
		if err != nil {
			return writeNodeIndexError(stderr, jsonOutput, err)
		}
		return writeNodeSearchResult(stdout, stderr, jsonOutput, result)
	case "info":
		detail, err := catalog.Get(moduleName)
		if err != nil {
			return writeNodeIndexError(stderr, jsonOutput, err)
		}
		if version != "" {
			detail, err = selectNodePackageVersion(detail, version)
			if err != nil {
				return writeNodeIndexError(stderr, jsonOutput, err)
			}
		}
		return writeNodeInfoResult(stdout, stderr, jsonOutput, detail)
	default:
		panic("unreachable node search command")
	}
}

func parseNodeSearchArguments(command string, args []string) (jsonOutput bool, query nodeindex.Query, moduleName, version string, ok bool) {
	flags := flag.NewFlagSet("node "+command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.BoolVar(&jsonOutput, "json", false, "write JSON output")
	switch command {
	case "search":
		var categories repeatedCategories
		all := false
		flags.Var(&categories, "category", "filter by category")
		flags.BoolVar(&all, "all", false, "include incompatible packages")
		if err := flags.Parse(args); err != nil || flags.NArg() > 1 {
			return false, nodeindex.Query{}, "", "", false
		}
		text := ""
		if flags.NArg() == 1 {
			text = flags.Arg(0)
		}
		return jsonOutput, nodeindex.Query{Text: text, Categories: append([]string(nil), categories...), CompatibleOnly: !all, Limit: 50}, "", "", true
	case "info":
		flags.StringVar(&version, "version", "", "select an exact version")
		if err := flags.Parse(args); err != nil || flags.NArg() != 1 {
			return false, nodeindex.Query{}, "", "", false
		}
		return jsonOutput, nodeindex.Query{}, flags.Arg(0), version, true
	default:
		return false, nodeindex.Query{}, "", "", false
	}
}

func selectNodePackageVersion(detail nodeindex.PackageDetail, version string) (nodeindex.PackageDetail, error) {
	position := slices.IndexFunc(detail.Versions, func(candidate nodeindex.PackageVersion) bool { return candidate.Version == version })
	if position < 0 {
		return nodeindex.PackageDetail{}, nodeIndexCLIError{code: nodeindex.CodeNotFound}
	}
	detail.Versions = []nodeindex.PackageVersion{detail.Versions[position]}
	assessmentPosition := slices.IndexFunc(detail.Assessments, func(candidate nodeindex.VersionAssessment) bool { return candidate.Version == version })
	if assessmentPosition >= 0 {
		detail.Assessments = []nodeindex.VersionAssessment{detail.Assessments[assessmentPosition]}
	} else {
		detail.Assessments = []nodeindex.VersionAssessment{}
	}
	return detail, nil
}

func writeNodeSearchResult(stdout, stderr io.Writer, jsonOutput bool, result nodeindex.SearchResult) int {
	if jsonOutput {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			_, _ = io.WriteString(stderr, "节点包搜索结果输出失败\n")
			return 1
		}
		return 0
	}
	if _, err := fmt.Fprintf(stdout, "release: %s\ntotal: %d\n", safeHumanText(result.Release), result.Total); err != nil {
		return 1
	}
	for _, item := range result.Items {
		if _, err := fmt.Fprintf(stdout, "%s — %s\n", safeHumanText(item.Name), safeHumanText(item.DisplayName)); err != nil {
			return 1
		}
		if item.RecommendedVersion == nil {
			if _, err := fmt.Fprintf(stdout, "  recommended: none\n  license: %s\n  compatibility: incompatible (%s)\n", safeHumanText(item.License), safeHumanText(joinReasons(item.Reasons))); err != nil {
				return 1
			}
			continue
		}
		compatibility := item.RecommendedVersion.Compatibility
		if _, err := fmt.Fprintf(stdout, "  recommended: %s\n  license: %s\n  compatibility: nodeAPI %s; runtime [%s, %s)\n",
			safeHumanText(item.RecommendedVersion.Version), safeHumanText(item.License), safeHumanText(compatibility.NodeAPI),
			safeHumanText(compatibility.Runtime.MinVersion), safeHumanText(compatibility.Runtime.MaxVersionExclusive)); err != nil {
			return 1
		}
	}
	_, err := io.WriteString(stdout, nodeReviewDisclaimer)
	if err != nil {
		return 1
	}
	return 0
}

func writeNodeInfoResult(stdout, stderr io.Writer, jsonOutput bool, detail nodeindex.PackageDetail) int {
	if jsonOutput {
		if err := json.NewEncoder(stdout).Encode(detail); err != nil {
			_, _ = io.WriteString(stderr, "节点包详情输出失败\n")
			return 1
		}
		return 0
	}
	if _, err := fmt.Fprintf(stdout, "module: %s\ncategories: %s\nkeywords: %s\n", safeHumanText(detail.Name), safeHumanText(strings.Join(detail.Categories, ", ")), safeHumanText(strings.Join(detail.Keywords, ", "))); err != nil {
		return 1
	}
	if detail.RecommendedVersion == nil {
		if _, err := fmt.Fprintf(stdout, "recommended: none (%s)\n", safeHumanText(joinReasons(detail.Reasons))); err != nil {
			return 1
		}
	} else if _, err := fmt.Fprintf(stdout, "recommended: %s\n", safeHumanText(detail.RecommendedVersion.Version)); err != nil {
		return 1
	}
	assessments := make(map[string]nodeindex.VersionAssessment, len(detail.Assessments))
	for _, assessment := range detail.Assessments {
		assessments[assessment.Version] = assessment
	}
	for _, version := range detail.Versions {
		compatibility := version.Manifest.Compatibility
		if _, err := fmt.Fprintf(stdout,
			"version: %s\n  display name: %s\n  license: %s\n  source: %s tag %s\n  commit: %s\n  manifest digest: %s\n  review: %s at %s\n  index commit: %s\n  lifecycle: %s",
			safeHumanText(version.Version), safeHumanText(version.Manifest.Metadata.DisplayName), safeHumanText(version.Manifest.Metadata.License),
			safeHumanText(version.Source.Repository), safeHumanText(version.Source.Tag), safeHumanText(version.Source.Commit), safeHumanText(version.Source.ManifestDigest),
			safeHumanText(version.Review.Status), version.Review.ReviewedAt.UTC().Format("2006-01-02T15:04:05Z"), safeHumanText(version.Review.IndexCommit), safeHumanText(version.Lifecycle.Status)); err != nil {
			return 1
		}
		if version.Lifecycle.Message != "" {
			if _, err := fmt.Fprintf(stdout, " — %s", safeHumanText(version.Lifecycle.Message)); err != nil {
				return 1
			}
		}
		if _, err := fmt.Fprintf(stdout, "\n  declared compatibility: nodeAPI %s; runtime [%s, %s)\n", safeHumanText(compatibility.NodeAPI), safeHumanText(compatibility.Runtime.MinVersion), safeHumanText(compatibility.Runtime.MaxVersionExclusive)); err != nil {
			return 1
		}
		assessment, exists := assessments[version.Version]
		if exists && assessment.Compatible {
			if _, err := io.WriteString(stdout, "  compatibility: compatible\n"); err != nil {
				return 1
			}
		} else if exists {
			if _, err := fmt.Fprintf(stdout, "  compatibility: incompatible (%s)\n", safeHumanText(joinReasons(assessment.Reasons))); err != nil {
				return 1
			}
		}
		if _, err := fmt.Fprintf(stdout, "  nodes: %s\n", safeHumanText(nodeTypes(version))); err != nil {
			return 1
		}
	}
	if _, err := io.WriteString(stdout, nodeReviewDisclaimer); err != nil {
		return 1
	}
	return 0
}

func joinReasons(reasons []nodeindex.Reason) string {
	values := make([]string, len(reasons))
	for index, reason := range reasons {
		values[index] = string(reason)
	}
	return strings.Join(values, ", ")
}

func nodeTypes(version nodeindex.PackageVersion) string {
	values := make([]string, 0)
	for _, registration := range version.Manifest.Registrations {
		for _, node := range registration.Nodes {
			values = append(values, node.Type+"@"+node.Version)
		}
	}
	return strings.Join(values, ", ")
}
