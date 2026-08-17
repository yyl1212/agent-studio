# 轻量化 Agent Studio Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 从空仓库交付可本地运行的单用户 Agent Studio，完整打通低代码 DAG 编排、开始参数驱动的 Agent 页面、节点扩展、测试运行、版本发布和运行记录。

**Architecture:** React Web 负责画布、Schema 表单与发布页；Go 模块化单体负责节点注册、图编译、DAG 调度、版本与运行服务；Docker PostgreSQL 保存草稿、不可变版本和运行记录。前后端通过 OpenAPI JSON 接口与 NDJSON 运行事件通信。

**Tech Stack:** Go 1.26、chi v5、pgx v5、Starlark Go、JSON Schema v6、PostgreSQL 18、Node.js 24 LTS、pnpm 10、React 19、Vite 8、TypeScript 7、React Flow 12、Vitest 4、Playwright。

## Global Constraints

- 以本地 `master` 提交 `655e5d2` 为基线，在现有 `codex/agent-studio` 分支实施；用户已明确授权无需远程仓库。
- Go 模块必须使用 `go 1.26.0` 和实施时验证通过的 Go 1.26 `toolchain` 补丁版本。
- 所有 Go 调试、构建和测试命令必须显式设置 `CGO_ENABLED=0`。
- 根 `package.json` 必须锁定 `packageManager: pnpm@10.34.5`；Node.js 要求 `>=24 <25`。
- PostgreSQL 使用 Docker 官方 `postgres:18` 镜像，数据卷挂载到 `/var/lib/postgresql`。
- 用户可见文案与项目文档使用中文。
- 不引入登录、RBAC、文件上传、RAG、循环、子工作流、外部插件进程、消息队列或自动重试。
- API Key 和敏感 HTTP Header 的值不得进入工作流 JSON、API 响应、日志或运行记录。
- 不执行宿主机 Shell；代码节点仅执行受限 Starlark，且被视为单用户可信代码能力，不宣称进程级隔离。
- 工作流必须恰有一个开始节点和一个结束节点；结束节点使用 `single-active` 输入汇聚语义。
- 每个任务遵循红—绿—重构，任务结束前运行该任务的完整测试并独立提交。

---

## 文件结构与职责

```text
.
├── .env.example                       # 本地配置样例，不含真实密钥
├── Makefile                           # db/dev/test/verify 统一入口
├── README.md                          # 中文启动和演示说明
├── compose.yaml                       # PostgreSQL 18
├── contracts/openapi.yaml             # HTTP 与 NDJSON 契约
├── docs/node-development.md           # 新增节点指南
├── apps/api/
│   ├── cmd/server/main.go             # 依赖组装和服务生命周期
│   ├── go.mod                         # Go 版本与依赖锁定
│   ├── internal/config/               # 环境变量解析与约束
│   ├── internal/domain/               # 图、节点、版本、运行领域类型
│   ├── internal/nodes/                # Registry、Schema 校验、内置节点
│   ├── internal/modelprovider/        # Mock 与 OpenAI-compatible
│   ├── internal/engine/               # 图编译与 DAG 调度
│   ├── internal/workflow/             # 草稿、发布、运行用例
│   ├── internal/httpapi/              # chi Handler、错误与 NDJSON
│   ├── internal/store/postgres/       # pgx 仓储与迁移执行器
│   └── migrations/                    # 嵌入式 SQL migration
└── apps/web/
    ├── package.json                   # Web 依赖和脚本
    ├── vite.config.ts                 # Vite/Vitest 配置
    ├── playwright.config.ts           # E2E 配置
    ├── e2e/                           # 核心闭环测试
    └── src/
        ├── app/                       # 路由、壳层、全局样式
        ├── components/schema-form/    # 通用 JSON Schema 表单
        ├── features/workflows/        # 工作流列表
        ├── features/studio/           # React Flow 编辑器和保存队列
        ├── features/agent/            # 发布后的动态 Agent 页面
        ├── features/runs/             # 运行状态与记录
        └── lib/api/                   # 生成类型、fetch 与 NDJSON 解析
```

### Task 1: 仓库骨架、工具链与运行配置

**Files:**
- Create: `package.json`
- Create: `pnpm-workspace.yaml`
- Create: `.env.example`
- Create: `compose.yaml`
- Create: `Makefile`
- Create: `apps/api/go.mod`
- Create: `apps/api/internal/config/config.go`
- Test: `apps/api/internal/config/config_test.go`
- Create: `apps/web/package.json`
- Create: `apps/web/index.html`
- Create: `apps/web/tsconfig.json`
- Create: `apps/web/vite.config.ts`
- Create: `apps/web/src/main.tsx`
- Create: `apps/web/src/app/App.tsx`
- Create: `apps/web/src/app/App.test.tsx`
- Create: `apps/web/src/app/styles.css`
- Create: `apps/web/src/test/setup.ts`

**Interfaces:**
- Produces: `config.Load() (config.Config, error)`。
- Produces: root scripts `dev:web`、`lint`、`test`、`build`、`test:e2e`。
- Produces: Docker service name `db` and database URL `postgres://agent:agent@localhost:5432/agent_studio?sslmode=disable`。

- [ ] **Step 1: 创建最小 `go.mod` 并写 Go 配置失败测试**

先创建只含模块名、`go 1.26.0` 和 `toolchain go1.26.5` 的 `apps/api/go.mod`，不创建 `config.go`，再写测试：

```go
func TestLoadUsesSafeDefaults(t *testing.T) {
    t.Setenv("DATABASE_URL", "")
    t.Setenv("MODEL_PROVIDER", "")
    cfg, err := Load()
    if err != nil { t.Fatal(err) }
    if cfg.HTTPAddr != ":8080" { t.Fatalf("HTTPAddr=%q", cfg.HTTPAddr) }
    if cfg.ModelProvider != "mock" { t.Fatalf("provider=%q", cfg.ModelProvider) }
    if cfg.MaxParallelNodes != 4 { t.Fatalf("parallel=%d", cfg.MaxParallelNodes) }
}

func TestLoadRejectsOpenAIWithoutBaseURL(t *testing.T) {
    t.Setenv("MODEL_PROVIDER", "openai-compatible")
    t.Setenv("OPENAI_BASE_URL", "")
    if _, err := Load(); err == nil {
        t.Fatal("expected OPENAI_BASE_URL validation error")
    }
}
```

- [ ] **Step 2: 运行配置测试并确认失败**

Run: `cd apps/api && GOTOOLCHAIN=auto CGO_ENABLED=0 go test ./internal/config -run TestLoad -v`
Expected: FAIL，原因是 `Load` 尚不存在。

- [ ] **Step 3: 实现最小配置加载并固定 Go 工具链**

`go.mod` 使用模块名 `agentstudio.local/api`，固定：

```go
go 1.26.0

toolchain go1.26.5
```

`Config` 必须包含：

```go
type Config struct {
    HTTPAddr                   string
    DatabaseURL               string
    WebOrigin                 string
    ModelProvider             string
    OpenAIBaseURL             string
    OpenAIAPIKey              string
    OpenAIDefaultModel        string
    HTTPNodeAllowPrivate      bool
    MaxParallelNodes          int
    WorkflowTimeout           time.Duration
}
```

默认值为 `:8080`、上述本地数据库 URL、`http://localhost:5173`、`mock`、并发 4、工作流超时 120 秒。`openai-compatible` 必须要求 `OPENAI_BASE_URL`，API Key 允许为空以兼容本地模型服务。

- [ ] **Step 4: 运行 Go 配置测试并确认通过**

Run: `cd apps/api && GOTOOLCHAIN=auto CGO_ENABLED=0 go test ./internal/config -v`
Expected: PASS。

- [ ] **Step 5: 创建最小 Web 测试清单并写壳层失败测试**

先创建 root/Web `package.json`、workspace、tsconfig、Vitest setup 并安装 React、Vitest、Testing Library 和 jsdom，使测试 runner 可启动；暂不创建 `App.tsx`，再写：

```tsx
it('显示中文产品名称', () => {
  render(<App />)
  expect(screen.getByRole('heading', { name: 'Agent Studio' })).toBeInTheDocument()
  expect(screen.getByText('工作流')).toBeInTheDocument()
})
```

- [ ] **Step 6: 运行 Web 测试并确认失败**

Run: `corepack pnpm@10.34.5 --dir apps/web test -- --run App.test.tsx`
Expected: FAIL，原因是前端包和 `App` 尚未建立。

- [ ] **Step 7: 补齐 Vite/React 应用骨架**

根 `package.json`：

```json
{
  "name": "agent-studio",
  "private": true,
  "packageManager": "pnpm@10.34.5",
  "engines": { "node": ">=24 <25" },
  "scripts": {
    "dev:web": "pnpm --filter @agent-studio/web dev",
    "lint": "pnpm --filter @agent-studio/web lint",
    "test": "pnpm --filter @agent-studio/web test --run",
    "build": "pnpm --filter @agent-studio/web build",
    "test:e2e": "pnpm --filter @agent-studio/web test:e2e"
  }
}
```

Web 固定生产依赖 `react@19.2.8`、`react-dom@19.2.8`、`@xyflow/react@12.11.2`；开发依赖固定 `typescript@7.0.2`、`vite@8.2.0`、`vitest@4.1.10`，并安装与 React 19 兼容的 Testing Library、jsdom 和类型包，最终精确版本进入 `pnpm-lock.yaml`。`App` 仅渲染产品标题和“工作流”导航，CSS 使用系统字体与自制样式，不引入图片素材。

Web scripts 固定为 `dev: vite`、`lint: tsc --noEmit`、`test: vitest`、`build: tsc --noEmit && vite build`。测试 setup 必须提供 `@testing-library/jest-dom`、`ResizeObserver` 和 `matchMedia` 的确定性 mock，保证 React Flow 在 jsdom 中可渲染。

- [ ] **Step 8: 创建 PostgreSQL Compose 与 Makefile 基础目标**

`compose.yaml` 使用 `postgres:18`、健康检查 `pg_isready -U agent -d agent_studio`、命名卷 `agent_pg_data:/var/lib/postgresql`，只暴露 `5432`。Makefile 的 `db-up` 使用 `docker compose up -d --wait db`，另提供 `db-down`、`dev-api`、`dev-web`，Go 目标均前置 `CGO_ENABLED=0`。

- [ ] **Step 9: 运行基础验证**

Run: `corepack pnpm@10.34.5 install`
Run: `cd apps/api && GOTOOLCHAIN=auto CGO_ENABLED=0 go test ./...`
Run: `corepack pnpm@10.34.5 test`
Run: `corepack pnpm@10.34.5 build`
Expected: 全部 PASS，Web 生成 `apps/web/dist`。

- [ ] **Step 10: 提交**

```bash
git add package.json pnpm-workspace.yaml pnpm-lock.yaml .env.example compose.yaml Makefile apps/api apps/web
git commit -m "chore: scaffold agent studio workspace"
```

### Task 2: 领域模型、JSON Schema 校验与节点 Registry

**Files:**
- Create: `apps/api/internal/domain/graph.go`
- Create: `apps/api/internal/domain/node.go`
- Create: `apps/api/internal/domain/workflow.go`
- Create: `apps/api/internal/domain/run.go`
- Create: `apps/api/internal/domain/errors.go`
- Create: `apps/api/internal/nodes/registry.go`
- Create: `apps/api/internal/nodes/schema.go`
- Test: `apps/api/internal/nodes/registry_test.go`

**Interfaces:**
- Produces: `domain.Graph`、`domain.Node`、`domain.Edge`、`domain.PortDefinition`、`domain.NodeRequest`、`domain.NodeResult`。
- Produces: `nodes.NodeType` and `nodes.Registry`。
- Consumes later: compiler, engine, HTTP API and all built-in nodes use these exact types.

- [ ] **Step 1: 写 Registry 失败测试**

```go
func TestRegistryRegistersAndSortsDefinitions(t *testing.T) {
    r := NewRegistry()
    if err := r.Register(fakeNode{kind: "zeta"}); err != nil { t.Fatal(err) }
    if err := r.Register(fakeNode{kind: "alpha"}); err != nil { t.Fatal(err) }
    defs := r.Definitions()
    if got := []string{defs[0].Type, defs[1].Type}; !reflect.DeepEqual(got, []string{"alpha", "zeta"}) {
        t.Fatalf("definitions=%v", got)
    }
}

func TestRegistryRejectsDuplicateAndInvalidSchema(t *testing.T) {
    r := NewRegistry()
    if err := r.Register(fakeNode{kind: "echo"}); err != nil { t.Fatal(err) }
    if err := r.Register(fakeNode{kind: "echo"}); !errors.Is(err, ErrDuplicateNodeType) {
        t.Fatalf("duplicate error=%v", err)
    }
    if err := r.Register(fakeNode{kind: "bad", schema: json.RawMessage(`{"type":`) }); err == nil {
        t.Fatal("expected invalid schema error")
    }
}
```

- [ ] **Step 2: 运行 Registry 测试并确认失败**

Run: `cd apps/api && CGO_ENABLED=0 go test ./internal/nodes -run TestRegistry -v`
Expected: FAIL，领域类型和 Registry 不存在。

- [ ] **Step 3: 定义稳定领域类型**

```go
type DataType string
const (
    TypeString DataType = "string"
    TypeNumber DataType = "number"
    TypeBoolean DataType = "boolean"
    TypeJSON DataType = "json"
    TypeAny DataType = "any"
)

type PortCardinality string
const (
    CardinalityOne PortCardinality = "one"
    CardinalitySingleActive PortCardinality = "single-active"
)

type PortDefinition struct {
    Key string `json:"key"`
    Title string `json:"title"`
    Type DataType `json:"type"`
    Required bool `json:"required"`
    Cardinality PortCardinality `json:"cardinality"`
}

type NodeRequest struct {
    Inputs map[string][]any
    RunInput map[string]any
    Config json.RawMessage
}

type NodeResult struct {
    Outputs map[string]any
    ActivePorts []string
}

type ResolvedPorts struct {
    Inputs []PortDefinition `json:"inputs"`
    Outputs []PortDefinition `json:"outputs"`
}
```

`Graph` 使用 `SchemaVersion int`、`Nodes []Node`、`Edges []Edge`；`Node` 精确包含 `ID`、`Type`、`TypeVersion`、`Position`、`Config`；`Edge` 精确包含 `ID`、`Source`、`SourcePort`、`Target`、`TargetPort`。

同时定义后续仓储与服务共用类型：

```go
type NodeDefinition struct {
    Type string `json:"type"`
    Version string `json:"version"`
    Title string `json:"title"`
    Description string `json:"description"`
    Category string `json:"category"`
    ConfigSchema json.RawMessage `json:"configSchema"`
    Inputs []PortDefinition `json:"inputs"`
    Outputs []PortDefinition `json:"outputs"`
}

type Workflow struct {
    ID string
    Name string
    Slug string
    Description string
    DraftGraph json.RawMessage
    DraftRevision int64
    PublishedVersionID *string
    PublishedVersion *int
    CreatedAt time.Time
    UpdatedAt time.Time
}

type WorkflowVersion struct {
    ID string
    WorkflowID string
    Version int
    Graph json.RawMessage
    InputSchema json.RawMessage
    CreatedAt time.Time
}
```

`run.go` 定义 `RunMode`、`RunStatus`、`NodeStatus`、`Run` 和 `NodeRun`；`Run` 包含 version ID、draft revision、graph snapshot、input/output/error 与起止时间。`errors.go` 定义 `PublicError` 和带 `Code/Message/NodeID/EdgeID/Path` 的 `ValidationIssue`。

- [ ] **Step 4: 实现 NodeType、Schema 编译和 Registry**

```go
type NodeType interface {
    Definition() domain.NodeDefinition
    Resolve(config json.RawMessage) (domain.ResolvedPorts, error)
    Execute(ctx context.Context, request domain.NodeRequest) (domain.NodeResult, error)
}

type Registry struct {
    entries map[string]registeredNode
}
```

Registry key 固定为 `type + "@" + version`。注册时使用 `jsonschema.UnmarshalJSON`、`Compiler.AddResource`、`Compiler.Compile` 编译配置 Schema，并缓存 `*jsonschema.Schema`。`ValidateConfig` 将 JSON 解码为 `any` 后调用 `Schema.Validate`。依赖固定 `github.com/santhosh-tekuri/jsonschema/v6@v6.0.2`。

- [ ] **Step 5: 运行 Registry 测试并补充配置校验断言**

Run: `cd apps/api && CGO_ENABLED=0 go test ./internal/nodes -v`
Expected: PASS；额外断言未知节点返回 `ErrNodeTypeNotFound`，不允许 panic。

- [ ] **Step 6: 提交**

```bash
git add apps/api/go.mod apps/api/go.sum apps/api/internal/domain apps/api/internal/nodes
git commit -m "feat: add node registry and workflow domain"
```

### Task 3: 开始、模板、条件和结束节点

**Files:**
- Create: `apps/api/internal/nodes/builtin/helpers.go`
- Create: `apps/api/internal/nodes/builtin/start.go`
- Test: `apps/api/internal/nodes/builtin/start_test.go`
- Create: `apps/api/internal/nodes/builtin/template.go`
- Test: `apps/api/internal/nodes/builtin/template_test.go`
- Create: `apps/api/internal/nodes/builtin/condition.go`
- Test: `apps/api/internal/nodes/builtin/condition_test.go`
- Create: `apps/api/internal/nodes/builtin/end.go`
- Test: `apps/api/internal/nodes/builtin/end_test.go`
- Create: `apps/api/internal/nodes/builtin/register.go`

**Interfaces:**
- Produces: `builtin.RegisterCore(registry *nodes.Registry) error`。
- Produces: Start output ports derived from field keys; Template input ports derived from strict `{{name}}`; Condition activates exactly one control port; End validates single-active result count.

- [ ] **Step 1: 写动态端口与执行失败测试**

```go
func TestStartResolvesFieldsAndEmitsRunInput(t *testing.T) {
    cfg := json.RawMessage(`{"fields":[{"key":"topic","label":"主题","type":"text","required":true}]}`)
    n := NewStart()
    ports, err := n.Resolve(cfg)
    if err != nil { t.Fatal(err) }
    if ports.Outputs[0].Key != "topic" || ports.Outputs[0].Type != domain.TypeString { t.Fatalf("ports=%+v", ports) }
    result, err := n.Execute(context.Background(), domain.NodeRequest{Config: cfg, RunInput: map[string]any{"topic":"Agent"}})
    if err != nil { t.Fatal(err) }
    if result.Outputs["topic"] != "Agent" { t.Fatalf("result=%v", result.Outputs) }
}

func TestTemplateRejectsMissingVariable(t *testing.T) {
    n := NewTemplate()
    cfg := json.RawMessage(`{"template":"主题：{{topic}}"}`)
    _, err := n.Execute(context.Background(), domain.NodeRequest{Config: cfg, Inputs: map[string][]any{}})
    if !errors.Is(err, ErrRequiredInputMissing) { t.Fatalf("err=%v", err) }
}

func TestEndRejectsMultipleActiveResults(t *testing.T) {
    _, err := NewEnd().Execute(context.Background(), domain.NodeRequest{Inputs: map[string][]any{"result":{"a", "b"}}})
    if !errors.Is(err, ErrEndMultipleResults) { t.Fatalf("err=%v", err) }
}
```

- [ ] **Step 2: 运行测试并确认失败**

Run: `cd apps/api && CGO_ENABLED=0 go test ./internal/nodes/builtin -v`
Expected: FAIL，四类节点尚不存在。

- [ ] **Step 3: 实现开始节点和输入 Schema 派生**

字段类型映射必须固定为：`text|textarea|select -> string`、`number -> number`、`boolean -> boolean`、`json -> json`。字段 key 必须匹配 `^[A-Za-z][A-Za-z0-9_]{0,63}$` 且唯一。Start `Execute` 验证必填、类型、默认值和 enum，并只输出已定义字段。另提供：

```go
func DeriveInputSchema(config json.RawMessage) (json.RawMessage, error)
```

生成 JSON Schema 2020-12 object，包含 `properties`、`required` 和 `additionalProperties:false`。字段 label/description/default/placeholder 分别映射 title/description/default/`x-ui-placeholder`；textarea/json/select 分别映射 `x-ui-widget`，select options 映射 enum。

- [ ] **Step 4: 实现严格模板、条件和结束节点**

- 模板变量只接受 `{{identifier}}`，同名变量只生成一个输入端口；执行时不允许缺值或隐式字符串化对象，JSON 值使用紧凑 JSON。
- 条件运算符精确支持 `equals`、`notEquals`、`contains`、`greaterThan`、`lessThan`、`isEmpty`，只激活 `true` 或 `false` 并沿该端口输出原值。
- End 的 `result` 端口为 `CardinalitySingleActive`；0 个输入返回 `ErrEndResultMissing`，超过 1 个返回 `ErrEndMultipleResults`，1 个时输出 `result`。

- [ ] **Step 5: 运行节点测试并确认通过**

Run: `cd apps/api && CGO_ENABLED=0 go test ./internal/nodes/builtin -v`
Expected: PASS，覆盖非法字段 key、重复字段、模板变量去重、六个条件运算符和结束节点 0/1/N 输入。

- [ ] **Step 6: 提交**

```bash
git add apps/api/internal/nodes/builtin
git commit -m "feat: add core workflow nodes"
```

### Task 4: Mock/OpenAI-compatible Provider 与 LLM 节点

**Files:**
- Create: `apps/api/internal/modelprovider/provider.go`
- Create: `apps/api/internal/modelprovider/mock.go`
- Test: `apps/api/internal/modelprovider/mock_test.go`
- Create: `apps/api/internal/modelprovider/openai.go`
- Test: `apps/api/internal/modelprovider/openai_test.go`
- Create: `apps/api/internal/nodes/builtin/llm.go`
- Test: `apps/api/internal/nodes/builtin/llm_test.go`
- Modify: `apps/api/internal/nodes/builtin/register.go`

**Interfaces:**
- Produces: `modelprovider.Provider.Complete(context.Context, Request) (Response, error)`。
- Consumes: LLM node injects one Provider and never reads API Key from node config.

- [ ] **Step 1: 写 Provider 和 LLM 失败测试**

```go
func TestMockIsDeterministic(t *testing.T) {
    p := NewMock()
    got, err := p.Complete(context.Background(), Request{Model:"mock", Prompt:"你好"})
    if err != nil { t.Fatal(err) }
    if got.Text != "Mock 回复：你好" { t.Fatalf("text=%q", got.Text) }
}

func TestOpenAIUsesConfiguredEndpointAndBearerToken(t *testing.T) {
    var path, auth string
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        path, auth = r.URL.Path, r.Header.Get("Authorization")
        io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
    }))
    defer srv.Close()
    p := NewOpenAICompatible(srv.URL+"/v1", "secret", srv.Client())
    got, err := p.Complete(context.Background(), Request{Model:"gpt-test", Prompt:"hi"})
    if err != nil { t.Fatal(err) }
    if path != "/v1/chat/completions" || auth != "Bearer secret" || got.Text != "ok" { t.Fatalf("path=%s auth=%s got=%+v", path, auth, got) }
}
```

- [ ] **Step 2: 运行 Provider 测试并确认失败**

Run: `cd apps/api && CGO_ENABLED=0 go test ./internal/modelprovider ./internal/nodes/builtin -run 'TestMock|TestOpenAI|TestLLM' -v`
Expected: FAIL，Provider 接口不存在。

- [ ] **Step 3: 实现 Provider 与 LLM**

```go
type Request struct {
    Model string
    SystemPrompt string
    Prompt string
    Temperature float64
    MaxTokens int
}
type Response struct { Text string; Usage map[string]int }
type Provider interface { Complete(context.Context, Request) (Response, error) }
```

OpenAI client 固定 POST `{baseURL}/chat/completions`，请求体使用 `model`、`messages`、`temperature`、`max_tokens`；API Key 非空时才发送 Bearer Header；限制响应体 1 MiB，非 2xx 返回不含响应 Header 和密钥的 `ProviderError`。LLM 节点配置只含 `model`、`systemPrompt`、`temperature`、`maxTokens`，输入 `prompt:string`，输出 `text:string` 和 `usage:json`，节点 context 超时 60 秒。构造 LLM 节点时注入 `defaultModel`：节点 model 为空时使用默认值，两者都为空才返回配置错误。

- [ ] **Step 4: 运行测试并确认通过**

Run: `cd apps/api && CGO_ENABLED=0 go test ./internal/modelprovider ./internal/nodes/builtin -v`
Expected: PASS，覆盖无 choices、超大响应、非 2xx、超时和配置中出现 API Key 字段被 Schema 拒绝。

- [ ] **Step 5: 提交**

```bash
git add apps/api/internal/modelprovider apps/api/internal/nodes/builtin
git commit -m "feat: add model providers and llm node"
```

### Task 5: HTTP 与 Starlark 代码节点

**Files:**
- Create: `apps/api/internal/nodes/builtin/http.go`
- Create: `apps/api/internal/nodes/builtin/safehttp.go`
- Test: `apps/api/internal/nodes/builtin/http_test.go`
- Create: `apps/api/internal/nodes/builtin/code.go`
- Create: `apps/api/internal/nodes/builtin/starlark_value.go`
- Test: `apps/api/internal/nodes/builtin/code_test.go`
- Modify: `apps/api/internal/nodes/builtin/register.go`

**Interfaces:**
- Produces: HTTP node with `status:number`、`headers:json`、`body:any` outputs.
- Produces: Code node running `def main(input)` and returning JSON-compatible `result`.

- [ ] **Step 1: 写安全失败测试**

```go
func TestHTTPRejectsPrivateAddressByDefault(t *testing.T) {
    n := NewHTTP(HTTPOptions{AllowPrivateNetwork:false, LookupIP: net.DefaultResolver.LookupIP})
    cfg := json.RawMessage(`{"method":"GET","url":"http://127.0.0.1:8080","headers":[]}`)
    _, err := n.Execute(context.Background(), domain.NodeRequest{Config:cfg})
    if !errors.Is(err, ErrPrivateAddressBlocked) { t.Fatalf("err=%v", err) }
}

func TestHTTPRequiresEnvForSensitiveHeader(t *testing.T) {
    n := NewHTTP(HTTPOptions{})
    cfg := json.RawMessage(`{"method":"GET","url":"https://example.com","headers":[{"name":"Authorization","valueSource":"literal","value":"secret"}]}`)
    if _, err := n.Resolve(cfg); !errors.Is(err, ErrSensitiveHeaderMustUseEnv) { t.Fatalf("err=%v", err) }
}

func TestCodeExecutesMainAndLimitsSteps(t *testing.T) {
    n := NewCode(CodeOptions{MaxSteps:1000, Timeout:time.Second, MaxOutputBytes:1<<20})
    okCfg := json.RawMessage(`{"source":"def main(input):\n  return {\"answer\": input[\"n\"] + 1}"}`)
    got, err := n.Execute(context.Background(), domain.NodeRequest{Config:okCfg, Inputs:map[string][]any{"input":{map[string]any{"n":1.0}}}})
    if err != nil || got.Outputs["result"].(map[string]any)["answer"] != float64(2) { t.Fatalf("got=%v err=%v", got, err) }
    loopCfg := json.RawMessage(`{"source":"def main(input):\n  for i in range(1000000):\n    pass\n  return None"}`)
    if _, err := n.Execute(context.Background(), domain.NodeRequest{Config:loopCfg}); !errors.Is(err, ErrCodeStepLimit) { t.Fatalf("err=%v", err) }
}
```

- [ ] **Step 2: 运行 HTTP/Code 测试并确认失败**

Run: `cd apps/api && CGO_ENABLED=0 go test ./internal/nodes/builtin -run 'TestHTTP|TestCode' -v`
Expected: FAIL，节点尚不存在。

- [ ] **Step 3: 实现 Safe HTTP Client**

- URL 只允许 `http`/`https` 且禁止 `userinfo`。
- 自定义 `DialContext` 对 DNS 返回的每个 IP 使用 `netip.Addr` 检查 loopback、private、link-local、multicast 和 unspecified。
- `CheckRedirect` 最多允许 3 次并重新触发地址检查。
- Header `valueSource=env` 只保存 `envName`，执行时调用注入的 `LookupEnv`；五类敏感 Header 禁止 literal。
- 超时上限 30 秒，响应体上限 1 MiB；JSON 响应解码为 JSON 值，其他响应返回 string。

- [ ] **Step 4: 实现 Starlark 节点**

依赖 `go.starlark.net@latest` 并让 `go.sum` 固定实际 pseudo-version。执行规则：源码不超过 64 KiB；`Thread.SetMaxExecutionSteps(100000)`；context 取消时调用 `Thread.Cancel`；predeclared 为空；不提供 `load`；必须导出可调用的 `main`；Go JSON 值与 Starlark 值递归转换，深度上限 64；返回值 JSON 编码后不得超过 1 MiB。

- [ ] **Step 5: 运行安全测试并确认通过**

Run: `cd apps/api && CGO_ENABLED=0 go test ./internal/nodes/builtin -run 'TestHTTP|TestCode' -v`
Expected: PASS，覆盖 DNS 私网、重定向私网、响应过大、缺失 env、源码过大、无 main、步数和超时。

- [ ] **Step 6: 提交**

```bash
git add apps/api/go.mod apps/api/go.sum apps/api/internal/nodes/builtin
git commit -m "feat: add guarded http and code nodes"
```

### Task 6: 图校验与执行计划编译

**Files:**
- Create: `apps/api/internal/engine/plan.go`
- Create: `apps/api/internal/engine/compiler.go`
- Create: `apps/api/internal/engine/issues.go`
- Test: `apps/api/internal/engine/compiler_test.go`

**Interfaces:**
- Consumes: `domain.Graph`, `nodes.Registry`.
- Produces: `engine.Compiler.Compile(domain.Graph) (*engine.Plan, []domain.ValidationIssue)`.
- Produces later: Engine and workflow service consume compiled node/edge maps and topological order.

- [ ] **Step 1: 写编译器失败测试**

```go
func TestCompilerRejectsCycleAndMultipleEnds(t *testing.T) {
    graph := fixtureGraphWithCycleAndTwoEnds()
    _, issues := newFixtureCompiler(t).Compile(graph)
    assertIssueCodes(t, issues, "WORKFLOW_CYCLE", "WORKFLOW_END_COUNT")
}

func TestCompilerAllowsMultipleEdgesOnlyForSingleActive(t *testing.T) {
    valid := fixtureConditionalBranchesIntoOneEnd()
    if _, issues := newFixtureCompiler(t).Compile(valid); len(issues) != 0 { t.Fatalf("issues=%+v", issues) }
    invalid := fixtureTwoEdgesIntoOrdinaryPort()
    _, issues := newFixtureCompiler(t).Compile(invalid)
    assertIssueCodes(t, issues, "PORT_CARDINALITY_VIOLATION")
}
```

- [ ] **Step 2: 运行编译器测试并确认失败**

Run: `cd apps/api && CGO_ENABLED=0 go test ./internal/engine -run TestCompiler -v`
Expected: FAIL，Compiler 尚不存在。

- [ ] **Step 3: 实现确定性六阶段编译**

编译顺序固定为：schemaVersion 和唯一 ID → Registry 查找与配置 Schema → Resolve 动态端口 → 边引用、类型和 cardinality → 唯一 Start/End、Start 正向可达性与 End 反向可达性 → Kahn 拓扑排序。所有 issue 按 `nodeId, edgeId, path, code` 排序，避免测试和 UI 抖动。

```go
type Plan struct {
    Graph domain.Graph
    Nodes map[string]CompiledNode
    Incoming map[string][]domain.Edge
    Outgoing map[string][]domain.Edge
    TopologicalOrder []string
    StartNodeID string
    EndNodeID string
}
```

- [ ] **Step 4: 运行完整编译测试**

Run: `cd apps/api && CGO_ENABLED=0 go test ./internal/engine -run TestCompiler -v`
Expected: PASS，覆盖未知节点版本、配置错误、悬空边、自连接、类型不匹配、不可达节点、无终止路径、环和结束节点计数。

- [ ] **Step 5: 提交**

```bash
git add apps/api/internal/engine
git commit -m "feat: compile and validate workflow graphs"
```

### Task 7: DAG 调度、条件跳过与 NDJSON 事件模型

**Files:**
- Create: `apps/api/internal/engine/event.go`
- Create: `apps/api/internal/engine/observer.go`
- Create: `apps/api/internal/engine/engine.go`
- Create: `apps/api/internal/engine/scheduler.go`
- Test: `apps/api/internal/engine/engine_test.go`

**Interfaces:**
- Consumes: `*engine.Plan`, node `Execute`, run input.
- Produces: `engine.Engine.Run(context.Context, string, *Plan, map[string]any, Observer) (RunResult, error)`，第二个参数为 runId。
- Produces: ordered events consumed later by persistence and NDJSON handlers.

- [ ] **Step 1: 写调度器失败测试**

```go
func TestEngineSkipsInactiveBranchWithoutDeadlock(t *testing.T) {
    plan := compileFixture(t, fixtureConditionalBranchesIntoOneEnd())
    observer := &memoryObserver{}
    result, err := New(Options{MaxParallel:4}).Run(context.Background(), "run-1", plan, map[string]any{"value":"yes"}, observer)
    if err != nil { t.Fatal(err) }
    if result.Output != "true-result" { t.Fatalf("output=%v", result.Output) }
    assertNodeStatus(t, observer.Events(), "false-node", domain.NodeSkipped)
}

func TestEngineFailsWhenEndReceivesMultipleActiveValues(t *testing.T) {
    plan := compileFixture(t, fixtureParallelBranchesDirectlyIntoEnd())
    _, err := New(Options{MaxParallel:4}).Run(context.Background(), "run-1", plan, nil, &memoryObserver{})
    if !errors.Is(err, builtin.ErrEndMultipleResults) { t.Fatalf("err=%v", err) }
}

func TestEngineRunsIndependentNodesConcurrently(t *testing.T) {
    tracker := newConcurrencyTracker()
    plan := compileFixtureWithTracker(t, fixtureTwoParallelNodes(), tracker)
    _, err := New(Options{MaxParallel:2}).Run(context.Background(), "run-1", plan, nil, &memoryObserver{})
    if err != nil { t.Fatal(err) }
    if tracker.Max() != 2 { t.Fatalf("max concurrency=%d", tracker.Max()) }
}
```

- [ ] **Step 2: 运行引擎测试并确认失败**

Run: `cd apps/api && CGO_ENABLED=0 go test ./internal/engine -run TestEngine -v`
Expected: FAIL，Engine 和事件模型不存在。

- [ ] **Step 3: 定义事件和观察者**

```go
type Event struct {
    Sequence int64 `json:"sequence"`
    Type string `json:"type"`
    RunID string `json:"runId"`
    NodeID string `json:"nodeId,omitempty"`
    Status domain.NodeStatus `json:"status,omitempty"`
    Input any `json:"input,omitempty"`
    Output any `json:"output,omitempty"`
    Error *domain.PublicError `json:"error,omitempty"`
    Timestamp time.Time `json:"timestamp"`
}

type Observer interface {
    Observe(context.Context, Event) error
}
```

事件顺序只能由调度器主 goroutine 分配；worker 不得直接写 Observer。Observer 失败必须取消运行，防止持久化和客户端事件静默分叉。

- [ ] **Step 4: 实现集中式调度器**

调度器维护 `pending/running/completed/failed/skipped/cancelled`，以及每条边的 `unknown/active/inactive`。循环规则：

1. Start 立即就绪。
2. 普通节点在全部上游终态且所有必需 `one` 端口恰有一个活跃值时就绪。
3. 普通节点在全部上游终态但缺少必需活跃值时变为 `skipped`，其全部出边变为 inactive。
4. End 在全部潜在上游终态后始终执行，让 End executor 判定 0/1/N 个 active 值。
5. worker 结果回到主 goroutine；只激活 `NodeResult.ActivePorts`，若为空则激活 `Outputs` 中存在的端口。
6. 节点执行错误只取消该节点的未运行后代，并保留无依赖关系的并行分支继续完成；已完成结果保留，运行最终失败且不产生部分输出。只有客户端取消、总超时或 Observer 失败才取消共享 context 和全部未完成节点。

Engine 使用 `context.WithTimeout` 限制总运行 120 秒，使用 semaphore 限制默认并发 4。返回：

```go
type RunResult struct {
    RunID string
    Output any
    NodeStatuses map[string]domain.NodeStatus
    StartedAt time.Time
    EndedAt time.Time
}
```

- [ ] **Step 5: 运行引擎全测试并确认通过**

Run: `cd apps/api && CGO_ENABLED=0 go test ./internal/engine -v`
Expected: PASS，覆盖事件 sequence、Observer 错误、context 取消、总超时、并发上限、0/N 个结束结果、跳过传播，以及一个分支失败时无关分支仍完成。

- [ ] **Step 6: 提交**

```bash
git add apps/api/internal/engine apps/api/internal/domain
git commit -m "feat: execute workflows with dag scheduler"
```

### Task 8: PostgreSQL migration 与仓储

**Files:**
- Create: `apps/api/migrations/000001_initial.sql`
- Create: `apps/api/migrations/embed.go`
- Create: `apps/api/internal/store/postgres/migrate.go`
- Test: `apps/api/internal/store/postgres/migrate_test.go`
- Create: `apps/api/internal/store/postgres/store.go`
- Create: `apps/api/internal/store/postgres/workflows.go`
- Create: `apps/api/internal/store/postgres/versions.go`
- Create: `apps/api/internal/store/postgres/runs.go`
- Test: `apps/api/internal/store/postgres/store_test.go`
- Modify: `Makefile`

**Interfaces:**
- Produces: `postgres.Open(context.Context, string) (*Store, error)` and `Store.Migrate(context.Context) error`.
- Produces: workflow service repository methods listed below.

- [ ] **Step 1: 写 migration/仓储失败集成测试**

```go
func TestPublishPreservesVersionAndTestSnapshot(t *testing.T) {
    store := migratedTestStore(t)
    wf := createWorkflowFixture(t, store)
    version, err := store.Publish(context.Background(), wf.ID, wf.DraftRevision, wf.DraftGraph, json.RawMessage(`{"type":"object"}`))
    if err != nil { t.Fatal(err) }
    if version.Version != 1 || version.WorkflowID != wf.ID { t.Fatalf("version=%+v", version) }

    run := newTestRun(wf.ID, wf.DraftRevision, wf.DraftGraph)
    if err := store.CreateRun(context.Background(), run); err != nil { t.Fatal(err) }
    loaded, err := store.GetRun(context.Background(), run.ID)
    if err != nil || !bytes.Equal(loaded.GraphSnapshot, wf.DraftGraph) { t.Fatalf("loaded=%+v err=%v", loaded, err) }
}

func TestPublishedRunRejectsVersionFromAnotherWorkflow(t *testing.T) {
    store := migratedTestStore(t)
    first, second := createTwoPublishedWorkflows(t, store)
    run := newPublishedRun(first.WorkflowID, second.ID)
    if err := store.CreateRun(context.Background(), run); err == nil { t.Fatal("expected composite foreign key violation") }
}
```

- [ ] **Step 2: 启动 PostgreSQL 并确认测试失败**

Run: `docker compose up -d db`
Run: `cd apps/api && TEST_DATABASE_URL='postgres://agent:agent@localhost:5432/agent_studio?sslmode=disable' CGO_ENABLED=0 go test ./internal/store/postgres -v`
Expected: FAIL，migration 和 Store 不存在。

测试 helper `newTestRun`/`newPublishedRun` 必须填充非空 ID、`status=running`、空 object input 和 `startedAt=time.Now()`，使外键测试不会先被其他 NOT NULL/Check 约束截断。未设置 `TEST_DATABASE_URL` 时集成测试明确 `t.Skip`；`make verify` 设置该变量，因而发布质量门不会跳过。

- [ ] **Step 3: 创建精确数据库结构**

Migration runner 自举 `schema_migrations`；`000001_initial.sql` 必须创建业务表：

```sql
CREATE TABLE workflows (
  id uuid PRIMARY KEY,
  name text NOT NULL,
  slug text NOT NULL UNIQUE,
  description text NOT NULL DEFAULT '',
  draft_graph jsonb NOT NULL,
  draft_revision bigint NOT NULL DEFAULT 1,
  published_version_id uuid NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE workflow_versions (
  id uuid PRIMARY KEY,
  workflow_id uuid NOT NULL REFERENCES workflows(id),
  version integer NOT NULL CHECK (version > 0),
  graph jsonb NOT NULL,
  input_schema jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workflow_id, version),
  UNIQUE (workflow_id, id)
);

ALTER TABLE workflows ADD CONSTRAINT workflows_published_version_fk
  FOREIGN KEY (id, published_version_id)
  REFERENCES workflow_versions(workflow_id, id);

CREATE TABLE runs (
  id uuid PRIMARY KEY,
  workflow_id uuid NOT NULL REFERENCES workflows(id),
  workflow_version_id uuid NULL,
  draft_revision bigint NULL,
  graph_snapshot jsonb NULL,
  mode text NOT NULL CHECK (mode IN ('test','published')),
  status text NOT NULL CHECK (status IN ('running','completed','failed','cancelled')),
  input jsonb NOT NULL,
  output jsonb NULL,
  error jsonb NULL,
  started_at timestamptz NOT NULL,
  ended_at timestamptz NULL,
  CONSTRAINT runs_version_fk FOREIGN KEY (workflow_id, workflow_version_id)
    REFERENCES workflow_versions(workflow_id, id),
  CONSTRAINT runs_mode_source_check CHECK (
    (mode='published' AND workflow_version_id IS NOT NULL AND draft_revision IS NULL AND graph_snapshot IS NULL)
    OR
    (mode='test' AND workflow_version_id IS NULL AND draft_revision IS NOT NULL AND graph_snapshot IS NOT NULL)
  )
);

CREATE TABLE node_runs (
  id uuid PRIMARY KEY,
  run_id uuid NOT NULL REFERENCES runs(id),
  node_id text NOT NULL,
  node_type text NOT NULL,
  status text NOT NULL,
  input jsonb NULL,
  output jsonb NULL,
  error jsonb NULL,
  started_at timestamptz NULL,
  ended_at timestamptz NULL,
  UNIQUE (run_id, node_id)
);
```

增加 `workflows(updated_at desc)`、`workflow_versions(workflow_id,version desc)`、`runs(workflow_id,started_at desc)` 和 `node_runs(run_id)` 索引。

- [ ] **Step 4: 实现嵌入式 migration runner**

使用 `//go:embed ../../../migrations/*.sql` 不可跨越父目录，因此在 `internal/store/postgres/migrate.go` 中 embed 路径对应复制到包内的 `migrations` 会造成双份来源，禁止采用。正确方式是在 `apps/api/migrations/embed.go` 定义：

```go
package migrations

import "embed"

//go:embed *.sql
var Files embed.FS
```

Runner 首先执行 `CREATE TABLE IF NOT EXISTS schema_migrations (version bigint PRIMARY KEY, name text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now())`，再读取六位数字前缀，按版本排序，使用 PostgreSQL advisory transaction lock，检查元数据表，每个未应用文件在事务内执行并记录。重复运行无副作用。集成测试 helper 不调用 `t.Parallel`，每个测试前在全局互斥锁内 `TRUNCATE node_runs, runs, workflow_versions, workflows CASCADE`，测试结束释放锁。

- [ ] **Step 5: 实现 pgx 仓储与错误映射**

依赖固定 `github.com/jackc/pgx/v5@v5.10.0`、`github.com/google/uuid@v1.6.0`。Store 精确提供：

```go
ListWorkflows(context.Context) ([]domain.Workflow, error)
CreateWorkflow(context.Context, domain.Workflow) (domain.Workflow, error)
GetWorkflow(context.Context, string) (domain.Workflow, error)
UpdateDraft(context.Context, string, int64, json.RawMessage) (domain.Workflow, error)
Publish(context.Context, string, int64, json.RawMessage, json.RawMessage) (domain.WorkflowVersion, error)
GetCurrentAgentVersion(context.Context, string) (domain.Workflow, domain.WorkflowVersion, error)
GetAgentVersion(context.Context, string, string) (domain.Workflow, domain.WorkflowVersion, error)
CreateRun(context.Context, domain.Run) error
UpsertNodeRun(context.Context, domain.NodeRun) error
FinishRun(context.Context, string, domain.RunStatus, any, *domain.PublicError, time.Time) error
GetRun(context.Context, string) (domain.Run, []domain.NodeRun, error)
ListRuns(context.Context, string, int) ([]domain.Run, error)
```

`UpdateDraft` 使用 `WHERE id=$1 AND draft_revision=$2` 并原子加一；0 行映射为 `ErrRevisionConflict`。`Publish` 在 transaction 中锁定 workflow、核对 revision、计算 `MAX(version)+1`、插入版本并更新 `published_version_id`。

- [ ] **Step 6: 运行 PostgreSQL 集成测试**

Run: `cd apps/api && TEST_DATABASE_URL='postgres://agent:agent@localhost:5432/agent_studio?sslmode=disable' CGO_ENABLED=0 go test ./internal/store/postgres -count=1 -v`
Expected: PASS，覆盖 migration 幂等、revision 409 映射、发布事务回滚、组合外键和 test snapshot。

- [ ] **Step 7: 提交**

```bash
git add Makefile apps/api/go.mod apps/api/go.sum apps/api/migrations apps/api/internal/store/postgres
git commit -m "feat: persist workflows and runs in postgres"
```

### Task 9: 工作流、发布与运行应用服务

**Files:**
- Create: `apps/api/internal/workflow/ports.go`
- Create: `apps/api/internal/workflow/service.go`
- Test: `apps/api/internal/workflow/service_test.go`
- Create: `apps/api/internal/workflow/run_service.go`
- Test: `apps/api/internal/workflow/run_service_test.go`
- Create: `apps/api/internal/workflow/redact.go`
- Test: `apps/api/internal/workflow/redact_test.go`

**Interfaces:**
- Consumes: Store、Compiler、Engine、Start input-schema derivation.
- Produces: all use cases consumed by HTTP handlers.

- [ ] **Step 1: 写 revision、版本竞态和 test snapshot 失败测试**

```go
func TestPublishRequiresExactRevision(t *testing.T) {
    svc, store := newServiceFixture(t)
    wf := store.Workflow()
    _, err := svc.Publish(context.Background(), wf.ID, wf.DraftRevision-1)
    if !errors.Is(err, domain.ErrRevisionConflict) { t.Fatalf("err=%v", err) }
}

func TestPrepareAgentUsesRequestedVersionAfterNewPublish(t *testing.T) {
    svc, store := newRunServiceFixture(t)
    v1 := store.AddVersion(graphReturning("v1"))
    store.SetCurrentVersion(store.AddVersion(graphReturning("v2")))
    prepared, err := svc.PrepareAgent(context.Background(), "demo", v1.ID, map[string]any{})
    if err != nil { t.Fatal(err) }
    result, err := svc.Execute(context.Background(), prepared, &memoryObserver{})
    if err != nil { t.Fatal(err) }
    if result.Output != "v1" { t.Fatalf("output=%v", result.Output) }
}

func TestTestRunStoresGraphSnapshotBeforeExecution(t *testing.T) {
    svc, store := newRunServiceFixture(t)
    wf := store.Workflow()
    prepared, err := svc.PrepareDraft(context.Background(), wf.ID, wf.DraftRevision, nil)
    if err != nil { t.Fatal(err) }
    if _, err := svc.Execute(context.Background(), prepared, &memoryObserver{}); err != nil { t.Fatal(err) }
    if !bytes.Equal(store.LastRun().GraphSnapshot, wf.DraftGraph) { t.Fatal("snapshot not persisted") }
}
```

- [ ] **Step 2: 运行应用服务测试并确认失败**

Run: `cd apps/api && CGO_ENABLED=0 go test ./internal/workflow -v`
Expected: FAIL，Service 尚不存在。

- [ ] **Step 3: 实现草稿与发布服务**

```go
type Service struct {
    store Store
    compiler Compiler
}

func (s *Service) Create(ctx context.Context, in CreateWorkflowInput) (domain.Workflow, error)
func (s *Service) SaveDraft(ctx context.Context, id string, revision int64, graph domain.Graph) (domain.Workflow, error)
func (s *Service) Validate(ctx context.Context, id string) []domain.ValidationIssue
func (s *Service) Publish(ctx context.Context, id string, revision int64) (domain.WorkflowVersion, error)
func (s *Service) AgentManifest(ctx context.Context, slug string) (AgentManifest, error)
```

Create 生成一个 Start 和一个 End 的未连线图。SaveDraft 只验证可解码结构和 schemaVersion，允许不完整图。Validate/Publish 调用 Compiler；Publish 从 Start 派生 input schema，再调用 transaction Store。AgentManifest 必须返回 `WorkflowVersionID`、显示版本、标题、说明和 input schema。

`CreateWorkflowInput` 精确包含 `Name`、`Slug`、`Description`；name/slug 必填，slug 匹配 `^[a-z0-9]+(?:-[a-z0-9]+)*$`。重复 slug 映射为 `WORKFLOW_SLUG_CONFLICT`。

- [ ] **Step 4: 实现 RunService、复合 Observer 和脱敏**

```go
func (s *RunService) PrepareDraft(ctx context.Context, workflowID string, revision int64, input map[string]any) (*PreparedRun, error)
func (s *RunService) PrepareAgent(ctx context.Context, slug, workflowVersionID string, input map[string]any) (*PreparedRun, error)
func (s *RunService) Execute(ctx context.Context, prepared *PreparedRun, observer engine.Observer) (engine.RunResult, error)
```

```go
type PreparedRun struct {
    RunID string
    Plan *engine.Plan
    Input map[string]any
    Mode domain.RunMode
    WorkflowID string
    WorkflowVersionID *string
    DraftRevision *int64
}
```

PrepareDraft 校验 revision、编译图、派生并校验开始输入 Schema，再创建包含 graph snapshot 的 test run。PrepareAgent 调用 `GetAgentVersion(slug, workflowVersionID)` 原子验证 slug 归属并加载不可变版本，使用版本的 input schema 校验输入，不读取 current version 替换请求版本。输入必须拒绝未知字段和类型不匹配。只有 Prepare 成功后 HTTP 才能切换为 NDJSON；Execute 将 runId 传给 Engine。

复合 Observer 先对整个 Event 递归脱敏，再将同一个安全 Event 持久化并转发给 HTTP observer，避免客户端与数据库看到不同敏感内容。所有输入、输出和错误使用：

```go
func Redact(value any) any
```

匹配字段名大小写不敏感的 authorization、proxy-authorization、cookie、set-cookie、api-key、apikey、token、secret，值替换为 `"[REDACTED]"`。

- [ ] **Step 5: 运行服务测试并确认通过**

Run: `cd apps/api && CGO_ENABLED=0 go test ./internal/workflow -v`
Expected: PASS，覆盖 invalid graph、revision 冲突、旧版本绑定、snapshot、持久化失败、Observer 失败和递归脱敏。

- [ ] **Step 6: 提交**

```bash
git add apps/api/internal/workflow
git commit -m "feat: orchestrate workflow publishing and runs"
```

### Task 10: OpenAPI、chi HTTP API 与 NDJSON 输出

**Files:**
- Create: `contracts/openapi.yaml`
- Create: `apps/api/internal/httpapi/router.go`
- Create: `apps/api/internal/httpapi/middleware.go`
- Create: `apps/api/internal/httpapi/json.go`
- Create: `apps/api/internal/httpapi/errors.go`
- Create: `apps/api/internal/httpapi/nodes.go`
- Create: `apps/api/internal/httpapi/workflows.go`
- Create: `apps/api/internal/httpapi/agents.go`
- Create: `apps/api/internal/httpapi/runs.go`
- Test: `apps/api/internal/httpapi/router_test.go`
- Test: `apps/api/internal/httpapi/stream_test.go`
- Create: `apps/api/cmd/server/main.go`

**Interfaces:**
- Produces: exact HTTP routes from the approved spec, excluding delete workflow.
- Produces: `httpapi.NewRouter(Dependencies) http.Handler`.

- [ ] **Step 1: 写 Handler 和流式失败测试**

```go
func TestAgentRunUsesBodyVersionIDAndStreamsNDJSON(t *testing.T) {
    deps := fixtureDeps()
    router := NewRouter(deps)
    req := httptest.NewRequest(http.MethodPost, "/api/agents/demo/runs", strings.NewReader(`{"workflowVersionId":"v1","input":{"topic":"x"}}`))
    req.Header.Set("Content-Type", "application/json")
    rec := httptest.NewRecorder()
    router.ServeHTTP(rec, req)
    if rec.Code != http.StatusOK { t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String()) }
    if got := rec.Header().Get("Content-Type"); got != "application/x-ndjson" { t.Fatalf("content-type=%s", got) }
    lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
    if len(lines) < 2 || !strings.Contains(lines[0], `"type":"run.started"`) { t.Fatalf("body=%s", rec.Body.String()) }
    if deps.runner.LastVersionID != "v1" { t.Fatalf("version=%s", deps.runner.LastVersionID) }
}

func TestRevisionConflictUsesStableErrorShape(t *testing.T) {
    rec := performFixtureRequest(t, http.MethodPut, "/api/workflows/w1", `{"draftRevision":1,"graph":{"schemaVersion":1,"nodes":[],"edges":[]}}`)
    assertJSONError(t, rec, http.StatusConflict, "WORKFLOW_REVISION_CONFLICT")
}
```

- [ ] **Step 2: 运行 HTTP 测试并确认失败**

Run: `cd apps/api && CGO_ENABLED=0 go test ./internal/httpapi -v`
Expected: FAIL，router 尚不存在。

- [ ] **Step 3: 编写 OpenAPI 3.1 契约**

契约必须逐项定义以下 routes：

```text
GET/POST /api/workflows
GET/PUT /api/workflows/{id}
POST /api/workflows/{id}/validate
POST /api/workflows/{id}/test-runs
POST /api/workflows/{id}/publish
GET /api/workflows/{id}/runs
GET /api/node-types
POST /api/node-types/{type}/{version}/resolve
GET /api/agents/{slug}
POST /api/agents/{slug}/runs
GET /api/runs/{id}
GET /healthz
GET /readyz
```

复用 schemas：`Graph`、`NodeDefinition`、`ValidationIssue`、`Workflow`、`WorkflowVersion`、`AgentManifest`、`Run`、`RunEvent`、`ErrorResponse`。运行响应声明 `application/x-ndjson`，每行符合 `RunEvent`。Agent run request 的 `workflowVersionId` 必填。

- [ ] **Step 4: 实现 Router、middleware 和错误映射**

固定 `github.com/go-chi/chi/v5@v5.3.1`。middleware 顺序：RequestID → Recoverer → JSON structured access log → CORS only configured WebOrigin。JSON body 上限 2 MiB，拒绝未知字段和尾随 JSON。错误映射固定：400 request、404 not found、409 revision/slug conflict、422 workflow invalid、500 internal；500 只返回 requestId。

- [ ] **Step 5: 实现 prepare-then-stream 与 NDJSON Observer**

运行 Handler 先调用 `PrepareDraft` 或 `PrepareAgent`；Prepare 错误仍返回普通 JSON 和对应 4xx/5xx。Prepare 成功后才写 `Content-Type: application/x-ndjson`、`Cache-Control: no-store`、`X-Content-Type-Options: nosniff` 并调用 Execute。`streamObserver.Observe` 使用 `json.Encoder.Encode` 后调用 `http.Flusher.Flush`。客户端断开通过 request context 取消 Engine。

- [ ] **Step 6: 组装 server 生命周期**

`main.go` 加载 config、连接 pgxpool、运行 migration、构造 registry 和七类节点、选择 model provider、构造 compiler/engine/store/services/router；接收 SIGINT/SIGTERM 后最多 10 秒优雅关闭。`/readyz` 检查 `pool.Ping` 与最新 migration version。

- [ ] **Step 7: 运行 HTTP 与全 Go 测试**

Run: `cd apps/api && CGO_ENABLED=0 go test ./internal/httpapi ./... -count=1`
Expected: PASS，覆盖 invalid JSON、未知字段、CORS、panic recovery、版本绑定、flush、取消和错误脱敏。

- [ ] **Step 8: 提交**

```bash
git add contracts apps/api/cmd apps/api/internal/httpapi apps/api/go.mod apps/api/go.sum
git commit -m "feat: expose workflow and agent http api"
```

### Task 11: Web API 客户端、通用 Schema 表单与工作流列表

**Files:**
- Modify: `apps/web/package.json`
- Modify: `apps/web/src/app/App.tsx`
- Create: `apps/web/src/app/router.tsx`
- Create: `apps/web/src/lib/api/generated.ts`
- Create: `apps/web/src/lib/api/client.ts`
- Test: `apps/web/src/lib/api/client.test.ts`
- Create: `apps/web/src/lib/api/ndjson.ts`
- Test: `apps/web/src/lib/api/ndjson.test.ts`
- Create: `apps/web/src/components/schema-form/types.ts`
- Create: `apps/web/src/components/schema-form/SchemaForm.tsx`
- Create: `apps/web/src/components/schema-form/Field.tsx`
- Create: `apps/web/src/components/schema-form/ArrayField.tsx`
- Test: `apps/web/src/components/schema-form/SchemaForm.test.tsx`
- Create: `apps/web/src/features/workflows/WorkflowListPage.tsx`
- Test: `apps/web/src/features/workflows/WorkflowListPage.test.tsx`

**Interfaces:**
- Produces: typed `api` methods and `readNDJSON(response, onEvent)`.
- Produces: `<SchemaForm schema value onChange onSubmit submitLabel />`.

- [ ] **Step 1: 写 NDJSON chunk 和 Schema 表单失败测试**

```ts
it('解析跨 chunk 的 NDJSON 行', async () => {
  const response = chunkedResponse(['{"type":"run.', 'started"}\n{"type":"run.completed"}\n'])
  const events: RunEvent[] = []
  await readNDJSON(response, event => events.push(event))
  expect(events.map(event => event.type)).toEqual(['run.started', 'run.completed'])
})

it('渲染必填文本、布尔、单选和 JSON 字段', async () => {
  render(<SchemaForm schema={startSchemaFixture} value={{}} onChange={vi.fn()} onSubmit={vi.fn()} submitLabel="运行" />)
  expect(screen.getByLabelText('主题')).toBeRequired()
  expect(screen.getByRole('checkbox', { name: '启用' })).toBeInTheDocument()
  expect(screen.getByRole('combobox', { name: '风格' })).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: '运行' }))
  expect(screen.getByText('主题为必填项')).toBeInTheDocument()
})
```

- [ ] **Step 2: 运行 Web 测试并确认失败**

Run: `corepack pnpm@10.34.5 --filter @agent-studio/web test --run src/lib/api src/components/schema-form src/features/workflows`
Expected: FAIL，客户端和表单不存在。

- [ ] **Step 3: 生成 API 类型并实现客户端**

安装 `openapi-typescript`，增加脚本：

```json
"generate:api": "openapi-typescript ../../contracts/openapi.yaml -o src/lib/api/generated.ts"
```

所有 JSON API 通过一个 `request<T>` 实现；非 2xx 解码 `ErrorResponse` 并抛 `APIError`。运行 API 返回原始 Response 给 `readNDJSON`，解析器必须处理 CRLF、空行、最后无换行、UTF-8 跨 chunk 和 AbortSignal。

- [ ] **Step 4: 实现通用 Schema 表单**

安装 Ajv 8。表单递归支持 object、string、number、integer、boolean、enum、array，以及 `x-ui-widget` 的 text、textarea、select、code、json；将 minimum/maximum/minLength/maxLength/pattern 传给原生控件，并遵循 `x-ui-order` 与 `x-ui-placeholder`。Ajv 使用 `{allErrors:true, strict:false, useDefaults:true}`；错误按 instancePath 映射中文文案。JSON widget 在 blur 和 submit 时解析，非法 JSON 不调用 submit。

- [ ] **Step 5: 实现路由与工作流列表**

使用 `react-router-dom@7` 的 `BrowserRouter`/`Routes`：`/` 重定向 `/workflows`，并声明 `/workflows`、`/workflows/:id`、`/workflows/:id/runs`、`/agents/:slug`。列表加载 `GET /api/workflows`，显示名称、草稿 revision、发布版本和更新时间；“新建工作流”打开包含名称、slug、说明的对话框，校验后调用 POST 并跳转编辑器。加载、空列表、slug 冲突和其他错误状态均有中文可访问文案。

- [ ] **Step 6: 运行类型生成和 Web 测试**

Run: `corepack pnpm@10.34.5 --filter @agent-studio/web generate:api`
Run: `corepack pnpm@10.34.5 --filter @agent-studio/web test --run`
Run: `corepack pnpm@10.34.5 --filter @agent-studio/web lint`
Expected: PASS，`generated.ts` 已纳入 Git 且无手工修改。

- [ ] **Step 7: 提交**

```bash
git add apps/web contracts/openapi.yaml pnpm-lock.yaml
git commit -m "feat: add schema forms and workflow list"
```

### Task 12: 画布优先 Studio 编辑器与串行自动保存

**Files:**
- Create: `apps/web/src/features/studio/types.ts`
- Create: `apps/web/src/features/studio/graphAdapter.ts`
- Test: `apps/web/src/features/studio/graphAdapter.test.ts`
- Create: `apps/web/src/features/studio/saveQueue.ts`
- Test: `apps/web/src/features/studio/saveQueue.test.ts`
- Create: `apps/web/src/features/studio/StudioPage.tsx`
- Test: `apps/web/src/features/studio/StudioPage.test.tsx`
- Create: `apps/web/src/features/studio/WorkflowCanvas.tsx`
- Create: `apps/web/src/features/studio/GenericNode.tsx`
- Create: `apps/web/src/features/studio/NodeLibraryDrawer.tsx`
- Create: `apps/web/src/features/studio/ConfigDrawer.tsx`
- Create: `apps/web/src/features/studio/TestRunDrawer.tsx`
- Create: `apps/web/src/features/studio/PublishDialog.tsx`
- Create: `apps/web/src/features/studio/studio.css`

**Interfaces:**
- Consumes: Graph、NodeDefinition、resolve API、workflow save/validate/test/publish API.
- Produces: `/workflows/:id` complete editor.
- Produces: `SaveQueue.enqueue(graph)` and `SaveQueue.flush()` with strictly one in-flight PUT.

- [ ] **Step 1: 写保存队列红灯测试**

```ts
it('同一时刻只保存一次并合并为最新草稿', async () => {
  const first = deferred<{ draftRevision: number }>()
  const save = vi.fn().mockReturnValueOnce(first.promise).mockResolvedValueOnce({ draftRevision: 3 })
  const queue = new SaveQueue(1, save)
  queue.enqueue(graph('a'))
  queue.enqueue(graph('b'))
  queue.enqueue(graph('c'))
  expect(save).toHaveBeenCalledTimes(1)
  first.resolve({ draftRevision: 2 })
  await queue.flush()
  expect(save).toHaveBeenCalledTimes(2)
  expect(save.mock.calls[1][0]).toEqual({ draftRevision: 2, graph: graph('c') })
})
```

- [ ] **Step 2: 写 Studio 交互红灯测试**

```tsx
it('打开节点库、添加节点并在右侧配置', async () => {
  renderStudio({ workflow: workflowFixture, definitions: definitionsFixture })
  await userEvent.click(screen.getByRole('button', { name: '添加节点' }))
  await userEvent.click(screen.getByRole('button', { name: '提示词模板' }))
  expect(screen.getByRole('dialog', { name: '节点配置' })).toBeInTheDocument()
  await userEvent.type(screen.getByLabelText('模板'), '回答：{{topic}}')
  expect(resolveNodeMock).toHaveBeenLastCalledWith('template', '1', expect.objectContaining({ template: '回答：{{topic}}' }))
})
```

- [ ] **Step 3: 运行 Studio 测试并确认失败**

Run: `corepack pnpm@10.34.5 --filter @agent-studio/web test --run src/features/studio`
Expected: FAIL，编辑器尚不存在。

- [ ] **Step 4: 实现 Graph adapter 与 SaveQueue**

Graph adapter 在领域 Node 与 React Flow Node 之间双向转换，保留 `typeVersion`、config、sourcePort/targetPort。SaveQueue 在 800ms debounce 后启动保存；in-flight 期间只保存最后一个 pending graph；409 后停止队列并暴露 `conflict` 状态；`flush` 等待 debounce、in-flight 和 pending 全部完成。

- [ ] **Step 5: 实现画布优先布局**

- 顶栏：返回列表、工作流名、保存状态、添加节点、测试运行、发布。
- 中央：ReactFlow + Background + Controls + MiniMap；默认不显示侧栏。
- 左抽屉：分类与搜索节点定义，点击后放置在当前 viewport 中心。
- GenericNode：按 resolved ports 绘制 Handle，显示节点名、类型和错误状态。
- 右抽屉：复用 SchemaForm；配置变化 250ms 后调用 resolve；消失的 handle 使关联边标红，不静默删除。
- 连线：前端检查端口类型和 cardinality；服务端错误仍为权威结果。
- 底抽屉：测试开始表单、NDJSON 节点状态、最终结果或稳定错误。
- 发布：先 `await saveQueue.flush()`，再 validate；错误聚焦元素，通过后 publish 并显示 `/agents/{slug}` 链接。

- [ ] **Step 6: 运行 Studio 测试与构建**

Run: `corepack pnpm@10.34.5 --filter @agent-studio/web test --run src/features/studio`
Run: `corepack pnpm@10.34.5 --filter @agent-studio/web lint`
Run: `corepack pnpm@10.34.5 --filter @agent-studio/web build`
Expected: PASS，覆盖 debounce、single-flight、409、动态 handle、类型拒绝、测试抽屉和 publish flush。

- [ ] **Step 7: 提交**

```bash
git add apps/web/src/features/studio apps/web/src/app pnpm-lock.yaml
git commit -m "feat: build canvas-first workflow editor"
```

### Task 13: Agent 页面、运行记录、E2E、文档与最终质量门

**Files:**
- Create: `apps/web/src/features/agent/AgentPage.tsx`
- Test: `apps/web/src/features/agent/AgentPage.test.tsx`
- Create: `apps/web/src/features/agent/agent.css`
- Create: `apps/web/src/features/runs/RunProgress.tsx`
- Create: `apps/web/src/features/runs/RunHistoryPage.tsx`
- Test: `apps/web/src/features/runs/RunHistoryPage.test.tsx`
- Modify: `apps/web/src/app/router.tsx`
- Modify: `apps/web/package.json`
- Modify: `pnpm-lock.yaml`
- Create: `apps/web/playwright.config.ts`
- Create: `apps/web/e2e/agent-studio.spec.ts`
- Create: `docs/node-development.md`
- Create: `README.md`
- Modify: `.env.example`
- Modify: `Makefile`

**Interfaces:**
- Produces: `/agents/:slug` generated form and `/workflows/:id/runs` history.
- Produces: `make verify` and `make test-e2e` as release gates.

- [ ] **Step 1: 写 Agent 版本绑定红灯测试**

```tsx
it('运行时回传页面加载时的 workflowVersionId', async () => {
  api.getAgentManifest.mockResolvedValue({ workflowVersionId: 'version-1', version: 1, title: '知识助手', inputSchema })
  renderAgentPage('demo')
  await userEvent.type(await screen.findByLabelText('主题'), 'Agent')
  api.runAgent.mockResolvedValue(ndjsonResponse(runCompleted('ok')))
  await userEvent.click(screen.getByRole('button', { name: '运行 Agent' }))
  expect(api.runAgent).toHaveBeenCalledWith('demo', { workflowVersionId: 'version-1', input: { topic: 'Agent' } }, expect.any(AbortSignal))
  expect(await screen.findByText('ok')).toBeInTheDocument()
})
```

- [ ] **Step 2: 运行 Agent/记录测试并确认失败**

Run: `corepack pnpm@10.34.5 --filter @agent-studio/web test --run src/features/agent src/features/runs`
Expected: FAIL，页面尚不存在。

- [ ] **Step 3: 实现聚焦式 Agent 页面和运行记录**

AgentPage 加载 manifest，使用单栏 SchemaForm，提交时固定携带加载到的 `workflowVersionId`；展示 node started/completed/failed 进度；取消按钮 abort fetch；string 结果使用 `<pre>` 的 `textContent` 语义，JSON 用 `JSON.stringify(value,null,2)`，不使用 `dangerouslySetInnerHTML`。RunHistoryPage 显示 mode、version/revision、status、startedAt、duration，点击加载 node runs，所有错误使用中文稳定码。

- [ ] **Step 4: 运行组件测试并确认通过**

Run: `corepack pnpm@10.34.5 --filter @agent-studio/web test --run src/features/agent src/features/runs`
Expected: PASS，覆盖 400 schema 错误、500 安全错误、取消、文本 XSS 字符串不解释、JSON 输出和旧版本绑定。

- [ ] **Step 5: 编写节点扩展与启动文档**

`docs/node-development.md` 必须给出完整 Echo 节点：Definition JSON Schema、Resolve、Execute、Register 和单元测试，并明确普通节点不改前端。README 必须包含架构 Mermaid、前置版本、Corepack 激活、`make db-up`、`make dev-api`、`make dev-web`、Mock 演示、OpenAI-compatible 环境变量、HTTP env Header、测试命令和首版边界。

- [ ] **Step 6: 写 Playwright 核心闭环测试**

先安装并锁定 `@playwright/test@1.62.1`，再创建配置和测试。

```ts
test('创建、测试、发布并运行 Agent', async ({ page }) => {
  await page.goto('/workflows')
  await page.getByRole('button', { name: '新建工作流' }).click()
  await page.getByTestId('node-start').click()
  await page.getByRole('button', { name: '添加字段' }).click()
  await page.getByLabel('字段标识').fill('topic')
  await page.getByLabel('字段标题').fill('主题')
  await page.getByLabel('字段类型').selectOption('text')
  await page.getByLabel('必填').check()
  await page.getByRole('button', { name: '添加节点' }).click()
  await page.getByRole('button', { name: '提示词模板' }).click()
  await page.getByLabel('模板').fill('回答：{{topic}}')
  await page.getByRole('button', { name: '添加节点' }).click()
  await page.getByRole('button', { name: 'LLM' }).click()
  // 通过 React Flow handle 的 data-port 属性连线 Start → Template → LLM → End。
  await connectPorts(page, ['start:topic', 'template:topic', 'template:text', 'llm:prompt', 'llm:text', 'end:result'])
  await page.getByRole('button', { name: '测试运行' }).click()
  await page.getByLabel('主题').fill('Agent')
  await page.getByRole('button', { name: '运行' }).click()
  await expect(page.getByText('Mock 回复：回答：Agent')).toBeVisible()
  await page.getByRole('button', { name: '发布' }).click()
  const agentURL = await page.getByRole('link', { name: '打开 Agent 页面' }).getAttribute('href')
  await page.goto(agentURL!)
  await page.getByLabel('主题').fill('Workflow')
  await page.getByRole('button', { name: '运行 Agent' }).click()
  await expect(page.getByText('Mock 回复：回答：Workflow')).toBeVisible()
})
```

`connectPorts` 必须实现为真实鼠标拖拽，不直接修改应用状态。第二个 E2E 使用两个 Page：Page A 保持已加载的 Agent V1；Page B 将模板改为 `V2：{{topic}}` 并再次发布；回到 Page A 提交，断言仍得到 V1 模板输出，再刷新 Page A 后断言得到 V2 输出。

- [ ] **Step 7: 完成 Makefile 质量门**

```make
TEST_DATABASE_URL ?= postgres://agent:agent@localhost:5432/agent_studio?sslmode=disable

verify: db-up
	cd apps/api && TEST_DATABASE_URL=$(TEST_DATABASE_URL) CGO_ENABLED=0 go test ./... -count=1
	cd apps/api && CGO_ENABLED=0 go vet ./...
	corepack pnpm@10.34.5 --filter @agent-studio/web generate:api
	git diff --exit-code -- apps/web/src/lib/api/generated.ts
	corepack pnpm@10.34.5 lint
	corepack pnpm@10.34.5 test
	corepack pnpm@10.34.5 build

test-e2e: db-up
	corepack pnpm@10.34.5 --filter @agent-studio/web exec playwright test
```

`.env.example` 定义所有变量但使用空密钥。Playwright `webServer` 数组分别启动 Mock API 和 Vite，并复用已有服务；测试前运行 migration。

- [ ] **Step 8: 运行新鲜完整回归**

Run: `corepack pnpm@10.34.5 --filter @agent-studio/web exec playwright install chromium`
Expected: Chromium 安装完成；已存在时不重复下载。

Run: `make verify`
Expected: Go test/vet、API 生成差异、Web typecheck/test/build 全部 exit 0。
Run: `make test-e2e`
Expected: 两个核心 E2E 全部 PASS。
Run: `git diff --check`
Expected: 无输出且 exit 0。

- [ ] **Step 9: 执行代码审查并修复发现**

先使用 `superpowers:requesting-code-review` 检查 `master...HEAD` 的规格符合性和代码质量；随后逐条验证审查意见，使用 `superpowers:receiving-code-review` 处理有争议或需修改的意见。修复后必须重新运行 Step 8，不能复用旧测试结果。

- [ ] **Step 10: 提交最终文档与回归修复**

```bash
git add README.md docs/node-development.md .env.example Makefile apps/web pnpm-lock.yaml
git commit -m "test: verify agent studio end to end"
```

## 实施完成判定

只有同时满足以下证据才能宣告完成：

1. `make verify` 最新一次运行 exit 0。
2. `make test-e2e` 最新一次运行两个核心用例全部通过。
3. `git diff --check` exit 0。
4. `git status --short` 为空。
5. 代码审查无未处理的 P0/P1/P2 问题。
6. README 的 Mock 启动流程由空数据库实际走通一次。
