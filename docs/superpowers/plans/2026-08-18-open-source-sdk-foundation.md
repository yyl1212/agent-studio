# Open Source Go SDK Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**目标：** 将现有内部节点协议升级为稳定、可公开导入的 Go SDK，完成七类官方节点迁移，并建立契约测试、兼容性约束和安全错误模型。

**架构：** 仓库使用根 Go Module `github.com/yyl1212/agent-studio`。公开协议位于 `sdk/go/agentnode`，测试工具位于 `sdk/go/agenttest`；运行时继续保留在 `apps/api/internal`，通过类型别名和薄适配层消费公开 SDK。公开包不得依赖数据库、HTTP 路由、DAG 编译器或 React 代码。

**技术栈：** Go 1.26.5、JSON Schema Draft 2020-12、`github.com/santhosh-tekuri/jsonschema/v6`、标准库 `log/slog`、现有 PostgreSQL/Chi 运行时。

---

## 执行门禁

本计划不能在编写计划时使用的 `codex/agent-studio` 分支上执行。

- [ ] 确认工作区干净：

  ```bash
  git status --short
  ```

  预期：无输出。

- [ ] 确认当前 MVP 与开放 SDK 规格已进入本地 `master`：

  ```bash
  git merge-base --is-ancestor f6508f4 master
  ```

  预期：退出码为 0。若失败，停止执行并先处理分支集成；不得绕过。

- [ ] 使用 `superpowers:using-git-worktrees` 从本地 `master` 创建隔离分支 `codex/open-source-sdk-foundation`。

- [ ] 所有 Go 构建、测试、静态检查命令显式设置 `CGO_ENABLED=0`。

## 全局验收约束

- `sdk/go/agentnode` 只依赖 Go 标准库。
- `sdk/go/agenttest` 可以依赖 JSON Schema 校验库，但不得依赖 `apps/api/internal`。
- 公开 SDK 不承诺进程隔离；`Capability` 在 v0.2 仅是声明和审计元数据，不是沙箱边界。
- 运行页面只能收到稳定的错误类别和错误码，不返回底层错误文本、密钥、请求头或完整配置。
- 现有工作流 JSON 和七类内置节点行为保持兼容。

## 文件地图

| 文件 | 操作 | 职责 |
|---|---|---|
| `go.mod`、`go.sum` | 新增/迁移 | 根 Go Module 与全仓依赖 |
| `apps/api/go.mod`、`apps/api/go.sum` | 删除 | 移除嵌套 Module |
| `Makefile` | 修改 | 从仓库根运行 Go 命令并固定 `CGO_ENABLED=0` |
| `sdk/go/agentnode/*.go` | 新增 | 稳定的节点协议、能力、错误和配置辅助函数 |
| `sdk/go/agenttest/*.go` | 新增 | 第三方节点契约测试工具 |
| `apps/api/internal/domain/node.go` | 修改 | 过渡性别名，保持内部调用兼容 |
| `apps/api/internal/nodes/registry.go` | 修改 | 实现公开 `Registrar` 并注册公开 `Node` |
| `apps/api/internal/nodes/adapter.go` | 新增 | 公开协议到运行时的规范化边界 |
| `apps/api/internal/nodes/builtin/*.go` | 修改 | 七类官方节点迁移到公开 SDK |
| `apps/api/internal/nodes/builtin/contract_test.go` | 新增 | 官方节点统一契约回归 |
| `apps/api/internal/engine/*.go` | 修改 | 节点执行错误带节点上下文传播 |
| `apps/api/internal/workflow/run_service.go` | 修改 | 安全结构化日志和 UI 错误映射 |
| `apps/api/cmd/server/main.go` | 修改 | 注入生产日志器 |
| `docs/sdk/api.md` | 新增 | v0.2 公开 API 文档 |
| `docs/sdk/compatibility.md` | 新增 | 版本与兼容性策略 |
| `docs/node-development.md`、`README.md` | 修改 | 指向公开 SDK，声明能力边界 |

## Task 1：迁移为根 Go Module

**文件：**

- 新增：`go.mod`
- 迁移：`go.sum`
- 删除：`apps/api/go.mod`
- 删除：`apps/api/go.sum`
- 修改：`Makefile`
- 修改：所有包含 `agentstudio.local/api/` 的 `.go` 文件

- [ ] 先证明根目录尚不能作为 Go Module 测试：

  ```bash
  CGO_ENABLED=0 go test ./...
  ```

  预期：出现“directory prefix . does not contain main module”一类错误。

- [ ] 在仓库根创建 `go.mod`，保留原依赖版本并使用公开模块路径：

  ```go
  module github.com/yyl1212/agent-studio

  go 1.26.0

  toolchain go1.26.5
  ```

  将 `apps/api/go.mod` 中全部 `require` 块原样接到该头部之后，并把 `apps/api/go.sum` 移到根目录。

- [ ] 删除 `apps/api/go.mod` 和 `apps/api/go.sum`，将所有内部导入前缀机械替换为：

  ```go
  import "github.com/yyl1212/agent-studio/apps/api/internal/domain"
  ```

  使用下面的检查确保旧模块路径清零：

  ```bash
  rg -n 'agentstudio\.local/api|apps/api/go\.mod|apps/api/go\.sum' --glob '!docs/**'
  ```

  预期：无输出。

- [ ] 修改 `Makefile`，Go 目标从仓库根执行；核心形式固定为：

  ```make
  test-api:
	CGO_ENABLED=0 go test ./apps/api/...

  vet-api:
	CGO_ENABLED=0 go vet ./apps/api/...

  dev-api:
	CGO_ENABLED=0 go run ./apps/api/cmd/server
  ```

- [ ] 整理依赖并执行全量回归：

  ```bash
  CGO_ENABLED=0 go mod tidy
  CGO_ENABLED=0 go test ./...
  CGO_ENABLED=0 go vet ./...
  make verify
  make test-e2e
  ```

  预期：全部通过，现有画布回归无变化。

- [ ] 提交根 Module 迁移：

  ```bash
  git add go.mod go.sum apps/api Makefile
  git commit -m "chore: publish repository go module"
  ```

## Task 2：以测试驱动建立 `agentnode` 公开协议

**文件：**

- 新增：`sdk/go/agentnode/node.go`
- 新增：`sdk/go/agentnode/definition.go`
- 新增：`sdk/go/agentnode/capability.go`
- 新增：`sdk/go/agentnode/errors.go`
- 新增：`sdk/go/agentnode/config.go`
- 新增：`sdk/go/agentnode/compatibility.go`
- 新增：`sdk/go/agentnode/node_test.go`
- 新增：`sdk/go/agentnode/errors_test.go`
- 新增：`sdk/go/agentnode/config_test.go`

- [ ] 先写 `node_test.go`，锁定 JSON 形状、枚举值和接口可实现性：

  ```go
  package agentnode_test

  import (
      "context"
      "encoding/json"
      "testing"

      "github.com/yyl1212/agent-studio/sdk/go/agentnode"
  )

  type echoNode struct{}

  func (echoNode) Definition() agentnode.Definition {
      return agentnode.Definition{
          Type: "example.echo", Version: "1.0.0", Title: "Echo",
          ConfigSchema: agentnode.MustSchema(`{"type":"object","additionalProperties":false}`),
          Inputs: []agentnode.Port{{Key: "text", Title: "Text", Type: agentnode.DataTypeString, Cardinality: agentnode.CardinalityOne}},
          Outputs: []agentnode.Port{{Key: "text", Title: "Text", Type: agentnode.DataTypeString, Cardinality: agentnode.CardinalityOne}},
      }
  }
  func (echoNode) Resolve(json.RawMessage) (agentnode.ResolvedPorts, error) {
      definition := echoNode{}.Definition()
      return agentnode.ResolvedPorts{Inputs: definition.Inputs, Outputs: definition.Outputs}, nil
  }
  func (echoNode) Execute(context.Context, agentnode.Request) (agentnode.Result, error) {
      return agentnode.Result{Outputs: map[string]any{"text": "hello"}}, nil
  }

  func TestNodeCanBeImplementedOutsideRuntime(t *testing.T) {
      var node agentnode.Node = echoNode{}
      encoded, err := json.Marshal(node.Definition())
      if err != nil {
          t.Fatal(err)
      }
      if string(encoded) == "{}" {
          t.Fatal("definition must have public JSON fields")
      }
  }
  ```

- [ ] 运行红灯测试：

  ```bash
  CGO_ENABLED=0 go test ./sdk/go/agentnode
  ```

  预期：公开包不存在或符号未定义。

- [ ] 实现公开协议，最终接口固定为：

  ```go
  package agentnode

  import (
      "context"
      "encoding/json"
  )

  const APIVersion = "agent-studio.dev/v1alpha1"
  const Version = "0.2.0"

  type Node interface {
      Definition() Definition
      Resolve(config json.RawMessage) (ResolvedPorts, error)
      Execute(ctx context.Context, request Request) (Result, error)
  }

  type Registrar interface {
      Register(node Node) error
  }

  type Request struct {
      Inputs   map[string][]any `json:"inputs"`
      RunInput map[string]any   `json:"runInput"`
      Config   json.RawMessage  `json:"config"`
  }

  type Result struct {
      Outputs     map[string]any `json:"outputs"`
      ActivePorts []string       `json:"activePorts,omitempty"`
  }
  ```

  `definition.go` 定义 `DataType`（`string`、`number`、`boolean`、`json`、`any`）、`Cardinality`（`one`、`single-active`）、`Port`、`ResolvedPorts` 和 `Definition`。为保持现有画布 JSON 和存量工作流兼容，端口字段不得改名：

  ```go
  type Port struct {
      Key         string      `json:"key"`
      Title       string      `json:"title"`
      Type        DataType    `json:"type"`
      Required    bool        `json:"required"`
      Cardinality Cardinality `json:"cardinality"`
  }

  type ResolvedPorts struct {
      Inputs  []Port `json:"inputs"`
      Outputs []Port `json:"outputs"`
  }

  type Definition struct {
      Type         string          `json:"type"`
      Version      string          `json:"version"`
      Title        string          `json:"title"`
      Description  string          `json:"description"`
      Category     string          `json:"category"`
      ConfigSchema json.RawMessage `json:"configSchema"`
      Inputs       []Port          `json:"inputs"`
      Outputs      []Port          `json:"outputs"`
      Capabilities []Capability    `json:"capabilities,omitempty"`
  }
  ```

  常量名固定为 `DataTypeString`、`DataTypeNumber`、`DataTypeBoolean`、`DataTypeJSON`、`DataTypeAny`、`CardinalityOne`、`CardinalitySingleActive`。`Definition` 必须保留现有 JSON 字段；新增能力字段为：

  ```go
  Capabilities []Capability `json:"capabilities,omitempty"`
  ```

- [ ] 在 `capability.go` 固定 v0.2 声明值：

  ```go
  package agentnode

  type Capability string

  const (
      CapabilityNetwork         Capability = "network"
      CapabilitySecrets         Capability = "secrets"
      CapabilityFilesystemRead  Capability = "filesystem-read"
      CapabilityFilesystemWrite Capability = "filesystem-write"
  )
  ```

- [ ] 先写 `errors_test.go`，覆盖 `errors.Is`/`errors.As`、五种分类、`context.Canceled` 映射和未知错误回退到 `internal`；再实现：

  ```go
  package agentnode

  type ErrorKind string

  const (
      ErrorKindConfig    ErrorKind = "config"
      ErrorKindInput     ErrorKind = "input"
      ErrorKindTemporary ErrorKind = "temporary"
      ErrorKindCanceled  ErrorKind = "canceled"
      ErrorKindInternal  ErrorKind = "internal"
  )

  type NodeError struct {
      Kind    ErrorKind
      Code    string
      Details map[string]any
      Err     error
  }

  func NewError(kind ErrorKind, code string, err error, details map[string]any) *NodeError
  func (e *NodeError) Error() string
  func (e *NodeError) Unwrap() error
  func KindOf(err error) ErrorKind
  ```

  `Error()` 只拼接 `Kind` 和 `Code`，不得拼接 `Err.Error()` 或 `Details`。`KindOf` 对 `context.Canceled` 返回 `canceled`，对 `context.DeadlineExceeded` 返回 `temporary`。

- [ ] 先写 `config_test.go`，覆盖未知字段、多段 JSON、空配置和类型错误；再实现严格解码函数：

  ```go
  package agentnode

  import "encoding/json"

  func DecodeConfig(raw json.RawMessage, target any) error
  func MustSchema(raw string) json.RawMessage
  ```

  `DecodeConfig` 将 nil、空白输入规范化为 `{}`，使用 `json.Decoder.DisallowUnknownFields()`，要求第二次 `Decode` 返回 `io.EOF`，错误包装为 `ErrorKindConfig`/`invalid_config`。`MustSchema` 仅验证静态字符串是合法 JSON；非法时 panic。

- [ ] 在 `compatibility.go` 提供纯函数，并为边界写表驱动测试：

  ```go
  package agentnode

  func SupportsAPIVersion(version string) bool {
      return version == APIVersion
  }
  ```

- [ ] 运行包级和全仓验证：

  ```bash
  CGO_ENABLED=0 go test ./sdk/go/agentnode -count=1
  CGO_ENABLED=0 go test ./...
  CGO_ENABLED=0 go vet ./...
  ```

- [ ] 提交公开协议：

  ```bash
  git add sdk/go/agentnode
  git commit -m "feat(sdk): define public node protocol"
  ```

## Task 3：建立运行时桥接层

**文件：**

- 修改：`apps/api/internal/domain/node.go`
- 修改：`apps/api/internal/nodes/registry.go`
- 新增：`apps/api/internal/nodes/adapter.go`
- 新增：`apps/api/internal/nodes/adapter_test.go`
- 修改：`apps/api/internal/nodes/registry_test.go`

- [ ] 在 `registry_test.go` 先加入外部 SDK 节点注册测试，断言 `Registry` 满足公开接口：

  ```go
  func TestRegistryImplementsPublicRegistrar(t *testing.T) {
      var registrar agentnode.Registrar = nodes.NewRegistry()
      if registrar == nil {
          t.Fatal("registrar must not be nil")
      }
  }
  ```

  再断言公开节点注册后可以被 `Get`、`Definitions` 和 `ValidateConfig` 使用。

- [ ] 运行红灯测试：

  ```bash
  CGO_ENABLED=0 go test ./apps/api/internal/nodes -count=1
  ```

  预期：`Registry.Register` 参数仍是内部类型，或缺少适配函数。

- [ ] 将 `domain/node.go` 改为过渡别名，避免复制协议：

  ```go
  package domain

  import "github.com/yyl1212/agent-studio/sdk/go/agentnode"

  type DataType = agentnode.DataType
  type PortCardinality = agentnode.Cardinality
  type PortDefinition = agentnode.Port
  type NodeRequest = agentnode.Request
  type NodeResult = agentnode.Result
  type ResolvedPorts = agentnode.ResolvedPorts
  type NodeDefinition = agentnode.Definition
  ```

  同文件的旧常量改为 `agentnode` 常量别名，保证现有工作流和测试无需批量改名。

- [ ] 将 Registry 的公开边界固定为：

  ```go
  type NodeType = agentnode.Node

  type registeredNode struct {
      node   agentnode.Node
      schema *jsonschema.Schema
  }

  type Registry struct {
      entries map[string]registeredNode
  }

  var _ agentnode.Registrar = (*Registry)(nil)

  func (r *Registry) Register(node agentnode.Node) error
  ```

- [ ] 在 `adapter.go` 集中处理 nil 切片、重复端口和活动端口规范化：

  ```go
  package nodes

  import "github.com/yyl1212/agent-studio/sdk/go/agentnode"

  func NormalizeDefinition(definition agentnode.Definition) (agentnode.Definition, error)
  func NormalizeResolvedPorts(ports agentnode.ResolvedPorts) (agentnode.ResolvedPorts, error)
  func NormalizeResult(result agentnode.Result, outputs []agentnode.Port) (agentnode.Result, error)
  func Adapt(node agentnode.Node) agentnode.Node
  ```

  `Adapt` 返回一个薄 wrapper，在 Definition、Resolve、Execute 的返回边界分别调用三个 Normalize 函数；Registry 只保存适配后的节点。规则：nil 输入/输出转为空数组；端口 Key 必须非空且同一方向唯一；`ActivePorts` 必须是已声明输出且去重；结果输出不能包含未声明键。

- [ ] 在 `adapter_test.go` 对每条规则写表驱动测试，并确保序列化后的空端口为 `[]` 而不是 `null`。

- [ ] 运行验证并提交：

  ```bash
  CGO_ENABLED=0 go test ./apps/api/internal/domain ./apps/api/internal/nodes -count=1
  CGO_ENABLED=0 go test ./...
  git add apps/api/internal/domain apps/api/internal/nodes
  git commit -m "refactor(runtime): consume public node sdk"
  ```

## Task 4：建立第三方节点契约测试包

**文件：**

- 新增：`sdk/go/agenttest/contract.go`
- 新增：`sdk/go/agenttest/execution.go`
- 新增：`sdk/go/agenttest/cancellation.go`
- 新增：`sdk/go/agenttest/contract_test.go`
- 新增：`sdk/go/agenttest/testdata/invalid-schemas.json`

- [ ] 先在 `contract_test.go` 定义故意违反规则的节点，逐项验证：空类型、非法版本、重复端口、未声明输出、不可 JSON 编码输出、超大输出、忽略取消信号都必须被拒绝。将单项校验实现为返回 `error` 的包内纯函数并直接测试；不要伪造 `testing.TB`，因为该接口含包外不可实现的方法。公开 `Run` 再把这些错误转换为 `t.Errorf`。

- [ ] 运行红灯测试：

  ```bash
  CGO_ENABLED=0 go test ./sdk/go/agenttest -count=1
  ```

- [ ] 实现面向第三方作者的最小 API：

  ```go
  package agenttest

  import (
      "encoding/json"
      "testing"
      "time"

      "github.com/yyl1212/agent-studio/sdk/go/agentnode"
  )

  type ExecutionCase struct {
      Name          string
      Request       agentnode.Request
      WantOutputs   map[string]any
      WantErrorKind *agentnode.ErrorKind
  }

  type CancellationCase struct {
      Request     agentnode.Request
      CancelAfter time.Duration
      MaxWait     time.Duration
  }

  type Contract struct {
      Node           agentnode.Node
      ValidConfigs   []json.RawMessage
      InvalidConfigs []json.RawMessage
      Executions     []ExecutionCase
      Cancellation   *CancellationCase
      MaxOutputBytes int
  }

  func Run(t *testing.T, contract Contract)
  ```

- [ ] `Run` 固定检查以下内容：

  1. `Type` 匹配 `^[a-z][a-z0-9]*(\.[a-z][a-z0-9-]*)*$`，允许现有 `start` 等内置类型；第三方脚手架另行强制命名空间。`Version` 匹配 `^[0-9]+(?:\.[0-9]+){0,2}$`，兼容现有节点版本 `1` 与扩展版本 `1.0.0`。
  2. 端口、能力和活动端口无重复，Schema 可按 Draft 2020-12 编译。
  3. `ValidConfigs` 可 Resolve，`InvalidConfigs` 必须返回 `config` 类错误。
  4. 每个执行结果与期望深比较、可 JSON 编码，且默认不超过 1 MiB。
  5. 提供 `Cancellation` 时，Execute 启动后默认 10 ms 取消 context，并要求默认 250 ms 内返回 `context.Canceled` 或 `canceled` 类 NodeError。

  契约失败日志可在本地测试进程中展开 `NodeError.Err`，让 `agent-studio node test` 显示完整开发原因；该内容不得进入 Runtime Event、数据库或 Agent HTTP 响应。

- [ ] 使用现有 JSON Schema 依赖完成实现，随后运行：

  ```bash
  CGO_ENABLED=0 go test ./sdk/go/... -count=1
  CGO_ENABLED=0 go vet ./sdk/go/...
  ```

- [ ] 提交契约测试包：

  ```bash
  git add sdk/go/agenttest go.mod go.sum
  git commit -m "feat(sdk): add node contract test kit"
  ```

## Task 5：迁移 Start、Template、Condition、End

**文件：**

- 修改：`apps/api/internal/nodes/builtin/start.go`
- 修改：`apps/api/internal/nodes/builtin/template.go`
- 修改：`apps/api/internal/nodes/builtin/condition.go`
- 修改：`apps/api/internal/nodes/builtin/end.go`
- 修改：`apps/api/internal/nodes/builtin/register.go`
- 修改：对应现有 `*_test.go`
- 新增：`apps/api/internal/nodes/builtin/contract_test.go`

- [ ] 先创建统一契约测试，注册四类核心节点并调用 `agenttest.Run`。至少为每个节点提供一个合法配置、一个非法配置和一个确定性的执行样例。

- [ ] 运行红灯测试，确认内置节点尚未直接实现公开类型：

  ```bash
  CGO_ENABLED=0 go test ./apps/api/internal/nodes/builtin -run Contract -count=1
  ```

- [ ] 将四类节点的方法签名直接改用 `agentnode`：

  ```go
  func (n *startNode) Definition() agentnode.Definition
  func (n *startNode) Resolve(config json.RawMessage) (agentnode.ResolvedPorts, error)
  func (n *startNode) Execute(ctx context.Context, request agentnode.Request) (agentnode.Result, error)
  ```

  Template、Condition、End 使用同样签名；不得通过内部 wrapper 假装实现公开接口。

- [ ] 保留现有 sentinel error 作为 `NodeError.Err`，同时分类：配置解析为 `config/invalid_config`，上游端口缺失或基数错误为 `input/invalid_input`，context 取消为 `canceled/run_canceled`，未知执行错误为 `internal/execution_failed`。既有 `errors.Is` 测试必须继续通过。

- [ ] 将注册入口改为：

  ```go
  func RegisterCore(registrar agentnode.Registrar) error
  ```

  注册顺序固定为 Start、Template、Condition、End，首次错误立即返回。

- [ ] 保留并运行所有既有行为测试，再运行契约和全仓测试：

  ```bash
  CGO_ENABLED=0 go test ./apps/api/internal/nodes/builtin -count=1
  CGO_ENABLED=0 go test ./...
  ```

- [ ] 提交核心节点迁移：

  ```bash
  git add apps/api/internal/nodes/builtin
  git commit -m "refactor(nodes): migrate core nodes to public sdk"
  ```

## Task 6：迁移 LLM、HTTP、Code 并声明能力

**文件：**

- 修改：`apps/api/internal/nodes/builtin/llm.go`
- 修改：`apps/api/internal/nodes/builtin/http.go`
- 修改：`apps/api/internal/nodes/builtin/code.go`
- 修改：相关 provider、helper、注册文件和测试
- 修改：`apps/api/internal/nodes/builtin/contract_test.go`

- [ ] 扩展统一契约测试：LLM 使用 fake provider，HTTP 使用 `httptest.Server`，Code 使用确定性 Starlark；加入取消、超时和密钥不进入输出的断言。

- [ ] 运行契约测试，记录迁移前红灯：

  ```bash
  CGO_ENABLED=0 go test ./apps/api/internal/nodes/builtin -run 'Contract/(llm|http|code)' -count=1
  ```

- [ ] 将三类节点直接迁移到 `agentnode` 类型，并在 Definition 中精确声明：

  ```go
  // LLM
  Capabilities: []agentnode.Capability{
      agentnode.CapabilityNetwork,
      agentnode.CapabilitySecrets,
  }

  // HTTP
  Capabilities: []agentnode.Capability{
      agentnode.CapabilityNetwork,
      agentnode.CapabilitySecrets,
  }

  // Code
  Capabilities: []agentnode.Capability{}
  ```

  Code 节点当前只运行内嵌 Starlark，因此不得声明宿主文件系统能力。

- [ ] 将所有配置解析错误改为 `config/invalid_config`，输入缺失改为 `input/missing_input`，主动取消改为 `canceled/run_canceled`，上游超时改为 `temporary/upstream_timeout`；未知底层错误包装为 `internal/execution_failed`。

- [ ] 将注册函数参数改为公开 `agentnode.Registrar`，保持原注册顺序和 provider 注入方式。

- [ ] 审查七类官方节点的 ConfigSchema：密钥只能配置环境变量名或引用标识，Schema 中不得出现可填写明文 API key、token、password、cookie 或 authorization 的字段。保留现有环境变量注入方式，并为拒绝明文密钥字段增加回归测试。

- [ ] 回归七类节点及 API：

  ```bash
  CGO_ENABLED=0 go test ./apps/api/internal/nodes/builtin -count=1
  CGO_ENABLED=0 go test ./apps/api/internal/httpapi/... -count=1
  CGO_ENABLED=0 go test ./... -count=1
  ```

- [ ] 提交集成节点迁移：

  ```bash
  git add apps/api/internal/nodes/builtin
  git commit -m "refactor(nodes): migrate integration nodes to public sdk"
  ```

## Task 7：建立安全错误传播与结构化日志

**文件：**

- 修改：`apps/api/internal/engine/engine.go`
- 修改：`apps/api/internal/engine/engine_test.go`
- 修改：`apps/api/internal/domain/errors.go`
- 修改：`apps/api/internal/workflow/run_service.go`
- 修改：`apps/api/internal/workflow/run_service_test.go`
- 修改：`apps/api/internal/workflow/redact.go`
- 修改：`apps/api/internal/workflow/redact_test.go`
- 修改：`apps/api/cmd/server/main.go`
- 修改：`contracts/openapi.yaml`
- 生成：`apps/web/src/lib/api/generated.ts`

- [ ] 先写测试：节点返回包含 `top-secret` 的底层错误和 Details 后，API 响应不得包含该字符串；JSON 日志必须包含 `run_id`、`node_id`、`node_type`、`error_kind`、`error_code`，且同样不得包含密钥。

- [ ] 运行红灯测试：

  ```bash
  CGO_ENABLED=0 go test ./apps/api/internal/workflow ./apps/api/internal/engine -run 'Error|Redact' -count=1
  ```

- [ ] 在 engine 增加携带节点上下文、支持 `Unwrap` 的错误：

  ```go
  type NodeExecutionError struct {
      NodeID   string
      NodeType string
      Err      error
  }

  func (e *NodeExecutionError) Error() string {
      return "node execution failed"
  }

  func (e *NodeExecutionError) Unwrap() error {
      return e.Err
  }
  ```

- [ ] 为 `RunService` 增加不破坏现有调用的选项式注入：

  ```go
  type RunOption func(*RunService)

  func WithLogger(logger *slog.Logger) RunOption {
      return func(service *RunService) {
          service.logger = logger
      }
  }

  func NewRunService(store Store, compiler Compiler, runtime Engine, options ...RunOption) *RunService
  ```

  默认日志器写入 `io.Discard`；server main 显式注入 JSON Handler 日志器。

- [ ] 为 `domain.PublicError` 新增字段：

  ```go
  Kind agentnode.ErrorKind `json:"kind,omitempty"`
  ```

  保持原有 Code、Message、NodeID 字段不变。公开 Code 继续使用 `RUN_FAILED`、`NODE_EXECUTION_FAILED`、`RUN_CANCELLED`，只新增 Kind；SDK 的 `run_canceled` 等细粒度 code 记录在结构化日志中。配置、输入、临时、取消和内部失败的 Message 均从固定中文文案表取值。

- [ ] 在 OpenAPI 的 `PublicError` schema 增加可选 `kind`，enum 与五个 `ErrorKind` 完全一致；随后运行前端 `generate:api` 并提交生成的 TypeScript 类型。

- [ ] `redact.go` 递归处理 map、slice 和字符串键。键名大小写归一后包含 `authorization`、`cookie`、`token`、`secret`、`password`、`api_key` 时值替换为 `[REDACTED]`。Runtime 日志不得记录 `Config`、完整 Inputs 或底层 `Err.Error()`；使用 `error_causes` 记录 unwrap 链上的 Go 类型名，并记录脱敏后的 `NodeError.Details`，既能定位底层类别又不泄漏文本。完整底层文本只允许出现在本地 `node test`。

- [ ] UI 错误映射固定为：

  ```json
  {
    "code": "NODE_EXECUTION_FAILED",
    "kind": "internal",
    "message": "节点执行失败"
  }
  ```

  公开 `code` 保持现有 API 兼容；`kind` 从 `NodeError` 分类映射，`message` 只能从服务端固定文案表选择。SDK 的原始 code、Details 和 cause 类型只进入脱敏结构化日志。

- [ ] 执行安全回归与全量测试：

  ```bash
  CGO_ENABLED=0 go test ./apps/api/internal/workflow ./apps/api/internal/engine -count=1
  CGO_ENABLED=0 go test ./... -count=1
  corepack pnpm@10.34.5 --filter @agent-studio/web generate:api
  make verify
  ```

- [ ] 提交错误模型：

  ```bash
  git add apps/api/internal/domain/errors.go apps/api/internal/workflow apps/api/internal/engine apps/api/cmd/server contracts/openapi.yaml apps/web/src/lib/api/generated.ts
  git commit -m "feat(runtime): add safe structured node errors"
  ```

## Task 8：冻结 v0.2 API 文档和兼容策略

**文件：**

- 新增：`docs/sdk/api.md`
- 新增：`docs/sdk/compatibility.md`
- 修改：`docs/node-development.md`
- 修改：`README.md`

- [ ] 编写 `docs/sdk/api.md`，按以下固定顺序说明并给出可编译示例：安装路径、Node 生命周期、Definition、动态端口、Request/Result、Registrar、错误分类、Capability、并发与 context 取消。示例导入必须是：

  ```go
  import "github.com/yyl1212/agent-studio/sdk/go/agentnode"
  ```

- [ ] 编写 `docs/sdk/compatibility.md`，明确：

  - v0.2 使用 `agent-studio.dev/v1alpha1`，相同 API Version 内新增可选 JSON 字段是兼容变更。
  - 删除字段、修改枚举语义、改变 Execute 生命周期必须提升 API Version。
  - Go SDK 在 v1.0 前允许 minor 版本出现编译期变更，但必须提供迁移说明。
  - manifest 与 Go SDK 分别版本化，CLI 必须拒绝未知 manifest API Version。
  - Capability 在 v0.2 仅用于展示、审计和未来调度，不提供隔离保证。

- [ ] 更新 `docs/node-development.md`，移除仅引用 `apps/api/internal` 的开发路径，保留“内置节点维护”章节并指向公开 SDK。

- [ ] 在中英文 README 的快速开始附近增加 SDK 入口、兼容性声明和安全边界链接，不复制整篇 API 文档。

- [ ] 校验文档中的 Go 示例可以编译。将示例抽到临时测试文件的命令固定为：

  ```bash
  CGO_ENABLED=0 go test ./sdk/go/... -count=1
  rg -n 'agentstudio\.local/api|apps/api/internal' docs/sdk README.md
  ```

  第二条仅允许兼容性文档中解释“不得导入 internal”的语句命中；代码块不得命中。

- [ ] 提交文档：

  ```bash
  git add docs/sdk docs/node-development.md README.md
  git commit -m "docs(sdk): publish api and compatibility contract"
  ```

## Task 9：最终审查与回归门禁

**文件：**

- 仅在发现问题时修改本计划涉及的文件

- [ ] 运行格式化并确认没有生成差异遗漏：

  ```bash
  CGO_ENABLED=0 go fmt ./...
  git diff --check
  git status --short
  ```

- [ ] 运行完整验证：

  ```bash
  CGO_ENABLED=0 go test ./... -count=1
  CGO_ENABLED=0 go vet ./...
  make verify
  make test-e2e
  ```

- [ ] 使用 `superpowers:requesting-code-review` 审查公开 API、兼容性、错误泄漏、context 取消和现有工作流兼容性。

- [ ] 若审查提出问题，使用 `superpowers:receiving-code-review` 逐条验证后修复；重复上一步全部命令。P0、P1、P2 问题必须清零。

- [ ] 确认提交历史只包含有意变更：

  ```bash
  git log --oneline master..HEAD
  git diff --stat master...HEAD
  git status --short
  ```

  预期：工作区干净，提交按 Task 1–8 排列。

## 完成标准

- 外部 Go 包只导入 `sdk/go/agentnode` 即可定义和注册节点。
- 七类官方节点全部通过 `agenttest.Run` 和既有行为测试。
- 根目录 `CGO_ENABLED=0 go test ./...`、`go vet`、前后端回归和 Playwright E2E 全部通过。
- API 响应和结构化日志均不泄漏测试密钥。
- 文档明确 v0.2 的兼容承诺和 Capability 非沙箱边界。
