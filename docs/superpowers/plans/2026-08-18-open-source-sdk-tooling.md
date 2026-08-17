# Open Source SDK Tooling and Examples Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**目标：** 提供可安装的 `agent-studio` CLI、严格的节点 manifest、确定性注册代码生成器、三类示例节点和中英文快速开始，使开发者可在 30 分钟内把一个新节点显示在画布上。

**架构：** CLI 位于 `cmd/agent-studio`，可测试逻辑位于 `internal/cli`。节点包通过仓库根 `agent-studio.nodes.yaml` 声明，由生成器输出唯一的 `apps/api/internal/generated/nodes_gen.go`；server 只调用生成注册入口。脚手架和示例节点只依赖公开 SDK，不依赖运行时 `internal` 包。

**技术栈：** Go 1.26.5、`go.yaml.in/yaml/v3` v3.0.4、`golang.org/x/mod` v0.38.0、现有 React Flow/Vite/Playwright、Go 标准库 `os/exec` 与 `text/template`。

---

## 执行门禁

- [ ] 确认《Open Source Go SDK Foundation Implementation Plan》已完整执行并进入本地 `master`：

  ```bash
  test -f sdk/go/agentnode/node.go
  test -f sdk/go/agenttest/contract.go
  test ! -f apps/api/go.mod
  CGO_ENABLED=0 go test ./... -count=1
  ```

- [ ] 使用 `superpowers:using-git-worktrees` 从包含 SDK 基础的本地 `master` 创建 `codex/open-source-sdk-tooling`。

- [ ] 所有子进程均直接传参数，不通过 shell 拼接；所有 Go 命令设置 `CGO_ENABLED=0`。

## 全局验收约束

- `agent-studio generate` 不联网下载依赖，不执行节点包代码，不覆盖 manifest 之外的手写文件。
- manifest 未知字段、未知 API Version、重复包、非法导入路径立即失败。
- 生成结果按导入路径排序，同一输入字节级稳定；写文件使用同目录临时文件和原子 rename。
- `node init` 不覆盖非空目录；目标类型和包名必须通过校验。
- Echo 是默认开发态可见的扩展节点；Retriever 和 Webhook 是独立示例，不默认连接真实外部服务。

## 文件地图

| 文件 | 操作 | 职责 |
|---|---|---|
| `cmd/agent-studio/main.go` | 新增 | CLI 进程入口 |
| `internal/cli/*.go` | 新增 | 命令解析、doctor、generate、node 子命令 |
| `internal/nodemanifest/*.go` | 新增 | manifest 严格解析与验证 |
| `internal/nodegen/*.go` | 新增 | 确定性注册代码生成 |
| `internal/scaffold/*.go` | 新增 | 节点项目脚手架模板 |
| `internal/generatedtest/*.go` | 新增 | 生成代码的端到端编译测试 |
| `agent-studio.nodes.yaml` | 新增 | 默认节点包清单 |
| `apps/api/internal/generated/nodes_gen.go` | 新增（生成） | server 的扩展节点注册入口 |
| `apps/api/cmd/server/main.go` | 修改 | 调用生成注册入口 |
| `examples/nodes/echo/*` | 新增 | 最小入门节点 |
| `examples/nodes/retriever/*` | 新增 | 本地确定性检索节点 |
| `examples/nodes/webhook/*` | 新增 | 安全外呼示例节点 |
| `apps/web/e2e/sdk-node.spec.ts` | 新增 | 扩展节点出现在画布的回归 |
| `Makefile` | 修改 | CLI、生成检查、SDK E2E 目标 |
| `docs/sdk/quickstart.md` | 新增 | 中文 30 分钟快速开始 |
| `docs/sdk/quickstart.en.md` | 新增 | 英文快速开始 |
| `docs/sdk/debugging.md` | 新增 | 调试和常见错误 |
| `README.md` | 修改 | 公开 SDK 入口与命令 |

## Task 1：建立可测试 CLI 外壳

**文件：**

- 新增：`cmd/agent-studio/main.go`
- 新增：`internal/cli/app.go`
- 新增：`internal/cli/app_test.go`

- [ ] 先写表驱动测试，覆盖空参数、`help`、`version`、未知命令和标准输出/错误输出分流：

  ```go
  func TestRunVersion(t *testing.T) {
      var stdout bytes.Buffer
      var stderr bytes.Buffer
      code := cli.Run(context.Background(), []string{"version"}, &stdout, &stderr)
      if code != 0 {
          t.Fatalf("code = %d, stderr = %s", code, stderr.String())
      }
      if got, want := stdout.String(), "agent-studio 0.2.0 (agent-studio.dev/v1alpha1)\n"; got != want {
          t.Fatalf("stdout = %q, want %q", got, want)
      }
  }
  ```

- [ ] 运行红灯测试：

  ```bash
  CGO_ENABLED=0 go test ./internal/cli -run 'Run|Version' -count=1
  ```

- [ ] 实现稳定入口：

  ```go
  package cli

  import (
      "context"
      "io"
  )

  func Run(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) int
  ```

  顶级命令固定为 `version`、`doctor`、`generate`、`node init`、`node test`。解析错误退出码 2，执行失败退出码 1，成功退出码 0。

- [ ] `main.go` 只负责信号 context 和退出码：

  ```go
  package main

  import (
      "context"
      "os"
      "os/signal"
      "syscall"

      "github.com/yyl1212/agent-studio/internal/cli"
  )

  func main() {
      ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
      defer stop()
      os.Exit(cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr))
  }
  ```

- [ ] 验证并提交：

  ```bash
  CGO_ENABLED=0 go test ./internal/cli ./cmd/agent-studio -count=1
  CGO_ENABLED=0 go run ./cmd/agent-studio version
  git add cmd/agent-studio internal/cli
  git commit -m "feat(cli): add agent studio command shell"
  ```

## Task 2：实现无副作用的环境诊断

**文件：**

- 新增：`internal/cli/doctor.go`
- 新增：`internal/cli/doctor_test.go`

- [ ] 先写 fake probes 测试，覆盖 Go 过旧、Node 过旧、缺少 corepack、Docker daemon 不可用、Compose 不可用和端口占用。测试不得读取开发机真实环境。

- [ ] 运行红灯测试：

  ```bash
  CGO_ENABLED=0 go test ./internal/cli -run Doctor -count=1
  ```

- [ ] 引入可注入依赖：

  ```go
  type DoctorDeps struct {
      LookPath    func(file string) (string, error)
      Command     func(ctx context.Context, name string, args ...string) ([]byte, error)
      Listen      func(network string, address string) (net.Listener, error)
      ReadFile    func(name string) ([]byte, error)
      WorkingDir  func() (string, error)
  }

  type CheckResult struct {
      Name    string
      Status  string
      Detail  string
  }

  func RunDoctor(ctx context.Context, deps DoctorDeps) []CheckResult
  ```

- [ ] 检查项和最低版本固定为：Go 1.26、Node 24、corepack、Docker daemon、Docker Compose v2，以及本地端口 5432、8080、5173 是否可绑定。端口检查成功后立即关闭 listener，不启动服务。

- [ ] 增加两个项目级只读检查：若当前目录存在 `agent-studio.nodes.yaml`，严格解析并确认其 API Version 受当前 SDK 支持；执行 `docker compose ps --status running db`，已运行时再执行 `docker compose exec -T db pg_isready -U agent -d agent_studio`。数据库未启动是 `warn`，版本不兼容是 `fail`；doctor 不自动启动容器或修改 manifest。

- [ ] 命令输出使用 `[ok]`、`[warn]`、`[fail]`；缺少必备项时退出码 1，仅端口被占用为 `warn` 且提示可能已有开发服务。

- [ ] 验证并提交：

  ```bash
  CGO_ENABLED=0 go test ./internal/cli -run Doctor -count=1
  CGO_ENABLED=0 go run ./cmd/agent-studio doctor
  git add internal/cli
  git commit -m "feat(cli): add development environment doctor"
  ```

## Task 3：实现严格节点 manifest

**文件：**

- 修改：`go.mod`
- 修改：`go.sum`
- 新增：`internal/nodemanifest/manifest.go`
- 新增：`internal/nodemanifest/manifest_test.go`
- 新增：`internal/nodemanifest/testdata/*.yaml`

- [ ] 添加并固定解析依赖：

  ```bash
  CGO_ENABLED=0 go get go.yaml.in/yaml/v3@v3.0.4
  CGO_ENABLED=0 go get golang.org/x/mod@v0.38.0
  ```

- [ ] 先写测试，覆盖合法文件、未知顶级字段、未知节点字段、空包、非法导入路径、重复包、错误 API Version、空节点列表和 YAML 多文档输入。

- [ ] 运行红灯测试：

  ```bash
  CGO_ENABLED=0 go test ./internal/nodemanifest -count=1
  ```

- [ ] 实现 manifest 类型和入口：

  ```go
  package nodemanifest

  const APIVersion = agentnode.APIVersion

  type Manifest struct {
      APIVersion string        `yaml:"apiVersion"`
      Nodes      []NodePackage `yaml:"nodes"`
  }

  type NodePackage struct {
      ImportPath string `yaml:"package"`
  }

  func Load(path string) (Manifest, error)
  func Parse(data []byte) (Manifest, error)
  func Validate(manifest Manifest) error
  ```

  `Parse` 使用 `yaml.Decoder.KnownFields(true)`，解码第二次必须是 `io.EOF`。路径调用 `module.CheckImportPath`，验证后按原顺序返回；排序由生成器负责。

- [ ] 错误文本包含 manifest 文件、字段路径和原因，但不得打印文件完整内容。

- [ ] 运行并提交：

  ```bash
  CGO_ENABLED=0 go test ./internal/nodemanifest -count=1
  CGO_ENABLED=0 go mod tidy
  git add go.mod go.sum internal/nodemanifest
  git commit -m "feat(cli): parse strict node manifests"
  ```

## Task 4：生成确定性注册代码

**文件：**

- 新增：`internal/nodegen/generate.go`
- 新增：`internal/nodegen/template.go`
- 新增：`internal/nodegen/generate_test.go`
- 新增：`internal/nodegen/testdata/nodes_gen.golden`
- 修改：`internal/cli/app.go`
- 新增：`internal/cli/generate.go`
- 修改：`internal/cli/app_test.go`

- [ ] 先写测试，覆盖空清单、乱序包、重复包、`go list` 失败、输出不变不改 mtime、输出变化原子替换，以及目标目录不存在时创建目录。

- [ ] 运行红灯测试：

  ```bash
  CGO_ENABLED=0 go test ./internal/nodegen ./internal/cli -run Generate -count=1
  ```

- [ ] 实现核心 API：

  ```go
  package nodegen

  import (
      "context"

      "github.com/yyl1212/agent-studio/internal/nodemanifest"
  )

  type PackageChecker func(ctx context.Context, importPath string) error

  func Generate(
      ctx context.Context,
      manifest nodemanifest.Manifest,
      outputPath string,
      checker PackageChecker,
  ) (changed bool, err error)

  func GoListChecker(moduleDir string) PackageChecker
  ```

- [ ] `GoListChecker` 直接运行：

  ```text
  go list -e=false <import-path>
  ```

  子进程环境显式添加 `CGO_ENABLED=0`、`GOPROXY=off`、`GOSUMDB=off`，因此缺少依赖时给出安装提示而不是隐式联网下载。

- [ ] 模板生成的公开函数固定为：

  ```go
  // Code generated by agent-studio generate; DO NOT EDIT.

  package generated

  import (
      "github.com/yyl1212/agent-studio/sdk/go/agentnode"
      nodepkg0 "github.com/yyl1212/agent-studio/examples/nodes/echo"
  )

  func Register(registrar agentnode.Registrar) error {
      if err := nodepkg0.Register(registrar); err != nil {
          return err
      }
      return nil
  }
  ```

  包按完整导入路径字典序排列，别名从 `nodepkg0` 递增；空 manifest 仍生成只返回 nil 的函数。

- [ ] 写入流程：渲染到内存、`go/format.Source`、比较现有字节；仅变化时在目标目录 `os.CreateTemp`、`Sync`、`Chmod(0644)`、`Rename`。失败后移除临时文件，原目标保持不变。

- [ ] 接入命令：

  ```text
  agent-studio generate --manifest agent-studio.nodes.yaml --output apps/api/internal/generated/nodes_gen.go
  ```

  两个参数均有上述默认值，未知 flag 退出码 2。

- [ ] 验证并提交：

  ```bash
  CGO_ENABLED=0 go test ./internal/nodegen ./internal/cli -run Generate -count=1
  git add internal/nodegen internal/cli
  git commit -m "feat(cli): generate deterministic node registration"
  ```

## Task 5：接入 Echo 默认扩展节点

**文件：**

- 新增：`examples/nodes/echo/node.go`
- 新增：`examples/nodes/echo/register.go`
- 新增：`examples/nodes/echo/node_test.go`
- 新增：`examples/nodes/echo/README.md`
- 新增：`agent-studio.nodes.yaml`
- 生成：`apps/api/internal/generated/nodes_gen.go`
- 新增：`apps/api/internal/generated/nodes_gen_test.go`
- 修改：`apps/api/cmd/server/main.go`
- 修改：`Makefile`

- [ ] 先为 Echo 写 `agenttest.Run` 契约：合法配置为空对象，输入 `text=hello` 输出同值；缺少输入返回 `input/missing_input`；取消的 context 返回 `context.Canceled`。

- [ ] 运行红灯测试：

  ```bash
  CGO_ENABLED=0 go test ./examples/nodes/echo -count=1
  ```

- [ ] 实现无配置 Echo 节点，Definition 固定：类型 `example.echo`、版本 `1.0.0`、分类 `Examples`，一个 string 输入和同名输出，无 Capability。

- [ ] 注册入口只依赖公开 SDK：

  ```go
  package echo

  import "github.com/yyl1212/agent-studio/sdk/go/agentnode"

  func Register(registrar agentnode.Registrar) error {
      return registrar.Register(Node{})
  }
  ```

- [ ] 创建默认 manifest：

  ```yaml
  apiVersion: agent-studio.dev/v1alpha1
  nodes:
    - package: github.com/yyl1212/agent-studio/examples/nodes/echo
  ```

- [ ] 执行生成器，新增测试断言 `generated.Register` 后 Registry 能找到 `example.echo`：

  ```bash
  CGO_ENABLED=0 go run ./cmd/agent-studio generate
  CGO_ENABLED=0 go test ./apps/api/internal/generated -count=1
  ```

- [ ] server 在内置节点注册完成后调用 `generated.Register(registry)`，失败时启动直接退出；不得静默跳过。

- [ ] Makefile 增加：

  ```make
  generate-nodes:
	CGO_ENABLED=0 go run ./cmd/agent-studio generate

  check-generated: generate-nodes
	git diff --exit-code -- apps/api/internal/generated/nodes_gen.go
  ```

- [ ] 验证并提交：

  ```bash
  CGO_ENABLED=0 go test ./examples/nodes/echo ./apps/api/internal/generated ./apps/api/cmd/server -count=1
  make check-generated
  git add examples/nodes/echo agent-studio.nodes.yaml apps/api/internal/generated apps/api/cmd/server Makefile
  git commit -m "feat(examples): register echo node from manifest"
  ```

## Task 6：实现安全节点脚手架

**文件：**

- 新增：`internal/scaffold/scaffold.go`
- 新增：`internal/scaffold/names.go`
- 新增：`internal/scaffold/templates/node.go.tmpl`
- 新增：`internal/scaffold/templates/register.go.tmpl`
- 新增：`internal/scaffold/templates/node_test.go.tmpl`
- 新增：`internal/scaffold/templates/README.md.tmpl`
- 新增：`internal/scaffold/scaffold_test.go`
- 新增：`internal/scaffold/testdata/basic.golden/*`
- 新增：`internal/cli/node_init.go`
- 修改：`internal/cli/app.go`
- 修改：`internal/cli/app_test.go`

- [ ] 先写临时 Module 测试，断言四个文件与 golden 完全一致，生成包能 `go test`，manifest 被原子追加；再覆盖非空目录、非法类型、重复注册和 `--register=false`。

- [ ] 运行红灯测试：

  ```bash
  CGO_ENABLED=0 go test ./internal/scaffold ./internal/cli -run 'Scaffold|NodeInit' -count=1
  ```

- [ ] 实现参数和返回值：

  ```go
  package scaffold

  type Options struct {
      ModuleDir   string
      TargetDir   string
      NodeType    string
      Register    bool
      Manifest    string
  }

  type Result struct {
      PackageName string
      ImportPath  string
      Files       []string
  }

  func Create(options Options) (Result, error)
  ```

- [ ] 使用 `modfile.Parse` 从根 `go.mod` 获取模块路径。节点类型必须匹配契约测试中的 type 正则且至少包含一个点，确保第三方类型有命名空间；Go 包名由目标目录 basename 生成，只允许字母、数字和下划线，且不能是 Go 关键字。

- [ ] 目标目录不存在时创建；已存在且包含任何目录项时失败。所有模板先渲染到内存并 `go/format`，全部成功后才落盘；中途失败时只清理本次创建的新目录。

- [ ] CLI 语法固定为：

  ```text
  agent-studio node init <target-dir> --type <namespaced-type> [--register=false]
  ```

  `--type` 必填，默认更新仓库根 manifest，输出后续两条命令：`agent-studio generate` 和 `go test <import-path>`。

- [ ] 验证并提交：

  ```bash
  CGO_ENABLED=0 go test ./internal/scaffold ./internal/cli -count=1
  git add internal/scaffold internal/cli
  git commit -m "feat(cli): scaffold public sdk nodes"
  ```

## Task 7：实现 `node test` 命令

**文件：**

- 新增：`internal/cli/node_test.go`
- 新增：`internal/cli/node_test_test.go`
- 修改：`internal/cli/app.go`

- [ ] 先写注入 runner 的测试，覆盖模块导入路径、`./relative/path`、缺少参数、多个参数、以 `-` 开头、空白字符、`..` 越界和 context 取消。

- [ ] 运行红灯测试：

  ```bash
  CGO_ENABLED=0 go test ./internal/cli -run NodeTest -count=1
  ```

- [ ] 实现不经过 shell 的 runner：

  ```go
  type CommandRunner interface {
      Run(ctx context.Context, dir string, env []string, name string, args ...string) error
  }

  func RunNodeTest(ctx context.Context, moduleDir string, packageArg string, runner CommandRunner) error
  ```

  完整导入路径用 `module.CheckImportPath` 校验；相对路径只允许以 `./` 开头、`filepath.Clean` 后仍位于 moduleDir、且不含空白。最终调用固定为 `go test -count=1 <validated-package>`，环境增加 `CGO_ENABLED=0`。

- [ ] 将命令输出直接流式连接到 CLI 的 stdout/stderr，不缓存测试密钥或大段日志。

- [ ] 验证并提交：

  ```bash
  CGO_ENABLED=0 go test ./internal/cli -run NodeTest -count=1
  CGO_ENABLED=0 go run ./cmd/agent-studio node test ./examples/nodes/echo
  git add internal/cli
  git commit -m "feat(cli): run isolated node tests"
  ```

## Task 8：增加本地 Retriever 示例

**文件：**

- 新增：`examples/nodes/retriever/node.go`
- 新增：`examples/nodes/retriever/config.go`
- 新增：`examples/nodes/retriever/register.go`
- 新增：`examples/nodes/retriever/node_test.go`
- 新增：`examples/nodes/retriever/README.md`

- [ ] 先写契约和行为测试。固定文档集与 query 后，结果按分数降序、同分按原文档下标升序；`topK` 越界和空文档集返回 config 错误。

- [ ] 运行红灯测试：

  ```bash
  CGO_ENABLED=0 go test ./examples/nodes/retriever -count=1
  ```

- [ ] 配置形状固定为：

  ```json
  {
    "documents": [
      {"id": "doc-1", "text": "Agent Studio builds workflows"}
    ],
    "topK": 3
  }
  ```

  输入端口 `query:string`，输出端口 `matches:json`。匹配使用小写 Unicode 词元集合的 Jaccard 相似度，不访问网络、不读文件、不声明 Capability。

- [ ] 输出每项只含 `id`、`text`、`score`，score 四舍五入到 6 位小数，保证跨运行确定性。

- [ ] 编写 README，明确它是 SDK 演示而非生产向量数据库，并给出单独加入 manifest 的 YAML 片段。

- [ ] 验证并提交：

  ```bash
  CGO_ENABLED=0 go test ./examples/nodes/retriever -count=1
  git add examples/nodes/retriever
  git commit -m "feat(examples): add deterministic retriever node"
  ```

## Task 9：增加受控 Webhook 示例

**文件：**

- 新增：`examples/nodes/webhook/node.go`
- 新增：`examples/nodes/webhook/client.go`
- 新增：`examples/nodes/webhook/register.go`
- 新增：`examples/nodes/webhook/node_test.go`
- 新增：`examples/nodes/webhook/README.md`

- [ ] 先用 `httptest.Server` 写测试，覆盖成功、非 2xx、超时、重定向、缺少 URL 环境变量、取消、响应超限，以及 token 不出现在输出和错误文本。

- [ ] 运行红灯测试：

  ```bash
  CGO_ENABLED=0 go test ./examples/nodes/webhook -count=1
  ```

- [ ] 节点配置只允许相对 `path` 和 `timeoutMs`，目标 origin 从运维环境变量 `AGENT_STUDIO_EXAMPLE_WEBHOOK_URL` 获取，凭据从 `AGENT_STUDIO_EXAMPLE_WEBHOOK_TOKEN` 获取。用户配置不得提供 scheme、host 或完整 URL。

- [ ] 客户端固定：默认超时 5 秒、最大 30 秒、禁用重定向、响应体上限 1 MiB；token 以 `Authorization: Bearer` 发出但不进入 `Details`、输出或错误字符串。返回 JSON 或文本的任意字符串中若出现 token 子串，递归替换为 `[REDACTED]` 后再写入输出。

- [ ] Definition 声明 `network` 和 `secrets`；输入 `body:json`，输出只含 `status:number` 和 `body:json`。

- [ ] README 说明示例边界、环境变量和生产场景仍需出站策略/进程隔离。

- [ ] 验证并提交：

  ```bash
  CGO_ENABLED=0 go test ./examples/nodes/webhook -count=1
  git add examples/nodes/webhook
  git commit -m "feat(examples): add controlled webhook node"
  ```

## Task 10：闭合“生成后画布可见”回归

**文件：**

- 新增：`internal/generatedtest/generated_test.go`
- 新增：`apps/web/e2e/sdk-node.spec.ts`
- 修改：`Makefile`

- [ ] 先写 Go 集成测试：在临时目录复制最小 `go.mod` 和脚手架输出，解析 manifest、生成注册文件并执行 `CGO_ENABLED=0 go test ./...`。该测试通过注入本地 module replacement 使用当前 SDK，不联网；同时断言仓库内 `examples/nodes/echo` 与 `node init --type example.echo --register=false` 的规范化输出一致，防止默认 Echo 绕过脚手架路径。

- [ ] 先写 Playwright 测试，打开工作流编辑器后给 Start 增加必填 string 参数 `topic`，从节点库搜索 `Echo`，拖入画布，连接 Start.topic → Echo.text → End.result，保存并重新加载；随后测试运行输入 `hello`，断言最终输出也是 `hello`，并确认节点和两条边仍存在。

- [ ] 运行红灯测试：

  ```bash
  CGO_ENABLED=0 go test ./internal/generatedtest -count=1
  corepack pnpm --filter @agent-studio/web test:e2e --grep "SDK Echo"
  ```

- [ ] 完成测试夹具与稳定选择器。前端不得硬编码 `example.echo`；节点必须来自 `/api/node-types` 返回的生成注册结果。

- [ ] Makefile 增加组合目标：

  ```make
  test-sdk-e2e: check-generated
	CGO_ENABLED=0 go test ./internal/generatedtest -count=1
	corepack pnpm --filter @agent-studio/web test:e2e --grep "SDK Echo"
  ```

- [ ] 运行验证并提交：

  ```bash
  make test-sdk-e2e
  git add internal/generatedtest apps/web/e2e/sdk-node.spec.ts Makefile
  git commit -m "test(sdk): verify generated nodes reach the canvas"
  ```

## Task 11：发布中英文开发者路径

**文件：**

- 新增：`docs/sdk/quickstart.md`
- 新增：`docs/sdk/quickstart.en.md`
- 新增：`docs/sdk/debugging.md`
- 修改：`README.md`

- [ ] 中文快速开始按真实执行顺序编写：前置环境、安装 CLI、`doctor`、`node init`、填写 Execute、`node test`、`generate`、启动 PostgreSQL/API/Web、在画布搜索并运行 Echo。

- [ ] 英文快速开始与中文版命令逐字一致；只翻译叙述，不维护第二套参数。

- [ ] 命令块固定包含：

  ```bash
  go install github.com/yyl1212/agent-studio/cmd/agent-studio@v0.2.0
  agent-studio doctor
  agent-studio node init examples/nodes/hello --type example.hello
  agent-studio node test ./examples/nodes/hello
  agent-studio generate
  docker compose up -d db
  make dev-api
  make dev-web
  ```

- [ ] 调试文档覆盖 manifest 严格错误、`GOPROXY=off` 缺依赖、生成代码过期、端口冲突、context 取消、Capability 非沙箱和安全日志定位。

- [ ] README 增加“扩展节点”入口、三类示例链接，以及 `go install` 命令；不把 Retriever/Webhook 默认加入 manifest。

- [ ] 手工从全新临时目录逐条执行快速开始。计时从 CLI 安装完成开始，到 `example.hello` 出现在画布并成功运行结束，目标小于 30 分钟；将实测环境和耗时写入文档末尾“已验证路径”。

- [ ] 提交文档：

  ```bash
  git add docs/sdk README.md
  git commit -m "docs(sdk): add bilingual node quickstart"
  ```

## Task 12：最终审查与回归门禁

**文件：**

- 仅在发现问题时修改本计划涉及的文件

- [ ] 生成并验证仓库状态：

  ```bash
  CGO_ENABLED=0 go run ./cmd/agent-studio generate
  CGO_ENABLED=0 go fmt ./...
  git diff --check
  make check-generated
  ```

- [ ] 运行完整测试：

  ```bash
  CGO_ENABLED=0 go test ./... -count=1
  CGO_ENABLED=0 go vet ./...
  make verify
  make test-e2e
  make test-sdk-e2e
  ```

- [ ] 使用 `superpowers:requesting-code-review` 检查命令注入、路径穿越、原子写、确定性、离线生成、示例密钥泄漏和快速开始可复现性。

- [ ] 使用 `superpowers:receiving-code-review` 验证并修复有效问题，重复全部测试；P0、P1、P2 清零。

- [ ] 检查提交和工作区：

  ```bash
  git log --oneline master..HEAD
  git diff --stat master...HEAD
  git status --short
  ```

  预期：生成文件无差异、工作区干净。

## 完成标准

- `go install github.com/yyl1212/agent-studio/cmd/agent-studio@v0.2.0` 对应的命令接口已固定并有测试。
- 从 `node init` 到 Echo 出现在画布的自动回归通过，人工快速开始实测少于 30 分钟。
- 相同 manifest 连续生成不会修改文件时间或内容。
- 三类示例全部通过契约测试，Webhook 的 URL 和 token 不可由普通工作流输出读取。
- 中文、英文快速开始和 CLI `help` 命令一致。
