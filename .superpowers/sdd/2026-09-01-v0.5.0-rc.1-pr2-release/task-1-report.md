# Task 1 实施报告：冻结 Runtime、SDK 与 snapshot 版本

## 实现

- Runtime 开发 fallback 固定为 `0.5.0-dev`。
- SDK `agentnode.Version` 固定为 `0.5.0`，API 版本保持 `agent-studio.dev/v1alpha1`。
- GoReleaser snapshot 模板固定为 `0.5.0-snapshot`，渲染归档版本为 `v0.5.0-snapshot`。
- Makefile `release-snapshot` 与 release workflow dry-run artifact version 同步为 `v0.5.0-snapshot`。
- 版本测试名称和期望值推进到 v0.5；check-version 与 check-release-version 测试夹具同步到 v0.5，并继续覆盖非法 tag、origin/main、版本不匹配、dirty/untracked、ignored cache、陈旧 snapshot 和双 `v`。

## RED

命令：

```text
GOCACHE=/tmp/agent-studio-gocache-red CGO_ENABLED=0 go test ./internal/buildinfo ./sdk/go/agentnode -count=1
```

关键输出：`TestPublicVersionsForV050` 报 `sdk=0.4.0`，`TestVersionForV050` 报 `Version = "0.4.0", want "0.5.0"`，buildinfo fallback/SDK 期望也因生产常量仍为 v0.4 而失败。

命令：

```text
sh scripts/check-version_test.sh
```

关键输出：`check-version tests passed`（该脚本验证逻辑不依赖仓库生产版本常量，因此测试夹具推进后即通过）。

命令：

```text
sh scripts/check-release-version_test.sh
```

关键输出：`rendered snapshot version mismatch in .goreleaser.yaml (rendered): got v0.4.0-snapshot, want v0.5.0-snapshot`。

## GREEN

命令：

```text
GOCACHE=/tmp/agent-studio-gocache-green CGO_ENABLED=0 go test ./internal/buildinfo ./sdk/go/agentnode -count=1
GOCACHE=/tmp/agent-studio-gocache-green CGO_ENABLED=0 go run ./cmd/agent-studio version
sh scripts/check-version_test.sh
sh scripts/check-release-version_test.sh
```

关键输出：两个 Go 包均 `ok`；CLI 输出为：

```text
agent-studio 0.5.0-dev (sdk 0.5.0; api agent-studio.dev/v1alpha1; commit unknown; dirty false)
```

脚本输出分别为 `check-version tests passed`、`check-release-version tests passed`。

## 文件

- `.github/workflows/release.yml`
- `.goreleaser.yaml`
- `Makefile`
- `internal/buildinfo/buildinfo.go`
- `internal/buildinfo/buildinfo_test.go`
- `sdk/go/agentnode/node.go`
- `sdk/go/agentnode/node_test.go`
- `scripts/check-version_test.sh`
- `scripts/check-release-version_test.sh`

## Self-review

- 已逐项对照版本矩阵，确认 Runtime、SDK、API、GoReleaser、渲染 snapshot、workflow dry snapshot 均一致。
- 已确认测试只推进版本期望/夹具，保留非法 tag、缺失 origin/main、版本不匹配、工作区状态和 ignored cache 覆盖。
- 已运行 `git diff --check`，无空白错误。
- 未 push、未创建 PR/Tag/Release。

## Concerns

- Go 测试使用 `/tmp/agent-studio-gocache-*`，因为默认用户 Go build cache 在受限环境中不可写；这不影响测试结果。
- 未执行完整仓库测试套件，按 Task 1 范围执行指定测试。
