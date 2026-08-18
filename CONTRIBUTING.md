# 贡献指南

感谢你改进 Agent Studio。提交代码表示你同意贡献内容按仓库的
[Apache License 2.0](LICENSE) 发布，并遵守
[行为准则](CODE_OF_CONDUCT.md)。

## 环境

- Go 1.26，项目 toolchain 为 1.26.5
- Node.js 24
- Corepack 与 pnpm 10.34.5
- 完整回归需要 Docker Desktop 或 Docker Compose

安装依赖：

```bash
corepack enable
corepack pnpm@10.34.5 install --frozen-lockfile
```

## 开发流程

1. 从最新的 `main` 创建 `codex/<简短主题>` 分支。
2. 先编写能稳定复现需求或缺陷的失败测试。
3. 编写使测试通过的最小实现，并保持提交范围单一。
4. 运行 `make verify-quick`。
5. 涉及数据库或发布门禁时再运行 `make verify`。
6. 在 Pull Request 中说明行为变化、测试证据和安全影响。

所有 Go 调试、测试和生成命令必须设置 `CGO_ENABLED=0`。

## 生成文件

不要直接编辑以下文件：

- `apps/api/internal/generated/nodes_gen.go`
- `apps/web/src/lib/api/generated.ts`

节点 manifest 变化后运行：

```bash
CGO_ENABLED=0 go run ./cmd/agent-studio generate
```

OpenAPI 变化后运行：

```bash
corepack pnpm@10.34.5 --filter @agent-studio/web generate:api
```

## 验证

不依赖 Docker 的提交前检查：

```bash
make verify-quick
```

需要 Docker PostgreSQL 的完整检查：

```bash
make verify
make test-e2e
make test-sdk-e2e
```

## 安全问题

不要在公开 Issue、Pull Request、日志或截图中提交密钥和未披露漏洞。
安全问题请按 [安全策略](SECURITY.md) 私密报告。
