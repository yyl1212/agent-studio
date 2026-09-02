# Task 2 实施报告：官方节点兼容范围与 v0.5 浏览器夹具

## 实现

- 官方根 manifest 的运行时范围更新为 `[v0.2.0, v0.6.0)`；`nodeAPI`、registration 与节点定义保持不变。
- 生成 golden path 使用 `--runtime-min v0.2.0 --runtime-max-exclusive v0.6.0`，并验证生成 package manifest 写出 `maxVersionExclusive: v0.6.0`。
- 仓库 release contract 以 Runtime `0.5.0-dev`、SDK `0.5.0`、tag `v0.5.0` 验证根 manifest 无兼容性错误。
- 浏览器索引夹具的 Future 节点调整为 `[v0.6.0, v0.7.0)`；两个 Search 节点版本调整为 `[v0.2.0, v0.6.0)`；索引资产 release 仍为 `v0.4.0`。
- Future 详情现在展示每个版本 manifest 的运行时兼容范围，因此可见 `运行时 v0.6.0 至 v0.7.0（不含）`，并保留“当前运行时版本过低”、本地缓存和无远端请求断言。

## 计划边界裁决

brief 的 Files 列表未包含 `apps/web/src/features/node-packages/NodePackageCard.tsx`，但同时要求 E2E 断言 Future **详情**展示运行时范围。经检查，原详情只展示兼容诊断与来源元数据，没有渲染 `version.manifest.compatibility.runtime`；该断言无法由夹具或测试本身满足。已获父任务明确授权，最小扩展该组件和相邻真实 UI 单测，仅增加详情元数据行，未修改兼容算法、交互、节点定义或其余展示。

## RED

```text
CGO_ENABLED=0 go test ./internal/generatedtest -run 'GoldenPath|RepositoryManifest' -count=1
```

关键输出：`TestRepositoryManifestSupportsV050Runtime` 失败：`max runtime=v0.5.0 want=v0.6.0`。

```text
RUN_PAYLOAD_ENCRYPTION_KEY=<.env.example 值> corepack pnpm@10.34.5 --filter @agent-studio/web exec playwright test e2e/node-packages.spec.ts
```

关键输出：Future 详情断言失败，未找到 `运行时 v0.6.0 至 v0.7.0（不含）`。

```text
corepack pnpm@10.34.5 --filter @agent-studio/web exec vitest run src/features/node-packages/NodePackageCard.test.tsx
```

关键输出：新增“展开详情时显示每个版本的运行时兼容范围”测试失败，详情区未找到 `运行时 v0.3.0 至 v0.4.0（不含）`。

说明：直接按 brief 首次运行 Playwright 时，现有运行脚本要求 `RUN_PAYLOAD_ENCRYPTION_KEY`；补充仓库 `.env.example` 的测试值后才获得上述真实 RED。临时保持旧 Future 范围时，Task 1 的 `0.5.0-dev` 会被节点索引正规化为 `v0.5.0`，使旧 `[v0.5.0,v0.6.0)` Future 被判为兼容、摘要由 1 变为 2；这是过渡夹具与已冻结 Runtime 矩阵的预期交互，不是本次实现缺陷。

## GREEN

```text
CGO_ENABLED=0 go test ./internal/generatedtest -run 'GoldenPath|RepositoryManifest' -count=1
```

关键输出：`ok github.com/yyl1212/agent-studio/internal/generatedtest 1.983s`。

```text
corepack pnpm@10.34.5 --filter @agent-studio/web exec vitest run src/features/node-packages/NodePackageCard.test.tsx
corepack pnpm@10.34.5 --filter @agent-studio/web run typecheck
```

关键输出：Vitest `6 passed`；TypeScript `tsc --noEmit` 成功。

```text
RUN_PAYLOAD_ENCRYPTION_KEY=<.env.example 值> corepack pnpm@10.34.5 --filter @agent-studio/web exec playwright test e2e/node-packages.spec.ts
```

关键输出：`1 passed (3.8s)`。

## 文件

- `agent-studio.node-package.json`
- `internal/generatedtest/golden_path_test.go`
- `internal/generatedtest/repository_release_contract_test.go`
- `apps/web/e2e/fixtures/node-index/index.json`
- `apps/web/e2e/node-packages.spec.ts`
- `apps/web/src/features/node-packages/NodePackageCard.tsx`（经边界裁决新增）
- `apps/web/src/features/node-packages/NodePackageCard.test.tsx`（经边界裁决新增）

## Self-review

- 按矩阵核对根 manifest、golden CLI、repository contract、Future 与两个 Search 夹具范围；fixture release 仍为 `v0.4.0`。
- E2E 验证真实页面：Future 仍显示“当前运行时版本过低”，详情显示精确的 v0.6-v0.7 半开区间，且无 GitHub/远端请求。
- 组件单测验证展开后的真实可见详情内容；未对 mock 调用次数作断言。
- `git diff --check` 将在提交前重新执行。

## Concerns

- brief 指定的 `CGO_ENABLED=0 go run ./cmd/agent-studio node inspect .` 仍以 `NODE_PACKAGE_ID_MISMATCH: 注册包 import path 无效` 退出。三条 registration 与父提交完全一致，错误发生于兼容检查前的 import-path 校验；根据 brief 未修改 registration。此基线限制不影响通过的 release contract、golden path、组件单测、TypeScript 类型检查和 Playwright E2E。
- Playwright 需要运行脚本既有的 `RUN_PAYLOAD_ENCRYPTION_KEY`，测试命令使用仓库 `.env.example` 提供的非敏感示例值；未写入任何环境文件。
