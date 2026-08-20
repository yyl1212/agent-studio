package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/yyl1212/agent-studio/internal/buildinfo"
	"github.com/yyl1212/agent-studio/internal/nodeindex"
)

const nodeIndexUsage = "node index usage: node index <status|refresh> [--json]\n"

type indexStatusCatalog interface {
	Status() nodeindex.Status
}

type nodeIndexDependencies struct {
	configuredCacheDir func() string
	resolveCacheDir    func(string) (string, error)
	runtime            func() nodeindex.Runtime
	openCatalog        func(string, nodeindex.Runtime) (indexStatusCatalog, error)
	refresh            func(context.Context, string) (nodeindex.RefreshResult, error)
}

type nodeIndexErrorOutput struct {
	Code    nodeindex.Code `json:"code"`
	Message string         `json:"message"`
}

type nodeIndexCLIError struct {
	code nodeindex.Code
}

func (err nodeIndexCLIError) Error() string {
	return string(err.code)
}

func nodeIndexCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return runNodeIndex(ctx, args, stdout, stderr, nodeIndexDependencies{
		configuredCacheDir: func() string { return os.Getenv("AGENT_STUDIO_NODE_INDEX_CACHE_DIR") },
		resolveCacheDir:    nodeindex.ResolveCacheDir,
		runtime: func() nodeindex.Runtime {
			info := buildinfo.Current()
			return nodeindex.Runtime{Version: info.Version, NodeAPI: info.APIVersion}
		},
		openCatalog: func(cacheDir string, runtime nodeindex.Runtime) (indexStatusCatalog, error) {
			store, err := nodeindex.OpenStore(cacheDir)
			if err != nil {
				return nil, err
			}
			return nodeindex.NewCatalog(store, runtime), nil
		},
		refresh: func(ctx context.Context, cacheDir string) (nodeindex.RefreshResult, error) {
			client := &http.Client{Timeout: 30 * time.Second}
			source := nodeindex.NewGitHubSource(client)
			return nodeindex.NewRefresher(cacheDir, source).Refresh(ctx)
		},
	})
}

func runNodeIndex(ctx context.Context, args []string, stdout, stderr io.Writer, dependencies nodeIndexDependencies) int {
	command, jsonOutput, ok := parseNodeIndexArguments(args)
	if !ok {
		_, _ = io.WriteString(stderr, nodeIndexUsage)
		return 2
	}
	cacheDir, err := dependencies.resolveCacheDir(dependencies.configuredCacheDir())
	if err != nil {
		return writeNodeIndexError(stderr, jsonOutput, err)
	}
	runtime := dependencies.runtime()

	switch command {
	case "status":
		catalog, err := dependencies.openCatalog(cacheDir, runtime)
		if err != nil {
			return writeNodeIndexError(stderr, jsonOutput, err)
		}
		return writeNodeIndexStatus(stdout, stderr, jsonOutput, cacheDir, catalog.Status())
	case "refresh":
		result, err := dependencies.refresh(ctx, cacheDir)
		if err != nil {
			return writeNodeIndexError(stderr, jsonOutput, err)
		}
		catalog, err := dependencies.openCatalog(cacheDir, runtime)
		if err != nil {
			return writeNodeIndexError(stderr, jsonOutput, err)
		}
		if catalog.Status().Release != result.Release {
			return writeNodeIndexError(stderr, jsonOutput, errors.New("refreshed node index is not active"))
		}
		return writeNodeIndexRefresh(stdout, stderr, jsonOutput, result)
	default:
		panic("unreachable node index command")
	}
}

func parseNodeIndexArguments(args []string) (command string, jsonOutput bool, ok bool) {
	if len(args) < 1 || len(args) > 2 || (args[0] != "status" && args[0] != "refresh") {
		return "", false, false
	}
	if len(args) == 2 {
		if args[1] != "--json" {
			return "", false, false
		}
		jsonOutput = true
	}
	return args[0], jsonOutput, true
}

func writeNodeIndexStatus(stdout, stderr io.Writer, jsonOutput bool, cacheDir string, status nodeindex.Status) int {
	if jsonOutput {
		if err := json.NewEncoder(stdout).Encode(status); err != nil {
			_, _ = io.WriteString(stderr, "节点索引状态输出失败\n")
			return 1
		}
		return 0
	}
	if _, err := fmt.Fprintf(stdout, "source: %s\nrelease: %s\ngenerated: %s\npackages: %d (compatible: %d)\n",
		safeHumanText(string(status.Source)), safeHumanText(status.Release), status.GeneratedAt.UTC().Format(time.RFC3339), status.PackageCount, status.CompatiblePackageCount); err != nil {
		return 1
	}
	if status.WarningCode != nil {
		if _, err := fmt.Fprintf(stdout, "warning: %s\n", safeHumanText(string(*status.WarningCode))); err != nil {
			return 1
		}
	}
	if _, err := fmt.Fprintf(stdout, "cache: %s\n", safeHumanText(filepath.Join(cacheDir, "index.json"))); err != nil {
		return 1
	}
	return 0
}

func writeNodeIndexRefresh(stdout, stderr io.Writer, jsonOutput bool, result nodeindex.RefreshResult) int {
	if jsonOutput {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			_, _ = io.WriteString(stderr, "节点索引刷新结果输出失败\n")
			return 1
		}
		return 0
	}
	var err error
	switch result.Status {
	case nodeindex.RefreshUpdated:
		_, err = fmt.Fprintf(stdout, "updated %s -> %s\n", result.PreviousRelease, result.Release)
	case nodeindex.RefreshAlreadyCurrent:
		_, err = fmt.Fprintf(stdout, "already current %s\n", result.Release)
	default:
		return writeNodeIndexError(stderr, false, errors.New("node index refresh returned an unknown status"))
	}
	if err != nil {
		return 1
	}
	return 0
}

func writeNodeIndexError(stderr io.Writer, jsonOutput bool, err error) int {
	code := nodeindex.CodeOf(err)
	var cliError nodeIndexCLIError
	if code == "" && errors.As(err, &cliError) {
		code = cliError.code
	}
	if code == "" {
		code = nodeindex.CodeContentInvalid
	}
	output := nodeIndexErrorOutput{Code: code, Message: nodeIndexErrorMessage(code)}
	if jsonOutput {
		_ = json.NewEncoder(stderr).Encode(output)
	} else {
		_, _ = fmt.Fprintf(stderr, "%s: %s\n", output.Code, output.Message)
	}
	return 1
}

func safeHumanText(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) < 2 {
		return ""
	}
	return string(encoded[1 : len(encoded)-1])
}

func nodeIndexErrorMessage(code nodeindex.Code) string {
	switch code {
	case nodeindex.CodeRateLimited:
		return "GitHub 请求受到限流，请稍后重试"
	case nodeindex.CodeReleaseNotFound:
		return "未找到可用的节点索引 Release"
	case nodeindex.CodeReleaseDowngrade:
		return "已拒绝节点索引降级"
	case nodeindex.CodeRefreshInProgress:
		return "另一个节点索引刷新正在进行"
	case nodeindex.CodeRefreshUnsupported:
		return "当前平台不支持节点索引刷新"
	case nodeindex.CodeCacheWriteFailed:
		return "节点索引缓存写入失败"
	case nodeindex.CodeDigestMismatch:
		return "节点索引文件校验失败"
	case nodeindex.CodeReleaseInvalid, nodeindex.CodeAssetInvalid:
		return "节点索引 Release 无效"
	case nodeindex.CodeSchemaUnsupported:
		return "节点索引格式版本不受支持"
	case nodeindex.CodeNotFound:
		return "未找到节点包"
	default:
		return "节点索引内容无效"
	}
}
