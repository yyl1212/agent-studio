# Open Source Release Engineering and RC Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**目标：** 为 Agent Studio 建立 Apache-2.0 开源治理、可复现 CI、静态前端单容器、macOS/Linux 发布包、GHCR 多架构镜像、SBOM 和 `v0.2.0-rc.1` 验收流程。

**架构：** 开发态仍使用 Vite + Go API + Docker PostgreSQL；发布态由 Go server 同源托管 Vite 静态产物。GoReleaser 生成 CLI/server 的 Darwin/Linux amd64/arm64 压缩包，Docker Buildx 生成 linux/amd64 与 linux/arm64 镜像。Git tag 驱动二进制和容器发布，SBOM、校验和及冒烟测试构成发布门禁。

**技术栈：** GitHub Actions、GoReleaser 2.17.0、Docker Buildx、PostgreSQL 18、Node 24、pnpm 10.34.5、Playwright Chromium、govulncheck 1.6.0、Anchore SBOM Action 0.24.0。

---

## 执行门禁

- [ ] 确认 SDK 基础与工具链两份计划已经完成并进入本地 `master`：

  ```bash
  test -f sdk/go/agentnode/node.go
  test -f cmd/agent-studio/main.go
  test -f agent-studio.nodes.yaml
  test -f apps/api/internal/generated/nodes_gen.go
  make check-generated
  make test-sdk-e2e
  ```

- [ ] 使用 `superpowers:using-git-worktrees` 从上述本地 `master` 创建 `codex/open-source-release-engineering`。

- [ ] 本计划只创建发布配置和本地 snapshot；实际推送 tag、创建 GitHub Release、发布 GHCR 镜像需要用户明确授权，不因执行本计划自动发生。

## 全局验收约束

- PR 工作流仅有 `contents: read`，不读取发布 secrets，不使用 `pull_request_target`。
- 发布工作流仅响应受保护的 `v*` tag 或手工 dispatch；发布 job 使用最小写权限。
- Go 构建和调试均设置 `CGO_ENABLED=0`。
- Docker 最终镜像以 nonroot 用户运行，不包含 Go/Node 工具链、源码、pnpm store 或 `.git`。
- `/api/*` 未知路由保持 JSON 404；SPA fallback 不吞掉 API 错误。
- 二进制包、容器镜像、校验和、SBOM 使用同一 tag，并能追溯到同一 Git SHA。

## 文件地图

| 文件 | 操作 | 职责 |
|---|---|---|
| `LICENSE`、`NOTICE` | 新增 | Apache-2.0 授权和项目声明 |
| `CONTRIBUTING.md`、`SECURITY.md`、`CODE_OF_CONDUCT.md` | 新增 | 贡献、安全和社区流程 |
| `.github/ISSUE_TEMPLATE/*`、`.github/pull_request_template.md` | 新增 | 标准化社区反馈 |
| `.github/dependabot.yml` | 新增 | Go、pnpm、Actions、Docker 依赖更新 |
| `internal/buildinfo/buildinfo.go` | 新增 | CLI/server 共享版本与提交信息 |
| `apps/web/playwright.config.ts` | 修改 | 改用 Playwright 自带 Chromium |
| `.github/workflows/ci.yml` | 新增 | PR/主干全量验证与安全扫描 |
| `apps/api/internal/httpapi/spa.go` | 新增 | 同源静态资源与 SPA fallback |
| `apps/api/internal/config/config.go` | 修改 | `WEB_DIST_DIR` 发布配置 |
| `apps/api/cmd/server/main.go` | 修改 | 注入静态文件系统和 build info |
| `apps/api/cmd/healthcheck/*` | 新增 | distroless 容器健康检查二进制 |
| `Dockerfile`、`.dockerignore` | 新增 | nonroot 多阶段生产镜像 |
| `compose.yaml` | 修改 | 增加本地生产态 app 服务 |
| `.goreleaser.yaml` | 新增 | 跨平台二进制与归档 |
| `.github/workflows/container.yml` | 新增 | GHCR 多架构镜像 |
| `.github/workflows/release.yml` | 新增 | tag 发布、SBOM 与校验 |
| `scripts/check-version.sh` | 新增 | tag/API/CLI 版本一致性 |
| `scripts/dependency-licenses.sh` | 新增 | Go/Web 依赖许可证清单 |
| `scripts/release-smoke.sh` | 新增 | 容器发布闭环冒烟 |
| `scripts/compose.smoke.yaml` | 新增 | 隔离的临时冒烟环境 |
| `docs/releases/v0.2.0-rc.1-checklist.md` | 新增 | RC 实测记录与退出条件 |
| `docs/releases/v0.2.0-rc.1.md`、`docs/releases/v0.2.0.md` | 新增 | 中文发布说明 |
| `docs/releases/releasing.md` | 新增 | 维护者发布手册 |
| `README.md` | 修改 | 安装、镜像、许可证和社区入口 |

## Task 1：补齐 Apache-2.0 和社区治理文件

**文件：**

- 新增：`LICENSE`
- 新增：`NOTICE`
- 新增：`CONTRIBUTING.md`
- 新增：`SECURITY.md`
- 新增：`CODE_OF_CONDUCT.md`
- 新增：`.github/ISSUE_TEMPLATE/bug.yml`
- 新增：`.github/ISSUE_TEMPLATE/feature.yml`
- 新增：`.github/ISSUE_TEMPLATE/config.yml`
- 新增：`.github/pull_request_template.md`
- 新增：`.github/dependabot.yml`

- [ ] 创建 `LICENSE`，内容逐字采用 Apache License 2.0 官方文本，首行和版本行为：

  ```text
  Apache License
  Version 2.0, January 2004
  http://www.apache.org/licenses/
  ```

  文件末尾必须是官方 “limitations under the License” 段落，不添加项目特有条款。

- [ ] 创建 `NOTICE`：

  ```text
  Agent Studio
  Copyright 2026 Agent Studio contributors

  This product includes software developed by the Agent Studio contributors.
  ```

- [ ] `CONTRIBUTING.md` 用中文说明开发分支必须使用 `codex/` 前缀、TDD、生成文件检查、提交粒度、代码审查和以下唯一验证入口：

  ```bash
  agent-studio doctor
  make verify
  make test-e2e
  make test-sdk-e2e
  make security
  ```

- [ ] `SECURITY.md` 明确仅通过 GitHub Security Advisories 的 “Report a vulnerability” 私密报告安全问题；禁止在公开 Issue 提交密钥、利用细节或真实工作流配置。支持范围固定为最新 minor 版本。

- [ ] `CODE_OF_CONDUCT.md` 采用 Contributor Covenant 2.1 标准正文，执法联系人统一为 GitHub 仓库维护者，不填写虚构邮箱。

- [ ] Issue Form 要求复现版本、操作系统、最小复现和脱敏日志；安全问题链接到 `SECURITY.md`。PR 模板要求勾选测试、生成文件、文档、安全和兼容性。

- [ ] Dependabot 每周检查 `gomod`、`npm`、`github-actions` 和 `docker`，每类最多 5 个打开 PR，目标分支 `master`。

- [ ] 检查并提交：

  ```bash
  rg -n 'TODO|TBD|example@example|your-email' LICENSE NOTICE CONTRIBUTING.md SECURITY.md CODE_OF_CONDUCT.md .github
  git diff --check
  git add LICENSE NOTICE CONTRIBUTING.md SECURITY.md CODE_OF_CONDUCT.md .github
  git commit -m "docs: add open source community governance"
  ```

  第一条预期无输出。

## Task 2：统一构建版本并校验 tag

**文件：**

- 新增：`internal/buildinfo/buildinfo.go`
- 新增：`internal/buildinfo/buildinfo_test.go`
- 修改：`internal/cli/app.go`
- 修改：`internal/cli/app_test.go`
- 修改：`apps/api/cmd/server/main.go`
- 新增：`scripts/check-version.sh`

- [ ] 先写测试，断言默认开发版本、注入版本和 CLI `version` 输出；默认值不得伪装成已发布 tag。

- [ ] 运行红灯测试：

  ```bash
  CGO_ENABLED=0 go test ./internal/buildinfo ./internal/cli -run Version -count=1
  ```

- [ ] 实现可由 ldflags 覆盖的构建信息：

  ```go
  package buildinfo

  import "github.com/yyl1212/agent-studio/sdk/go/agentnode"

  var Version = agentnode.Version + "-dev"
  var Commit = "unknown"
  var BuildDate = "unknown"
  ```

  CLI 输出 `agent-studio <Version> (<APIVersion>, commit <Commit>)`；`agent-studio version --sdk` 只输出 `agentnode.Version`，供发布脚本机器读取。server 启动日志记录相同三项，但 HTTP API 不暴露绝对构建路径。

- [ ] `scripts/check-version.sh` 使用 `set -eu`，只接受一个 `vMAJOR.MINOR.PATCH` 或 `vMAJOR.MINOR.PATCH-rc.N` 参数。通过 `CGO_ENABLED=0 go run ./cmd/agent-studio version --sdk` 读取 SDK 版本；剥离 tag 的 `v` 和 prerelease 后必须与其相等，非法或不一致退出 1。

- [ ] 测试脚本边界：

  ```bash
  scripts/check-version.sh v0.2.0
  scripts/check-version.sh v0.2.0-rc.1
  if scripts/check-version.sh v0.3.0; then exit 1; fi
  if scripts/check-version.sh latest; then exit 1; fi
  ```

- [ ] 验证并提交：

  ```bash
  CGO_ENABLED=0 go test ./internal/buildinfo ./internal/cli ./apps/api/cmd/server -count=1
  git add internal/buildinfo internal/cli apps/api/cmd/server scripts/check-version.sh
  git commit -m "chore(release): add reproducible build metadata"
  ```

## Task 3：建立 PR/主干 CI 与安全扫描

**文件：**

- 新增：`.github/workflows/ci.yml`
- 修改：`apps/web/playwright.config.ts`
- 修改：`Makefile`

- [ ] 先移除 Playwright 对系统 Chrome 的依赖：`projects` 使用 `devices['Desktop Chrome']`，但删除 `channel: 'chrome'`。本地执行：

  ```bash
  corepack pnpm@10.34.5 --filter @agent-studio/web exec playwright install chromium
  make test-e2e
  ```

- [ ] Makefile 增加安全目标：

  ```make
  GOVULNCHECK_VERSION ?= v1.6.0
  GO_LICENSES_VERSION ?= v2.0.1

  license-check:
	GOBIN=$$(pwd)/.tmp/bin CGO_ENABLED=0 go install github.com/google/go-licenses/v2@$(GO_LICENSES_VERSION)
	.tmp/bin/go-licenses check ./...
	corepack pnpm@10.34.5 licenses list --prod --json >/dev/null

  security: license-check
	GOBIN=$$(pwd)/.tmp/bin CGO_ENABLED=0 go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	.tmp/bin/govulncheck ./...
	corepack pnpm@10.34.5 audit --prod --audit-level=high
  ```

  同时把 `.tmp/` 加入 `.gitignore`；该目录仅保存可重建工具，不进入发布包。

- [ ] 创建 `ci.yml`，触发条件固定为 `pull_request` 和推送到 `master`，并设置：

  ```yaml
  permissions:
    contents: read

  concurrency:
    group: ci-${{ github.workflow }}-${{ github.ref }}
    cancel-in-progress: true
  ```

- [ ] `verify` job 使用 Ubuntu runner，步骤和版本固定为：

  ```yaml
  - uses: actions/checkout@v7
  - uses: actions/setup-go@v7
    with:
      go-version-file: go.mod
      cache: true
  - uses: actions/setup-node@v6
    with:
      node-version: 24
  - run: corepack enable
  - run: corepack pnpm@10.34.5 install --frozen-lockfile
  - run: corepack pnpm@10.34.5 --filter @agent-studio/web exec playwright install --with-deps chromium
  - run: make verify
  - run: make test-e2e
  - run: make test-sdk-e2e
  ```

  PostgreSQL 必须由现有 `docker compose up -d --wait db` 启动，禁止改为内存数据库。

- [ ] `security` job 在同一依赖安装基础上执行 `make security`。`go-licenses check` 的未知/禁止许可证、`govulncheck` 数据库或 npm registry 无法访问均应标记 job 失败，不静默跳过；维护者可从 GitHub UI 重跑。

- [ ] 所有 job 增加 30 分钟超时；无条件执行 `docker compose down --volumes` 清理 CI 临时数据库。不得删除开发者本地命名卷。

- [ ] 使用 actionlint（若本机已安装）和真实命令验证；未安装时使用 Docker 镜像 `rhysd/actionlint:1.7.7` 只读挂载仓库：

  ```bash
  docker run --rm -v "$PWD:/repo:ro" -w /repo rhysd/actionlint:1.7.7
  make verify
  make test-e2e
  make test-sdk-e2e
  make security
  ```

- [ ] 提交 CI：

  ```bash
  git add .github/workflows/ci.yml apps/web/playwright.config.ts Makefile .gitignore
  git commit -m "ci: verify sdk canvas and dependencies"
  ```

## Task 4：让 Go server 安全托管 SPA

**文件：**

- 新增：`apps/api/internal/httpapi/spa.go`
- 新增：`apps/api/internal/httpapi/spa_test.go`
- 修改：`apps/api/internal/httpapi/router.go`
- 修改：`apps/api/internal/httpapi/router_test.go`
- 修改：`apps/api/internal/config/config.go`
- 修改：`apps/api/internal/config/config_test.go`
- 修改：`apps/api/cmd/server/main.go`

- [ ] 先写静态服务测试，覆盖 `/`、`/assets/app.js`、`/workflows/demo` fallback、HEAD 请求、缓存头、缺少 index、目录穿越，以及 `/api/not-found` 仍返回 JSON 404。

- [ ] 运行红灯测试：

  ```bash
  CGO_ENABLED=0 go test ./apps/api/internal/httpapi ./apps/api/internal/config -run 'SPA|WebDist' -count=1
  ```

- [ ] 在 Router 依赖中新增可选文件系统：

  ```go
  type Dependencies struct {
      Registry  *nodes.Registry
      Workflows WorkflowService
      Runner    Runner
      Runs      RunReader
      Readiness Readiness
      WebOrigin string
      Web       fs.FS
      Logger    *slog.Logger
  }
  ```

- [ ] `spa.go` 实现：

  ```go
  func NewSPA(root fs.FS) (http.Handler, error)
  ```

  构造时读取 `index.html` 并失败即返回错误；请求路径先 `path.Clean`，拒绝反斜杠和 `..` 段。存在文件交给 `http.FileServerFS`；不存在且 Accept 允许 HTML 时返回 index；其他返回 404。带 hash 的 `/assets/*` 设置一年 immutable，index 设置 `no-cache`。

- [ ] Router 顺序固定为 health/readiness、`/api`、最后可选 SPA。`/api/*` 的 NotFound 使用现有 `ErrorResponse`，不得进入 SPA。

- [ ] Config 增加：

  ```go
  WebDistDir string
  ```

  从 `WEB_DIST_DIR` 读取，默认空字符串表示开发态不托管前端。非空时 server 用 `os.DirFS` 注入；路径不存在或 index 缺失时启动失败。

- [ ] 全量路由回归并提交：

  ```bash
  CGO_ENABLED=0 go test ./apps/api/internal/httpapi ./apps/api/internal/config ./apps/api/cmd/server -count=1
  CGO_ENABLED=0 go test ./... -count=1
  git add apps/api/internal/httpapi apps/api/internal/config apps/api/cmd/server
  git commit -m "feat(server): serve the production web application"
  ```

## Task 5：构建 nonroot 单容器镜像

**文件：**

- 新增：`apps/api/cmd/healthcheck/main.go`
- 新增：`apps/api/cmd/healthcheck/main_test.go`
- 新增：`Dockerfile`
- 新增：`.dockerignore`
- 修改：`compose.yaml`
- 修改：`Makefile`

- [ ] 先为 healthcheck 写 `httptest.Server` 测试：`/readyz` 2xx 返回 0，非 2xx、连接失败和超过 2 秒均返回非零。

- [ ] 实现只依赖标准库的 healthcheck；固定请求 `http://127.0.0.1:8080/readyz`，可通过 `HEALTHCHECK_URL` 覆盖，响应体始终关闭且最多读取 4 KiB。

- [ ] 创建三阶段 Dockerfile，逻辑固定为：

  ```dockerfile
  FROM node:24-bookworm-slim AS web-build
  WORKDIR /src
  COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
  COPY apps/web/package.json apps/web/package.json
  RUN corepack pnpm@10.34.5 install --frozen-lockfile
  COPY apps/web apps/web
  COPY contracts contracts
  RUN corepack pnpm@10.34.5 --filter @agent-studio/web build

  FROM golang:1.26-bookworm AS go-build
  WORKDIR /src
  COPY go.mod go.sum ./
  RUN go mod download
  COPY . .
  ARG VERSION=0.2.0-dev
  ARG COMMIT=unknown
  ARG BUILD_DATE=unknown
  RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/yyl1212/agent-studio/internal/buildinfo.Version=${VERSION} -X github.com/yyl1212/agent-studio/internal/buildinfo.Commit=${COMMIT} -X github.com/yyl1212/agent-studio/internal/buildinfo.BuildDate=${BUILD_DATE}" -o /out/server ./apps/api/cmd/server
  RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/healthcheck ./apps/api/cmd/healthcheck

  FROM gcr.io/distroless/static-debian12:nonroot
  WORKDIR /app
  COPY --from=go-build /out/server /app/server
  COPY --from=go-build /out/healthcheck /app/healthcheck
  COPY --from=web-build /src/apps/web/dist /app/web
  ENV HTTP_ADDR=:8080 WEB_DIST_DIR=/app/web
  EXPOSE 8080
  USER nonroot:nonroot
  HEALTHCHECK --interval=10s --timeout=3s --retries=6 CMD ["/app/healthcheck"]
  ENTRYPOINT ["/app/server"]
  ```

- [ ] `.dockerignore` 排除 `.git`、`.env*`、`node_modules`、`dist`、Playwright 报告、测试缓存、`.tmp` 和本地数据库数据；不得排除源码或 contracts。

- [ ] `compose.yaml` 增加 `app`：从当前 Dockerfile 构建，依赖健康的 db，使用 `postgres://agent:agent@db:5432/agent_studio?sslmode=disable`，设置 `MODEL_PROVIDER=mock`，映射 `8080:8080`。现有 `db-up` 仍只启动 db。

- [ ] Makefile 增加 `image`、`up`、`down`：

  ```make
  image:
	docker build --build-arg VERSION=0.2.0-dev --build-arg COMMIT=$$(git rev-parse --short=12 HEAD) -t agent-studio:dev .

  up:
	docker compose up -d --build --wait app

  down:
	docker compose down
  ```

- [ ] 构建并检查镜像：

  ```bash
  make image
  docker image inspect agent-studio:dev --format '{{.Config.User}}'
  docker run --rm --entrypoint /app/healthcheck agent-studio:dev
  ```

  第二条预期是 nonroot UID/用户名。第三条预期因没有 server 而非零，证明 healthcheck 独立执行而非 shell 脚本。

- [ ] 启动闭环并提交：

  ```bash
  make up
  curl --fail http://127.0.0.1:8080/readyz
  curl --fail http://127.0.0.1:8080/workflows
  make down
  git add apps/api/cmd/healthcheck Dockerfile .dockerignore compose.yaml Makefile
  git commit -m "feat(release): build a nonroot full stack image"
  ```

## Task 6：配置跨平台二进制归档

**文件：**

- 新增：`.goreleaser.yaml`
- 新增：`scripts/dependency-licenses.sh`
- 修改：`Makefile`
- 新增：`docs/releases/binary-layout.md`

- [ ] 创建 GoReleaser v2 配置。builds 固定为 `agent-studio`（`./cmd/agent-studio`）和 `agent-studio-server`（`./apps/api/cmd/server`），共同设置：

  ```yaml
  version: 2
  project_name: agent-studio

  builds:
    - id: agent-studio
      main: ./cmd/agent-studio
      binary: agent-studio
      env: [CGO_ENABLED=0]
      goos: [darwin, linux]
      goarch: [amd64, arm64]
      flags: [-trimpath]
    - id: agent-studio-server
      main: ./apps/api/cmd/server
      binary: agent-studio-server
      env: [CGO_ENABLED=0]
      goos: [darwin, linux]
      goarch: [amd64, arm64]
      flags: [-trimpath]
  ```

  两个 build 的 ldflags 都注入 `internal/buildinfo` 三个变量；Version 使用 `{{.Version}}`，Commit 使用 `{{.FullCommit}}`，BuildDate 使用 `{{.Date}}`。

- [ ] archives 每个平台一个 `tar.gz`，同时包含两个二进制、`LICENSE`、`NOTICE`、`README.md` 和 `apps/web/dist` 映射为 `web/`。checksums 文件名固定 `checksums.txt`，算法 SHA-256。

- [ ] changelog 按 feat/fix/docs/chore 分组，排除 merge 和 test-only 提交；release prerelease 状态从 semver tag 自动判断。

- [ ] 文档说明解压布局和 server 启动方式：

  ```bash
  WEB_DIST_DIR=./web DATABASE_URL='postgres://agent:agent@localhost:5432/agent_studio?sslmode=disable' ./agent-studio-server
  ```

- [ ] `scripts/dependency-licenses.sh <output-dir>` 固定安装 `github.com/google/go-licenses/v2@v2.0.1`，先执行 `go-licenses check ./...`，再生成 `third-party-go.csv`；同时执行 `pnpm licenses list --prod --json` 生成 `third-party-web.json`。脚本使用 `set -eu`，输出目录必须位于仓库内且不得是仓库根。

- [ ] Makefile 固定本地验证版本：

  ```make
  GORELEASER_VERSION ?= v2.17.0

  release-check:
	CGO_ENABLED=0 go run github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION) check
	corepack pnpm@10.34.5 build
	CGO_ENABLED=0 go run github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION) release --snapshot --clean
	scripts/dependency-licenses.sh dist
  ```

- [ ] 运行 snapshot，逐包执行 checksum 和 version：

  ```bash
  make release-check
  cd dist
  shasum -a 256 -c checksums.txt
  find . -type f -name '*.tar.gz' -print
  ```

- [ ] 提交发布配置：

  ```bash
  git add .goreleaser.yaml scripts/dependency-licenses.sh Makefile docs/releases/binary-layout.md
  git commit -m "feat(release): package cross platform binaries"
  ```

## Task 7：发布 GHCR 多架构镜像

**文件：**

- 新增：`.github/workflows/container.yml`

- [ ] 创建 workflow，触发 `pull_request`、`master` push 和 `v*` tag。权限固定：PR 为 `contents: read`；push/tag job 增加 `packages: write`。

- [ ] Action 版本固定为：

  ```yaml
  - uses: actions/checkout@v7
  - uses: docker/setup-qemu-action@v4
  - uses: docker/setup-buildx-action@v4
  - uses: docker/metadata-action@v6
  - uses: docker/login-action@v4
  - uses: docker/build-push-action@v7
  ```

- [ ] metadata images 使用 `ghcr.io/${{ github.repository }}`；tags 规则包括 PR、master 的 `edge`、semver 完整版本和 SHA。只有非 PR 才登录并 push。

- [ ] build 参数注入 `${{ steps.meta.outputs.version }}`、`${{ github.sha }}` 和提交时间；平台为 `linux/amd64,linux/arm64`，开启 GitHub Actions layer cache，设置 `provenance: mode=max`、`sbom: true` 和 OCI labels，使镜像 manifest 同时带来源与 SBOM attestations。

- [ ] PR 只构建 `linux/amd64` 且 `push: false`，避免每个 PR 都跑 QEMU 双架构；master/tag 构建双架构。

- [ ] push 后执行：

  ```bash
  docker buildx imagetools inspect "ghcr.io/${GITHUB_REPOSITORY,,}:${IMAGE_TAG}"
  ```

  断言 manifest 同时包含 amd64 和 arm64。

- [ ] 本地 actionlint 验证并提交：

  ```bash
  docker run --rm -v "$PWD:/repo:ro" -w /repo rhysd/actionlint:1.7.7 .github/workflows/container.yml
  git add .github/workflows/container.yml
  git commit -m "ci(release): publish multi architecture images"
  ```

## Task 8：建立 tag 发布与 SBOM 工作流

**文件：**

- 新增：`.github/workflows/release.yml`
- 新增：`docs/releases/v0.2.0-rc.1.md`
- 新增：`docs/releases/v0.2.0.md`

- [ ] 创建 workflow，仅响应 `v*` tag；permissions 固定为 `contents: write`、`actions: read`，不授予 packages 权限。需要重跑时使用 GitHub Actions 的 Re-run，不提供可能绕过 tag 校验的手工发布入口。

- [ ] 创建两份中文 release notes，固定包含“新增能力、兼容性、安全边界、已知限制、升级/安装、校验方式”六节。RC 说明明确进程内节点与 API 同权限；正式版说明从 RC 迁移无需改 manifest API Version。不得包含未关闭问题的占位描述。

- [ ] 发布前步骤固定为：checkout `fetch-depth: 0`、Setup Go v7、Setup Node v6、pnpm frozen install、安装 Playwright Chromium 及系统依赖、构建 web、`scripts/check-version.sh "$GITHUB_REF_NAME"`、确认 `docs/releases/$GITHUB_REF_NAME.md` 存在、`make verify`、`make test-sdk-e2e`。

- [ ] 使用官方 GoReleaser Action：

  ```yaml
  - uses: goreleaser/goreleaser-action@v7
    with:
      distribution: goreleaser
      version: v2.17.0
      args: release --clean --release-notes=docs/releases/${{ github.ref_name }}.md
    env:
      GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
  ```

- [ ] GoReleaser 完成后执行 `scripts/dependency-licenses.sh dist`，再生成发布目录 SBOM：

  ```yaml
  - uses: anchore/sbom-action@v0.24.0
    with:
      path: dist
      format: spdx-json
      output-file: dist/agent-studio-${{ github.ref_name }}.spdx.json
      artifact-name: agent-studio-${{ github.ref_name }}.spdx.json
      upload-artifact: true
      upload-release-assets: true
  ```

- [ ] 用 `gh release upload "$GITHUB_REF_NAME" dist/third-party-go.csv dist/third-party-web.json --clobber` 上传许可证清单。再将 checksums、两份许可证清单和 SBOM 作为 Actions artifact 上传（`actions/upload-artifact@v7`，`if-no-files-found: error`）。校验 GitHub Release 至少包含四个平台归档、checksums、两份许可证清单和 SPDX JSON。

- [ ] RC tag（如 `v0.2.0-rc.1`）必须生成 prerelease；正式 `v0.2.0` 不标 prerelease。workflow 不自动创建或移动 `latest` Git tag。

- [ ] 本地 actionlint 验证并提交：

  ```bash
  docker run --rm -v "$PWD:/repo:ro" -w /repo rhysd/actionlint:1.7.7 .github/workflows/release.yml
  git add .github/workflows/release.yml docs/releases/v0.2.0-rc.1.md docs/releases/v0.2.0.md
  git commit -m "ci(release): publish checksummed binaries and sbom"
  ```

## Task 9：建立容器级发布冒烟

**文件：**

- 新增：`scripts/compose.smoke.yaml`
- 新增：`scripts/release-smoke.sh`
- 新增：`scripts/release-smoke_test.sh`
- 修改：`Makefile`
- 修改：`.github/workflows/ci.yml`

- [ ] 先写 shell 测试，用 PATH 前置 fake `docker`/`curl`/`jq`，断言任一步失败会保留原退出码并调用作用域明确的 `docker compose -p <project> down --volumes`；不得使用全局 prune。

- [ ] `compose.smoke.yaml` 使用随机 Compose project 隔离资源，app 镜像从 `AGENT_STUDIO_IMAGE` 读取，数据库不暴露宿主端口，app 随机映射宿主 8080 并通过 `docker compose port app 8080` 获取。

- [ ] `release-smoke.sh` 使用 `set -eu` 和 trap，project 名只含小写字母、数字和连字符。依次执行：

  1. 启动 db/app 并等待健康。
  2. 检查 `/readyz`、`/workflows`、`/api/node-types`，确认 `example.echo` 存在。
  3. POST 创建 `release-smoke` 工作流并读取 ID/revision。
  4. PUT 图：Start（必填 `topic`）→ Template（`smoke: {{topic}}`）→ End。
  5. POST validate，断言 `valid=true`。
  6. POST publish，取得 workflowVersionId。
  7. POST `/api/agents/release-smoke/runs`，input 为 `{"topic":"ok"}`，读取 NDJSON 直到 `run.completed`，断言最终输出 `smoke: ok`。
  8. trap 执行 `docker compose -p "$project" down --volumes`。

- [ ] 脚本对 API JSON 使用 `jq -e`，curl 固定 `--fail-with-body --silent --show-error --max-time 30`，不把环境变量密钥打印到 `set -x` 日志。

- [ ] Makefile 增加：

  ```make
  release-smoke: image
	AGENT_STUDIO_IMAGE=agent-studio:dev scripts/release-smoke.sh
  ```

- [ ] CI 增加独立 `release-smoke` job，依赖 verify，先构建 `agent-studio:ci-${{ github.sha }}` 再运行脚本。失败时上传 compose logs，日志保留 7 天。

- [ ] 验证并提交：

  ```bash
  scripts/release-smoke_test.sh
  make release-smoke
  git add scripts Makefile .github/workflows/ci.yml
  git commit -m "test(release): smoke test the published runtime"
  ```

## Task 10：发布维护手册和 RC 验收表

**文件：**

- 新增：`docs/releases/releasing.md`
- 新增：`docs/releases/v0.2.0-rc.1-checklist.md`
- 修改：`README.md`

- [ ] 维护手册严格按顺序说明：同步 master、创建 release worktree、运行全量验证、检查 changelog、创建签名 tag、等待三个 workflow、下载并校验产物、运行镜像冒烟、宣布 release、必要时撤销 release。实际命令使用：

  ```bash
  git tag -s v0.2.0-rc.1 -m "Agent Studio v0.2.0-rc.1"
  git push origin v0.2.0-rc.1
  ```

  文档显式标注这两条是有外部影响的人工步骤，执行前必须获得发布授权。

- [ ] 撤销策略不删除或覆盖 tag：将 GitHub Release 标记为 prerelease/撤回，在 README 标记已知问题，发布修复版本；已推送镜像保持不可变，以新 tag 替代。

- [ ] RC checklist 使用可勾选字段记录：提交 SHA、CI run 链接、四平台 checksum、amd64/arm64 镜像 digest、SBOM、三名未参与开发的 Go 开发者、各自独立仓库中的节点链接、契约测试结果、每人的首个节点耗时、遇到的问题、P0/P1/P2 状态。

- [ ] RC 退出条件固定为：

  - 三名开发者均在 30 分钟内让各自独立仓库中的自定义节点通过 SDK 契约测试、出现在画布并成功执行。
  - CI、发布 snapshot、容器冒烟全部通过。
  - P0/P1/P2 缺陷为 0；P3 必须有公开 issue 和 owner。
  - SDK/manifest/CLI 兼容性文档与产物版本一致。

- [ ] README 增加三种安装入口：源码开发、`go install ...@v0.2.0`、`docker run ghcr.io/yyl1212/agent-studio:v0.2.0`；Docker 示例必须同时说明外部 PostgreSQL URL 和持久化责任。

- [ ] README 增加 LICENSE、CONTRIBUTING、SECURITY、行为准则和发布说明链接，并注明 v0.2 Capability 不是沙箱。

- [ ] 检查链接和禁用占位词后提交：

  ```bash
  rg -n 'TODO|TBD|your-org|your-email|example\.com' README.md docs/releases CONTRIBUTING.md SECURITY.md
  git diff --check
  git add README.md docs/releases
  git commit -m "docs(release): add rc process and installation guide"
  ```

  第一条预期无输出。

## Task 11：最终代码审查、回归和交付门禁

**文件：**

- 仅在发现问题时修改本计划涉及的文件

- [ ] 运行生成、格式和静态检查：

  ```bash
  CGO_ENABLED=0 go run ./cmd/agent-studio generate
  CGO_ENABLED=0 go fmt ./...
  git diff --check
  make check-generated
  docker run --rm -v "$PWD:/repo:ro" -w /repo rhysd/actionlint:1.7.7
  ```

- [ ] 运行全量回归：

  ```bash
  CGO_ENABLED=0 go test ./... -count=1
  CGO_ENABLED=0 go vet ./...
  make verify
  make test-e2e
  make test-sdk-e2e
  make security
  make release-smoke
  make release-check
  ```

- [ ] 验证生产容器不包含敏感和开发文件：

  ```bash
  docker history --no-trunc agent-studio:dev
  docker run --rm --entrypoint /app/server agent-studio:dev
  docker image inspect agent-studio:dev --format '{{json .Config.Env}}'
  ```

  history 和 Env 不得出现 API key、`.env` 内容或本地路径。第二条因缺少数据库可失败，但错误中不得包含密码以外的 DSN 敏感信息；如当前日志会打印完整 DSN，先修复再继续。

- [ ] 使用 `superpowers:requesting-code-review` 审查 GitHub 权限、供应链固定版本、tag 一致性、镜像 nonroot、静态路径穿越、API fallback、SBOM、密钥泄漏和清理脚本范围。

- [ ] 使用 `superpowers:receiving-code-review` 验证并修复有效意见，重新执行以上全部命令。P0、P1、P2 清零。

- [ ] 使用 `superpowers:verification-before-completion` 检查最新一次命令输出，而不是沿用此前结果。

- [ ] 检查提交边界和工作区：

  ```bash
  git log --oneline master..HEAD
  git diff --stat master...HEAD
  git status --short
  ```

  预期：工作区干净，未创建或推送任何 release tag。

## 完成标准

- PR CI、依赖安全检查、SDK/画布 E2E、容器冒烟均可重复运行。
- 单个 nonroot 镜像能连接 Docker PostgreSQL，并从 8080 同源提供 API 与聚焦式前端。
- GoReleaser snapshot 包含四个平台归档、前端静态目录和 SHA-256 校验和。
- tag 工作流能够发布对应的二进制、双架构 GHCR 镜像和 SPDX JSON SBOM。
- RC 表具备三名外部开发者实测入口，且退出条件可客观判断。
- 计划执行结束时没有擅自创建 tag、GitHub Release 或推送镜像。
