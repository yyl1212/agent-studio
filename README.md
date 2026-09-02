# Agent Studio

Agent Studio 是一套轻量化、本地优先的 Agent 开发系统：用低代码画布组合工作流，从开始节点参数自动生成聚焦式 Agent 页面，并通过后端节点注册表快速扩展能力。首版采用 Go 单体 API、React 画布和 Docker PostgreSQL，默认 Mock 模型即可跑通完整闭环。

```mermaid
flowchart LR
  B["React 画布与 Schema 表单"] -->|"JSON API / NDJSON"| A["Go chi API"]
  A --> S["工作流服务"]
  S --> C["DAG 编译器"]
  A -->|"持久化排队"| P[("PostgreSQL 18")]
  W["独立 Go Worker"] -->|"租约领取 / 续租"| P
  W --> E["并发执行引擎"]
  C --> R["节点注册表"]
  E --> R
  R --> M["Mock / OpenAI-compatible"]
```

## 开发者预览版本

当前开发者预览目标为 `v0.5.0-rc.1`，面向源码使用者、工作流作者、节点包开发者和运维人员在隔离环境验证。它集中交付 v0.5-A 可观测性、v0.5-B 画布体验、v0.5-C 备份恢复与 v0.5-D 持久运行；仍是非生产稳定版，也不提供 v1 兼容承诺。标签和附件只有在标签工作流成功后才能使用，执行安装或下载前应核对远端状态。完整边界、制品核验和限制见 [v0.5.0-rc.1 RC 说明](docs/releases/v0.5.0-rc.1.md)。

标签工作流成功后的源码安装命令：

```bash
CGO_ENABLED=0 go install github.com/yyl1212/agent-studio/cmd/agent-studio@v0.5.0-rc.1
agent-studio version
```

### 预编译 CLI 附件

`v0.5.0-rc.1` 仅在标签工作流全部成功后提供 Linux/macOS 的 amd64、arm64 CLI 归档、SHA-256 校验和与逐归档 SPDX JSON SBOM。下载示例：

```bash
VERSION=v0.5.0-rc.1
OS=darwin
ARCH=arm64
ARCHIVE="agent-studio_${VERSION}_${OS}_${ARCH}.tar.gz"
BASE_URL="https://github.com/yyl1212/agent-studio/releases/download/${VERSION}"

curl -fLO "${BASE_URL}/${ARCHIVE}"
curl -fLO "${BASE_URL}/checksums.txt"
grep "  ${ARCHIVE}$" checksums.txt | shasum -a 256 -c -
tar -xzf "${ARCHIVE}"
./agent-studio version
```

Linux 使用 `sha256sum` 校验：

```bash
grep "  ${ARCHIVE}$" checksums.txt | sha256sum -c -
```

支持的预编译目标如下：

| 操作系统 | 架构 |
| --- | --- |
| Linux | amd64 |
| Linux | arm64 |
| macOS | amd64 |
| macOS | arm64 |

macOS 归档目前未签名，也未经过 Apple Developer ID 签名或公证。`checksums.txt` 中的 SHA-256 只能确认下载内容与发布附件一致，不能替代发布者签名验证。若目标 Release 没有预编译附件，请继续使用 `go install`；这也是当前更稳妥的安装方式。

## 环境要求

- Go 1.26（项目 toolchain 为 1.26.5）
- Node.js 24
- Corepack 与 pnpm 10.34.5
- Docker Desktop / Docker Compose

首次安装：

```bash
corepack enable
corepack pnpm@10.34.5 install
cp .env.example .env
```

`RUN_PAYLOAD_ENCRYPTION_KEY` 是 API 与 Worker 的强制共享配置，必须是 Base64 编码的 32 字节随机值。`.env.example` 里的值只用于本机演示；首次启动前应生成自己的临时开发密钥，并把输出填入未提交的 `.env`：

```bash
openssl rand -base64 32
```

不要把真实环境的密钥提交到仓库、备份或日志。已存在活动运行时不得随意轮换；密钥丢失会使对应私有载荷无法解密。

## 本地启动

推荐一次启动 PostgreSQL、API 与独立 Worker：

```bash
export RUN_PAYLOAD_ENCRYPTION_KEY="$(openssl rand -base64 32)"
make dev-stack
```

API 和 Worker 使用同一镜像、数据库与密钥，但以两个独立进程运行。分别查看日志：

```bash
docker compose logs -f api
docker compose logs -f worker
```

源码调试时也可以分终端启动。先确保 `.env` 已配置同一个 `RUN_PAYLOAD_ENCRYPTION_KEY`，再运行：

启动 PostgreSQL：

```bash
make db-up
```

可观测性默认关闭。需要在本机查看 Metrics 与 Traces 时，可运行 `make observability-up`，然后访问 Prometheus `http://127.0.0.1:9090` 和 Jaeger `http://127.0.0.1:16686`。配置、验证与故障处理见[可观测性运行手册](docs/observability.md)；真实闭环可运行 `make observability-verify`。

终端一启动 API（启动时自动执行幂等 migration）：

```bash
make dev-api
```

`make dev-api` 会自动加载根目录 `.env`；修改模型或网络策略配置后重启 API 即可生效。

终端二启动 Worker：

```bash
make dev-worker
```

终端三启动 Web：

```bash
make dev-web
```

打开 `http://localhost:5173/workflows`。默认 `MODEL_PROVIDER=mock`，无需密钥：创建工作流，配置开始节点，添加“提示词模板”和“LLM”，连接到结束节点后即可测试、发布并运行 Agent。

发布前可在 Studio 顶部打开“Agent 页面设置”，配置标题、说明、强调色、按钮文案和结果展示方式。发布后的聚焦式单栏页面会把运行 ID 写入 URL，支持刷新恢复、取消和再次运行；完整使用方式与容量、安全边界见 [Agent 发布页与运行恢复](docs/agent-page.md)。

Studio 使用画布旁的单一上下文工作台。节点输入先保存在本地草稿中，端口解析完成后需显式点击“应用配置”；切换节点、测试、发布或导出时会保护未应用草稿。完整操作与响应式边界见 [Studio 聚焦工作台使用指南](docs/frontend-workbench.md)。

Studio 顶部“版本历史”已支持发布版本时间线、任意两个快照的五组语义比较、受保护的“恢复为草稿”和单级撤销。草稿回滚不会改变线上 Agent 或历史运行绑定；操作步骤、并发语义和安全边界见 [工作流版本治理](docs/workflow-versions.md)。

顶部“节点包”导航提供只读的本地索引目录；使用与安全边界见[官方节点包索引](docs/node-index.md)。

顶部“运行”导航提供全局筛选、3 秒智能刷新、协作取消和安全完整重试；操作步骤、幂等语义与秘密边界见[运行管理与恢复](docs/run-management.md)。

运行由 Worker 从 PostgreSQL 队列领取。API 或浏览器断开不会中止后台执行；Worker 异常退出后，纯节点可按租约自动接管，只读或有副作用的不确定节点会暂停为“等待人工恢复”。管理员可在“运行”详情的恢复入口逐个确认重试或终止运行，公开 Agent 页面不会暴露这些管理操作。Worker 内的队列采样会记录队列深度和最老排队时间；它用于观察当前实例，不代替容量规划。

RC 容量基线固定为 1 API、1 Worker、Worker concurrency 4、500 Mock runs 和 10 分钟命令上限。该演练不是 SLA；它只检查本地隔离 Compose 环境中运行、租约和队列是否收敛。只在隔离的非生产环境运行；先确认 Docker、Compose、curl、jq、Ruby 和 Go 可用，再导出专用测试密钥（不要使用真实或固定密钥）：

```bash
export RUN_PAYLOAD_ENCRYPTION_KEY="$(openssl rand -base64 32)"
make test-rc-capacity-e2e
```

升级到 migration 7 前必须在维护窗口停止旧 API、旧 Worker 和写库工具，使用同版本 API/Worker、同一数据库和相同 `RUN_PAYLOAD_ENCRYPTION_KEY` 重启。回滚仅恢复升级前备份和旧制品，不能逆迁移或混跑；完整步骤见 [v0.5-D 升级与回滚](docs/upgrades/v0.5-d.md)。

健康检查：`GET http://localhost:8080/healthz`；就绪检查还会验证 PostgreSQL 与最新 migration：`GET http://localhost:8080/readyz`。

## 实例备份与恢复

实例备份覆盖工作流、版本和完整运行记录，归档为敏感明文文件；创建文件权限为 `0600`，但不内置加密。请将归档放在加密磁盘或使用外部加密工具，并在恢复前停止 API。完整安全边界、空实例检查、正式恢复清单和故障处理见[实例备份与恢复](docs/backup-restore.md)。

仅展示日常创建、离线检查和目标 dry-run；所有涉及数据库的 CLI 命令从 `DATABASE_URL` 环境变量读取连接：

```bash
DATABASE_URL='postgres://user:password@host:5432/agent_studio?sslmode=disable' \
  agent-studio backup create --output ./backups/studio-20260829.asbak
agent-studio backup inspect ./backups/studio-20260829.asbak
DATABASE_URL='postgres://user:password@target-host:5432/agent_studio?sslmode=disable' \
  agent-studio backup restore --dry-run ./backups/studio-20260829.asbak
```

## 调试回放与局部重跑

完成一次测试、发布版本运行或局部调试后，在工作流顶部进入“运行记录”，点击目标运行的“调试回放”。回放页不会再次执行节点：画布保持只读，右侧时间线按持久化事件序号展示运行与节点状态；点击时间线事件或画布节点，可以检查该节点当时的输入、输出、活动端口、耗时、脱敏路径和公开错误。局部重跑产生的新运行会显示来源运行链接，便于沿调试链返回原始现场。

需要从某个节点继续验证时，在节点详情点击“从此节点重新运行”。服务端会重新计算活动子图和历史冻结边，并在执行前显示安全等级：

- `pure`：纯计算，不访问外部服务。
- `read_only`：只读调用，可能产生模型费用，但不写入外部系统。
- `side_effect`：可能写入或调用外部系统，必须显式确认后才能运行。

入口输入在浏览器中必须是 JSON 对象。被脱敏的秘密入口不会从历史记录恢复，需要重新填写；如果必要的历史冻结边包含秘密值，系统会拒绝局部重跑，而不是用空值或掩码继续执行。取消只会中止尚未完成的请求，不能撤销已经发生的模型费用、Webhook 或其他外部副作用。

早期版本产生、缺少完整事件快照的 legacy 运行会降级为只读摘要：仍可查看画布和节点最终状态，但不能精确回放或局部重跑。升级时遗留的活动运行会进入人工恢复，不会猜测或静默重放历史副作用；处理方式见 [v0.5-D 升级与回滚](docs/upgrades/v0.5-d.md)。

## Go 节点 SDK

第三方节点只需导入：

```go
import "github.com/yyl1212/agent-studio/sdk/go/agentnode"
```

公开 SDK 当前为 v0.5 / `agent-studio.dev/v1alpha1`。完整生命周期与可编译示例见 [Go 节点 SDK API](docs/sdk/api.md)，版本承诺见 [兼容性策略](docs/sdk/compatibility.md)，开发入口见 [节点扩展指南](docs/node-development.md)。

节点仍在 API 进程内运行。Capability 只用于展示和审计，不提供沙箱隔离；生产环境只应加载可信扩展，并用容器、系统权限和网络策略建立安全边界。

### 创建扩展节点

仓库内置低代码节点开发闭环：环境检查、脚手架、契约测试、确定性注册代码生成，以及无需前端专用组件的画布验证。

```bash
CGO_ENABLED=0 go run ./cmd/agent-studio doctor
CGO_ENABLED=0 go run ./cmd/agent-studio node init my-echo
CGO_ENABLED=0 go run ./cmd/agent-studio node test ./extensions/my-echo
CGO_ENABLED=0 go run ./cmd/agent-studio generate
```

查看当前应用构建、SDK/API 契约和提交信息：

```bash
CGO_ENABLED=0 go run ./cmd/agent-studio version
```

源码开发构建显示 `0.5.0-dev`；从版本标签安装时显示对应 tag。

完整步骤见 [30 分钟创建第一个扩展节点](docs/sdk/quickstart.md)，错误处理见 [节点开发排错](docs/sdk/debugging.md)。扩展与 API 同进程运行，只应加载可信源码。

### 官方扩展节点

根 manifest 默认登记三个可直接在通用画布使用的扩展：

- Echo：最小文本节点示例，为输入添加前缀。
- Retriever：基于本地 Jaccard 相似度的确定性演示，不使用向量数据库或 Embedding，也不作为生产知识库。
- Webhook：固定向运维指定的基地址发送 JSON `POST`；工作流只能保存受约束的相对 `path`。

使用 Webhook 前，在 `.env` 配置 API 进程环境：

```dotenv
AGENT_STUDIO_WEBHOOK_URL=https://hooks.example.com/agent-studio
AGENT_STUDIO_WEBHOOK_TOKEN=
```

Token 可选，只能由运维环境提供，不得写入工作流配置。修改这两个变量后需重启 API；URL 留空不影响启动，只会在执行 Webhook 时返回安全的缺少配置错误。

## 模型配置

默认配置：

```dotenv
MODEL_PROVIDER=mock
```

切换到 OpenAI-compatible Chat Completions 服务：

```dotenv
MODEL_PROVIDER=openai-compatible
OPENAI_BASE_URL=https://your-provider.example/v1
OPENAI_API_KEY=
OPENAI_DEFAULT_MODEL=your-model
```

密钥只通过环境变量传入，不进入工作流 JSON、API 响应或日志。

### LLM v2 结构化输出

节点库同时保留两个版本：`LLM`（`llm@1`）继续生成文本，`LLM · 结构化输出`（`llm@2`）可选择文本或严格结构化模式。最小画布如下：

```text
开始 → 提示词模板 → LLM · 结构化输出 → 结束
```

在 v2 节点把“输出模式”设为 `structured`，再用低代码字段列表声明 1–32 个输出字段。字段类型支持字符串、数字、整数、布尔值和字符串数组；取消“必填”的字段会按 nullable 字段处理。节点会提供完整 `json` 端口、每个已声明字段的动态端口，以及 `usage` 端口。修改或删除字段后，应重新连接画布上已经失效的动态端口。

默认 Mock Provider 可直接演示：字符串字段返回 `Mock 回复：<提示词>`，数字和整数返回 `0`，布尔值返回 `false`，字符串数组返回空数组。接入 OpenAI-compatible 服务时，服务必须原生支持 Chat Completions 的 strict JSON Schema `response_format`；Agent Studio 不会自动降级为提示词约束，也不会隐藏重试。模型拒绝结构化输出、上游失败或本地二次校验失败都会返回稳定的公开错误，原始上游响应与密钥不会进入工作流输出、API 错误或日志。

## HTTP 节点密钥与网络策略

HTTP 节点的敏感 Header 必须选择环境变量来源并填写变量名，例如 `UPSTREAM_API_KEY`；禁止把 Authorization/Cookie 直接写入配置。默认拒绝 loopback、私网、link-local 和解析后落入私网的地址，以降低 SSRF 风险。本地可信开发环境确需访问私网时才设置：

```dotenv
HTTP_NODE_ALLOW_PRIVATE=true
```

## 验证

不启动 Docker 的提交前快速检查：

```bash
make verify-quick
```

该命令运行 Go 生成物检查、单元测试、vet、发布脚本测试，以及前端
OpenAPI 生成物检查、lint、类型检查、组件测试和生产构建。

需要 Docker PostgreSQL 的完整回归：

```bash
make verify
make test-e2e
make test-sdk-e2e
```

`make verify` 会检查节点生成代码，启动数据库，运行全部 Go 测试、`go vet`、OpenAPI 类型再生成差异检查、前端类型检查、组件测试和生产构建。`make test-e2e` 使用真实浏览器验证创建、配置、连线、测试、发布、版本绑定和 Agent 运行；`make test-sdk-e2e` 额外验证临时 Module 的 CLI 黄金路径和通用画布 Echo 节点。

备份交付门禁也包含在 `make verify-go-quick`：它会检查备份文档的安全契约和仓库黄金备份 fixture。需要 Docker PostgreSQL 的完整备份恢复路径时运行：

```bash
make test-backup-e2e
```

单独调试：

```bash
cd apps/api && CGO_ENABLED=0 go test ./... -count=1
corepack pnpm@10.34.5 test
corepack pnpm@10.34.5 build
```

## 项目结构

- `apps/api`：Go API、DAG 编译/执行、节点、PostgreSQL store 和 migration；`run_events` 保存可分页回放的脱敏事件快照。
- `apps/web`：React 工作流列表、画布 Studio、通用 Schema 表单、Agent 页与运行记录。
- `sdk/go/agentnode`：公开节点协议；`sdk/go/agenttest`：第三方节点契约测试工具。
- `contracts/openapi.yaml`：前后端唯一 HTTP 契约来源。
- [工作流模板导入导出](docs/workflow-templates.md)：本地 JSON 模板的使用方法与安全边界。
- [Studio 聚焦工作台](docs/frontend-workbench.md)：节点草稿、动态端口、测试调试、快捷键和响应式行为。
- [节点包开发与兼容检查](docs/node-packages.md)：节点包清单、离线检查、手工安装和模板依赖提示。
- [官方节点包索引](docs/node-index.md)：离线搜索、显式刷新、缓存恢复和人工源码审核流程。
- [工作流版本治理](docs/workflow-versions.md)：Studio 版本历史入口、结构化比较、安全回滚、单级撤销与管理 API。
- `docs/node-development.md`：无需修改前端的节点扩展示例。
- `docs/sdk/quickstart.md`：仓库节点创建、测试、生成和画布验证的黄金路径。

## 首版边界

- 单租户、本地部署，不包含登录、RBAC、团队协作与审计后台。
- 工作流是无环图，不支持循环、人工审批、定时触发和分布式队列。
- 运行通过 PostgreSQL 队列和独立 Worker 执行；当前不引入 Redis/Kafka，也不支持跨区域调度。
- 浏览器或 API 断线不影响后台执行；Worker 故障只会自动接管可证明安全的工作，副作用不确定时必须人工恢复。
- 模型接入为 Mock 或 OpenAI-compatible Chat Completions，不含供应商专属高级能力。
- Code 节点使用受步数、时间和输出大小约束的 Starlark，不执行任意系统命令。
- 本地 RAG 闭环仍不在本阶段范围；Retriever 仅为确定性演示节点，不是生产知识库。

## 社区与安全

- 贡献流程：[CONTRIBUTING.md](CONTRIBUTING.md)
- 行为准则：[CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
- 安全报告：[SECURITY.md](SECURITY.md)
- 许可证：[Apache-2.0](LICENSE)
