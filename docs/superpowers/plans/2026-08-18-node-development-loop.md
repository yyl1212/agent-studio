# Agent Studio 节点开发闭环 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立仓库内扩展节点的完整开发闭环，使开发者通过 `node init → node test → generate` 把 Echo 节点接入 Runtime 并在画布中运行。

**Architecture:** CLI 只负责命令编排，严格 manifest、确定性生成器和脚手架分别位于独立内部包。扩展节点通过编译期生成的 `RegisterNodes(agentnode.Registrar)` 接入现有 Registry，前端继续消费 `/api/node-types`，不维护节点专用映射。

**Tech Stack:** Go 1.26.5、`go.yaml.in/yaml/v3` v3.0.4、`golang.org/x/mod` v0.38.0、React 19、React Flow、Playwright Chromium、Docker PostgreSQL 18。

## Global Constraints

- 所有 Go 构建、测试和 CLI 子进程显式设置 `CGO_ENABLED=0`。
- 首期只支持仓库内 `extensions/`，名称只接受小写 kebab-case。
- CLI 黄金路径固定为 `node init → node test → generate → 启动 API → 画布运行`。
- `generate` 使用 `GOPROXY=off go list -mod=readonly`，不下载或执行清单中的节点代码。
- Manifest、生成文件和脚手架更新必须先完整校验；失败不得破坏已有文件。
- 扩展节点只依赖 `sdk/go/agentnode`，测试可额外依赖 `sdk/go/agenttest`。
- 生成代码字节级确定，内容不变时不得更新 mtime。
- 只加载可信的进程内扩展；本计划不实现热加载、进程隔离、Retriever、Webhook 或发布流水线。
- 用户可见文档使用中文；代码、标识符和提交消息使用英文。

---

## 文件结构

| 路径 | 操作 | 单一职责 |
|---|---|---|
| `cmd/agent-studio/main.go` | 新增 | CLI 进程入口与信号 context |
| `internal/cli/app.go` | 新增 | 顶级命令解析、帮助、版本和退出码 |
| `internal/cli/doctor.go` | 新增 | 只读开发环境诊断 |
| `internal/cli/generate.go` | 新增 | manifest 到 generator 的命令编排 |
| `internal/cli/node_init.go` | 新增 | scaffold 命令编排 |
| `internal/cli/node_test.go` | 新增 | 包边界校验和 Go 测试子进程 |
| `internal/nodemanifest/manifest.go` | 新增 | manifest 类型、严格解析、纯内存修改与编码 |
| `internal/nodegen/generate.go` | 新增 | 包验证、确定性渲染和原子写入 |
| `internal/scaffold/scaffold.go` | 新增 | 脚手架计划、模板渲染和事务式应用 |
| `agent-studio.nodes.yaml` | 新增 | 扩展节点唯一声明清单 |
| `extensions/echo/*` | 新增（由 CLI 生成） | 最小公开 SDK 扩展节点 |
| `apps/api/internal/generated/nodes_gen.go` | 新增（生成） | 扩展节点统一注册入口 |
| `apps/api/cmd/server/main.go` | 修改 | 启动时注册生成节点 |
| `apps/api/cmd/server/main_test.go` | 新增 | Server 扩展注册入口回归 |
| `apps/api/internal/httpapi/router_test.go` | 修改 | 扩展节点类型 API 回归 |
| `internal/generatedtest/golden_path_test.go` | 新增 | 临时 Module 中执行完整 CLI 黄金路径 |
| `apps/web/e2e/sdk-node.spec.ts` | 新增 | Echo 画布和运行回归 |
| `Makefile` | 修改 | 生成检查与 SDK E2E 入口 |
| `docs/sdk/quickstart.md` | 新增 | 30 分钟中文快速开始 |
| `docs/sdk/debugging.md` | 新增 | CLI、生成、注册和运行排错 |

---

### Task 1: 建立可测试 CLI 外壳

**Files:**
- Create: `cmd/agent-studio/main.go`
- Create: `internal/cli/app.go`
- Create: `internal/cli/app_test.go`

**Interfaces:**
- Produces: `func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int`
- Produces: 顶级命令保留字 `doctor`、`generate`、`node init`、`node test`。
- Consumes: `agentnode.Version` 和 `agentnode.APIVersion`。

- [ ] **Step 1: 写 CLI 红灯测试**

在 `internal/cli/app_test.go` 写入表驱动测试：

```go
func TestRunTopLevelCommands(t *testing.T) {
    tests := []struct {
        name string
        args []string
        wantCode int
        wantOut string
        wantErr string
    }{
        {name: "help", args: []string{"help"}, wantCode: 0, wantOut: "doctor\ngenerate\nnode init\nnode test\nversion\n"},
        {name: "version", args: []string{"version"}, wantCode: 0, wantOut: "agent-studio 0.2.0 (agent-studio.dev/v1alpha1)\n"},
        {name: "unknown", args: []string{"missing"}, wantCode: 2, wantErr: "unknown command \"missing\"\n"},
        {name: "missing node subcommand", args: []string{"node"}, wantCode: 2, wantErr: "node requires init or test\n"},
    }
    for _, test := range tests {
        t.Run(test.name, func(t *testing.T) {
            var stdout, stderr bytes.Buffer
            code := Run(context.Background(), test.args, &stdout, &stderr)
            if code != test.wantCode || stdout.String() != test.wantOut || stderr.String() != test.wantErr {
                t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
            }
        })
    }
}
```

- [ ] **Step 2: 运行红灯测试**

Run:

```bash
CGO_ENABLED=0 go test ./internal/cli -run TestRunTopLevelCommands -count=1
```

Expected: FAIL，`internal/cli` 或 `Run` 尚不存在。

- [ ] **Step 3: 实现 CLI 调度与稳定输出**

`internal/cli/app.go` 的公开入口固定为：

```go
package cli

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int
```

空参数和 `help` 输出同一帮助；`version` 使用 `agentnode.Version` 与 `agentnode.APIVersion`。尚未实现的保留命令输出 `<command> is not implemented` 并返回 1，而不是落入 unknown command。

`cmd/agent-studio/main.go` 只做信号和退出码处理：

```go
func main() {
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()
    os.Exit(cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
```

- [ ] **Step 4: 运行 CLI 测试和可执行冒烟**

Run:

```bash
CGO_ENABLED=0 go test ./internal/cli ./cmd/agent-studio -count=1
CGO_ENABLED=0 go run ./cmd/agent-studio version
```

Expected: PASS；第二条输出 `agent-studio 0.2.0 (agent-studio.dev/v1alpha1)`。

- [ ] **Step 5: 提交 CLI 外壳**

```bash
git add cmd/agent-studio internal/cli
git commit -m "feat(cli): add agent studio command shell"
```

---

### Task 2: 实现只读 doctor

**Files:**
- Create: `internal/cli/doctor.go`
- Create: `internal/cli/doctor_test.go`
- Modify: `internal/cli/app.go`

**Interfaces:**
- Produces: `type DoctorDeps`、`type CheckResult`、`func Diagnose(ctx context.Context, root string, deps DoctorDeps) []CheckResult`。
- Consumes: Task 1 的 `Run` 调度入口。
- Later: Task 3 的 manifest 检查接入相同 `manifest` 检查项。

- [ ] **Step 1: 写 fake-probe 红灯测试**

定义可注入依赖和预期检查：

```go
type DoctorDeps struct {
    LookPath func(string) (string, error)
    Command func(context.Context, string, ...string) ([]byte, error)
    Listen func(string, string) (net.Listener, error)
    ReadFile func(string) ([]byte, error)
}

func TestDiagnoseClassifiesRequiredToolsAndPorts(t *testing.T) {
    deps := fakeDoctorDeps()
    deps.Command = func(_ context.Context, name string, args ...string) ([]byte, error) {
        switch name {
        case "go": return []byte("go version go1.25.0 linux/amd64"), nil
        case "node": return []byte("v24.0.0"), nil
        case "docker": return []byte("Docker Compose version v2.40.0"), nil
        default: return nil, fmt.Errorf("unexpected command %s %v", name, args)
        }
    }
    results := Diagnose(context.Background(), t.TempDir(), deps)
    assertCheck(t, results, "go", "fail")
    assertCheck(t, results, "node", "ok")
    assertCheck(t, results, "port 8080", "ok")
}
```

再添加三项精确测试：Docker daemon 失败为 `fail`；端口 `EADDRINUSE` 为 `warn`；数据库未运行为 `warn`。所有测试使用 fake，不读取开发机环境。

- [ ] **Step 2: 运行 doctor 红灯测试**

```bash
CGO_ENABLED=0 go test ./internal/cli -run 'Doctor|Diagnose' -count=1
```

Expected: FAIL，`Diagnose` 尚不存在。

- [ ] **Step 3: 实现固定检查项与 CLI 输出**

实现 `CheckResult`：

```go
type CheckResult struct {
    Name string
    Status string // ok, warn, fail
    Detail string
}
```

检查 Go >= 1.26、Node >= 24、Corepack、Docker daemon、Docker Compose v2、5432/8080/5173 端口。命令输出每行固定为 `[ok] name: detail`、`[warn] ...` 或 `[fail] ...`；存在 `fail` 时 `doctor` 返回 1，仅有 `warn` 时返回 0。所有成功创建的 listener 立即关闭。

- [ ] **Step 4: 验证 doctor 不产生副作用**

```bash
CGO_ENABLED=0 go test ./internal/cli -run 'Doctor|Diagnose' -count=1
CGO_ENABLED=0 go run ./cmd/agent-studio doctor
git status --short
```

Expected: 测试 PASS；doctor 可按本机状态返回 0 或 1，但 `git status` 不出现 doctor 新建文件。

- [ ] **Step 5: 提交 doctor**

```bash
git add internal/cli
git commit -m "feat(cli): add read-only environment doctor"
```

---

### Task 3: 建立严格节点 manifest

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Create: `internal/nodemanifest/manifest.go`
- Create: `internal/nodemanifest/manifest_test.go`
- Create: `internal/nodemanifest/testdata/valid.yaml`
- Create: `agent-studio.nodes.yaml`
- Modify: `internal/cli/doctor.go`
- Modify: `internal/cli/doctor_test.go`

**Interfaces:**
- Produces: `Manifest`、`NodePackage`、`Parse`、`Load`、`Marshal`、`AddPackage`。
- Consumes: `agentnode.APIVersion`。
- Later: Task 4 generator 和 Task 5 scaffold 只消费这些纯 manifest 接口。

- [ ] **Step 1: 固定 YAML 和 module 校验依赖**

```bash
CGO_ENABLED=0 go get go.yaml.in/yaml/v3@v3.0.4
CGO_ENABLED=0 go get golang.org/x/mod@v0.38.0
```

- [ ] **Step 2: 写 manifest 红灯测试**

```go
func TestParseStrictManifest(t *testing.T) {
    tests := []struct {
        name string
        source string
        wantErr string
    }{
        {name: "unknown top field", source: "apiVersion: agent-studio.dev/v1alpha1\nnodes: []\nextra: true\n", wantErr: "field extra"},
        {name: "unknown node field", source: "apiVersion: agent-studio.dev/v1alpha1\nnodes:\n  - package: example.com/node\n    command: run\n", wantErr: "field command"},
        {name: "wrong version", source: "apiVersion: v2\nnodes: []\n", wantErr: "unsupported apiVersion"},
        {name: "duplicate", source: "apiVersion: agent-studio.dev/v1alpha1\nnodes:\n  - package: example.com/node\n  - package: example.com/node\n", wantErr: "duplicate package"},
        {name: "multi document", source: "apiVersion: agent-studio.dev/v1alpha1\nnodes: []\n---\nnodes: []\n", wantErr: "multiple YAML documents"},
    }
    for _, test := range tests {
        t.Run(test.name, func(t *testing.T) {
            _, err := Parse("manifest.yaml", []byte(test.source))
            if err == nil || !strings.Contains(err.Error(), test.wantErr) {
                t.Fatalf("error=%v, want substring %q", err, test.wantErr)
            }
        })
    }
}
```

另写 round-trip 测试：`Marshal(AddPackage(empty, "example.com/project/extensions/echo"))` 再 Parse 后只包含一个包；重复 AddPackage 返回错误且不修改输入切片。

- [ ] **Step 3: 运行 manifest 红灯测试**

```bash
CGO_ENABLED=0 go test ./internal/nodemanifest -count=1
```

Expected: FAIL，包不存在。

- [ ] **Step 4: 实现纯 manifest API**

```go
type Manifest struct {
    APIVersion string `yaml:"apiVersion"`
    Nodes []NodePackage `yaml:"nodes"`
}

type NodePackage struct {
    Package string `yaml:"package"`
}

func Parse(source string, data []byte) (Manifest, error)
func Load(path string) (Manifest, error)
func Marshal(manifest Manifest) ([]byte, error)
func AddPackage(manifest Manifest, importPath string) (Manifest, error)
```

`Parse` 使用 `yaml.Decoder.KnownFields(true)`，第二次 Decode 必须得到 `io.EOF`；路径调用 `module.CheckImportPath`；所有错误包含 source 与字段原因，但不打印整个 manifest。

- [ ] **Step 5: 添加空根清单并接入 doctor**

`agent-studio.nodes.yaml` 初始内容固定为：

```yaml
apiVersion: agent-studio.dev/v1alpha1
nodes: []
```

Doctor 在文件存在时调用 `nodemanifest.Load`；不兼容或非法为 `fail`，文件不存在为 `warn`，不得创建文件。

- [ ] **Step 6: 验证并整理依赖**

```bash
CGO_ENABLED=0 go test ./internal/nodemanifest ./internal/cli -count=1
CGO_ENABLED=0 go mod tidy
git diff --check
```

Expected: PASS；`sdk/go/agentnode` 仍只依赖标准库。

- [ ] **Step 7: 提交严格 manifest**

```bash
git add go.mod go.sum internal/nodemanifest internal/cli agent-studio.nodes.yaml
git commit -m "feat(cli): add strict node manifest"
```

---

### Task 4: 实现确定性注册代码生成

**Files:**
- Create: `internal/nodegen/generate.go`
- Create: `internal/nodegen/generate_test.go`
- Create: `internal/nodegen/testdata/nodes_gen.golden`
- Create: `internal/cli/generate.go`
- Create: `internal/cli/generate_test.go`
- Modify: `internal/cli/app.go`
- Create: `apps/api/internal/generated/nodes_gen.go`

**Interfaces:**
- Consumes: Task 3 的 `nodemanifest.Manifest` 和 `Load`。
- Produces: `Generator.Generate(ctx, root, manifest, outputPath) (changed bool, err error)`。
- Produces: 生成代码 `func RegisterNodes(registrar agentnode.Registrar) error`。
- Later: Task 7 Server 只依赖生成包公开入口。

- [ ] **Step 1: 写渲染和原子写入红灯测试**

```go
func TestGenerateIsSortedAndStable(t *testing.T) {
    root := t.TempDir()
    output := filepath.Join(root, "nodes_gen.go")
    generator := Generator{ListPackage: func(_ context.Context, _, path string) error { return nil }}
    manifest := nodemanifest.Manifest{APIVersion: agentnode.APIVersion, Nodes: []nodemanifest.NodePackage{
        {Package: "example.com/zeta"}, {Package: "example.com/alpha"},
    }}
    changed, err := generator.Generate(context.Background(), root, manifest, output)
    if err != nil || !changed { t.Fatalf("changed=%v err=%v", changed, err) }
    first, _ := os.ReadFile(output)
    before, _ := os.Stat(output)
    changed, err = generator.Generate(context.Background(), root, manifest, output)
    after, _ := os.Stat(output)
    if err != nil || changed || !before.ModTime().Equal(after.ModTime()) {
        t.Fatalf("changed=%v err=%v before=%v after=%v", changed, err, before.ModTime(), after.ModTime())
    }
    assertGolden(t, "testdata/nodes_gen.golden", first)
}
```

再写 `TestGeneratePreservesOldFileWhenPackageValidationFails`：预写 `old-content`，让第二个包校验失败，断言目标仍为 `old-content` 且没有残留临时文件。

- [ ] **Step 2: 运行 generator 红灯测试**

```bash
CGO_ENABLED=0 go test ./internal/nodegen -count=1
```

Expected: FAIL，`Generator` 尚不存在。

- [ ] **Step 3: 实现 generator 与真实包验证**

```go
type Generator struct {
    ListPackage func(ctx context.Context, root, importPath string) error
}

func (generator Generator) Generate(
    ctx context.Context,
    root string,
    manifest nodemanifest.Manifest,
    outputPath string,
) (bool, error)
```

默认 `ListPackage` 执行：

```text
GOPROXY=off CGO_ENABLED=0 go list -mod=readonly <importPath>
```

子进程 `Dir=root`，继承环境时覆盖同名变量。生成 import alias 固定为排序后的 `node0`、`node1`。每个注册错误包装包路径：

```go
if err := node0.Register(registrar); err != nil {
    return fmt.Errorf("register example.com/alpha: %w", err)
}
```

先验证全部包，再 render、`go/format.Source`、比较旧内容；仅内容变化时创建同目录临时文件、fsync、chmod 0644、rename。

- [ ] **Step 4: 写 generate 命令红灯测试**

在临时 root 写合法 manifest，注入 fake generator，断言 CLI 使用默认输出 `apps/api/internal/generated/nodes_gen.go`；manifest 非法时 generator 不被调用，stderr 包含文件路径。

```bash
CGO_ENABLED=0 go test ./internal/cli -run Generate -count=1
```

Expected: FAIL，generate 仍返回未实现。

- [ ] **Step 5: 接入 CLI 并生成空注册入口**

`agent-studio generate` 从当前目录向上查找包含 `go.mod` 和 `agent-studio.nodes.yaml` 的根目录；任一缺失返回 1。成功输出 `generated <path>` 或 `unchanged <path>`。

Run:

```bash
CGO_ENABLED=0 go run ./cmd/agent-studio generate
CGO_ENABLED=0 go run ./cmd/agent-studio generate
```

Expected: 第一次生成空 `RegisterNodes`，第二次输出 unchanged。

- [ ] **Step 6: 验证生成确定性**

```bash
CGO_ENABLED=0 go test ./internal/nodegen ./internal/cli ./apps/api/internal/generated -count=1
before=$(shasum apps/api/internal/generated/nodes_gen.go)
CGO_ENABLED=0 go run ./cmd/agent-studio generate
after=$(shasum apps/api/internal/generated/nodes_gen.go)
test "$before" = "$after"
```

- [ ] **Step 7: 提交 generator**

```bash
git add internal/nodegen internal/cli apps/api/internal/generated
git commit -m "feat(cli): generate deterministic node registration"
```

---

### Task 5: 实现 Echo 脚手架和 node init

**Files:**
- Create: `internal/scaffold/scaffold.go`
- Create: `internal/scaffold/scaffold_test.go`
- Create: `internal/scaffold/templates/node.go.tmpl`
- Create: `internal/scaffold/templates/node_test.go.tmpl`
- Create: `internal/scaffold/templates/README.md.tmpl`
- Create: `internal/cli/node_init.go`
- Create: `internal/cli/node_init_test.go`
- Modify: `internal/cli/app.go`

**Interfaces:**
- Consumes: Task 3 的 `nodemanifest.Parse`、`Marshal`、`AddPackage`。
- Produces: `scaffold.Plan(request Request) (ScaffoldPlan, error)` 与 `scaffold.Apply(plan ScaffoldPlan, deps ApplyDeps) error`。
- Produces: `node init <name>` 仓库内命令。

- [ ] **Step 1: 写名称、模板和覆盖保护红灯测试**

```go
func TestPlanEchoScaffold(t *testing.T) {
    manifest := nodemanifest.Manifest{APIVersion: agentnode.APIVersion, Nodes: []nodemanifest.NodePackage{}}
    plan, err := Plan(Request{RootDir: "/repo", ModulePath: "example.com/studio", Name: "my-echo", Manifest: manifest})
    if err != nil { t.Fatal(err) }
    if got, want := plan.Directory, "/repo/extensions/my-echo"; got != want { t.Fatalf("directory=%q", got) }
    assertPlannedFileContains(t, plan, "node.go", `Type: "extension.my-echo"`)
    assertPlannedFileContains(t, plan, "node.go", "package myecho")
    if got := plan.Manifest.Nodes[0].Package; got != "example.com/studio/extensions/my-echo" { t.Fatalf("package=%q", got) }
}
```

表驱动拒绝 `Echo`、`echo_node`、`../echo`、`echo--node`、空名称；Apply 测试预建非空目录并断言所有文件保持不变。

- [ ] **Step 2: 写 manifest rename 失败回滚测试**

使用 fake `ApplyDeps` 让 manifest rename 返回 `permission denied`，断言本次新建的 `extensions/echo` 被删除，原 manifest 字节不变；预先存在的兄弟目录不被删除。

- [ ] **Step 3: 运行 scaffold 红灯测试**

```bash
CGO_ENABLED=0 go test ./internal/scaffold -count=1
```

Expected: FAIL，scaffold 不存在。

- [ ] **Step 4: 实现纯计划和可回滚应用**

```go
type Request struct {
    RootDir string
    ModulePath string
    Name string
    Manifest nodemanifest.Manifest
}

type ScaffoldPlan struct {
    Directory string
    Files map[string][]byte
    Manifest nodemanifest.Manifest
    ManifestPath string
}

func Plan(request Request) (ScaffoldPlan, error)
func Apply(plan ScaffoldPlan, deps ApplyDeps) error
```

模板生成的 Definition 固定为：版本 `1.0.0`、分类 `扩展`、说明 `返回带前缀的输入文本`，Title 由 kebab-case 名称分词首字母大写（`echo` 得到 `Echo`，`my-echo` 得到 `My Echo`）。配置 Schema 只有标题为 `前缀` 的可选 `prefix` 字符串；输入和输出都使用 key `text`、标题 `文本`、类型 `string`、基数 `one`。

Execute 先检查 `ctx.Err()`，取消时返回 `agentnode.NewError(ErrorKindCanceled, "run_canceled", ctx.Err(), nil)`；再校验单值字符串输入，错误使用 `agentnode.NewError(ErrorKindInput, "invalid_text", cause, nil)`。

模板测试使用 `agenttest.Run`，覆盖空前缀、带前缀执行和已取消 context；README 只包含公开 SDK import 和三条黄金命令。

- [ ] **Step 5: 写 node init CLI 红灯测试**

在 temp root 写 `go.mod` 与空 manifest，调用内部 CLI runner 执行 `node init echo`，断言创建三个文件、manifest 新增正确 import path、stdout 只输出下一步命令；再次执行返回 1 且文件字节不变。

```bash
CGO_ENABLED=0 go test ./internal/cli -run NodeInit -count=1
```

Expected: FAIL，node init 尚未接入。

- [ ] **Step 6: 接入 node init**

从根 `go.mod` 使用 `modfile.Parse` 读取 module path，不调用网络。输出固定为：

```text
created extensions/echo
next: CGO_ENABLED=0 go run ./cmd/agent-studio node test ./extensions/echo
next: CGO_ENABLED=0 go run ./cmd/agent-studio generate
```

- [ ] **Step 7: 验证脚手架**

```bash
CGO_ENABLED=0 go test ./internal/scaffold ./internal/cli -run 'Scaffold|NodeInit|Plan|Apply' -count=1
git diff --check
```

- [ ] **Step 8: 提交脚手架**

```bash
git add internal/scaffold internal/cli
git commit -m "feat(cli): scaffold repository node extensions"
```

---

### Task 6: 实现 node test

**Files:**
- Create: `internal/cli/node_test.go`
- Create: `internal/cli/node_test_test.go`
- Modify: `internal/cli/app.go`

**Interfaces:**
- Consumes: Task 1 的 CLI 退出码和 Task 5 的仓库根查找约定。
- Produces: `node test <package>`，依次执行离线 `go list` 与 `go test`。

- [ ] **Step 1: 写子进程参数和输出传播红灯测试**

```go
func TestNodeTestUsesOfflineCGOFreeGoCommands(t *testing.T) {
    var calls []processCall
    runner := func(_ context.Context, call processCall) error {
        calls = append(calls, call)
        if call.Stdout != nil { _, _ = io.WriteString(call.Stdout, "ok\n") }
        return nil
    }
    var stdout, stderr bytes.Buffer
    code := runNodeTest(context.Background(), "/repo", "./extensions/echo", &stdout, &stderr, runner)
    if code != 0 { t.Fatalf("code=%d stderr=%s", code, stderr.String()) }
    assertProcessCall(t, calls[0], "go", []string{"list", "-mod=readonly", "./extensions/echo"}, map[string]string{"CGO_ENABLED":"0", "GOPROXY":"off"})
    assertProcessCall(t, calls[1], "go", []string{"test", "./extensions/echo", "-count=1"}, map[string]string{"CGO_ENABLED":"0"})
}
```

再测试：缺少 package 参数返回 2；`../outside` 返回 2 且不调用 runner；go list 失败时不调用 go test；go test stderr 和退出失败原样传播。

- [ ] **Step 2: 运行 node test 红灯测试**

```bash
CGO_ENABLED=0 go test ./internal/cli -run NodeTest -count=1
```

Expected: FAIL，`runNodeTest` 不存在。

- [ ] **Step 3: 实现包边界和子进程执行**

接受 `./extensions/<name>` 或当前 module import path；通过绝对路径和 `filepath.Rel(root, target)` 拒绝逃逸根目录，通过 `go list` 确认 package。不得把 package 字符串交给 shell。

成功输出 go test 原文，不增加 JSON 包装；任何子进程失败返回 1，参数错误返回 2。

- [ ] **Step 4: 验证 node test**

```bash
CGO_ENABLED=0 go test ./internal/cli -run NodeTest -count=1
CGO_ENABLED=0 go test ./internal/cli ./internal/scaffold -count=1
```

- [ ] **Step 5: 提交 node test**

```bash
git add internal/cli
git commit -m "feat(cli): test node extension packages"
```

---

### Task 7: 用 CLI 生成 Echo 并接入 Runtime

**Files:**
- Create: `extensions/echo/node.go`（由 CLI 生成）
- Create: `extensions/echo/node_test.go`（由 CLI 生成）
- Create: `extensions/echo/README.md`（由 CLI 生成）
- Modify: `agent-studio.nodes.yaml`（由 CLI 修改）
- Modify: `apps/api/internal/generated/nodes_gen.go`（由 CLI 生成）
- Create: `apps/api/internal/generated/nodes_gen_test.go`
- Modify: `apps/api/cmd/server/main.go`
- Create: `apps/api/cmd/server/main_test.go`
- Modify: `apps/api/internal/httpapi/router_test.go`

**Interfaces:**
- Consumes: Task 4 的 `generated.RegisterNodes`、Task 5 的 Echo 模板、Task 6 的 node test。
- Produces: Runtime 中的 `extension.echo@1.0.0`。
- Later: Task 8 的 API 和画布测试依赖该节点定义。

- [ ] **Step 1: 通过黄金命令创建 Echo**

```bash
CGO_ENABLED=0 go run ./cmd/agent-studio node init echo
CGO_ENABLED=0 go run ./cmd/agent-studio node test ./extensions/echo
CGO_ENABLED=0 go run ./cmd/agent-studio generate
```

Expected: 三条命令成功；manifest 只含 Echo；生成文件导入 `github.com/yyl1212/agent-studio/extensions/echo`。

- [ ] **Step 2: 写生成注册入口红灯测试**

```go
func TestRegisterNodesAddsEcho(t *testing.T) {
    registry := nodes.NewRegistry()
    if err := RegisterNodes(registry); err != nil { t.Fatal(err) }
    node, err := registry.Get("extension.echo", "1.0.0")
    if err != nil { t.Fatal(err) }
    result, err := node.Execute(context.Background(), agentnode.Request{
        Config: json.RawMessage(`{"prefix":"回答："}`),
        Inputs: map[string][]any{"text": {"你好"}},
    })
    if err != nil || result.Outputs["text"] != "回答：你好" {
        t.Fatalf("result=%v err=%v", result, err)
    }
}
```

- [ ] **Step 3: 验证测试先暴露 Server 尚未调用扩展注册**

在 `apps/api/cmd/server/main_test.go` 抽取并测试 `registerExtensionNodes(registry)`；测试期先断言函数不存在或 Echo 未注册。

```bash
CGO_ENABLED=0 go test ./apps/api/internal/generated ./apps/api/cmd/server -run 'RegisterNodes|ExtensionNodes' -count=1
```

Expected: generated 包测试可通过；server 测试 FAIL，扩展入口尚未调用。

- [ ] **Step 4: 接入 Server 启动注册**

在内置节点全部成功注册后、创建 Compiler 前调用：

```go
if err := generated.RegisterNodes(registry); err != nil {
    return fmt.Errorf("register extension nodes: %w", err)
}
```

用小函数封装以便 server 单测，不修改 Registry 或 SDK 接口。重复类型和非法 Definition 继续由 Registry 阻止启动。

- [ ] **Step 5: 运行 Runtime 集成回归**

在 `router_test.go` 增加 API 回归，使用 `generated.RegisterNodes(dependencies.Registry)` 后请求 `/api/node-types`，反序列化为 `[]agentnode.Definition`，断言存在且只存在一个 `extension.echo@1.0.0`，其 `inputs`/`outputs` 均为 JSON 数组、`category` 为 `扩展`。

```bash
CGO_ENABLED=0 go test ./extensions/echo ./apps/api/internal/generated ./apps/api/cmd/server ./apps/api/internal/httpapi ./apps/api/internal/nodes ./apps/api/internal/engine -count=1
CGO_ENABLED=0 go vet ./extensions/echo ./apps/api/internal/generated ./apps/api/cmd/server
```

- [ ] **Step 6: 提交 Echo 与 Runtime 接入**

```bash
git add extensions/echo agent-studio.nodes.yaml apps/api/internal/generated apps/api/cmd/server apps/api/internal/httpapi/router_test.go
git commit -m "feat(nodes): register generated echo extension"
```

---

### Task 8: 建立临时 Module 黄金路径和画布 E2E

**Files:**
- Create: `internal/generatedtest/golden_path_test.go`
- Create: `apps/web/e2e/sdk-node.spec.ts`
- Modify: `apps/web/playwright.config.ts`
- Modify: `Makefile`
- Modify: `.gitignore`

**Interfaces:**
- Consumes: 完整 CLI、Echo 注册和现有画布通用节点组件。
- Produces: `make check-generated` 与 `make test-sdk-e2e`。

- [ ] **Step 1: 写临时 Module 黄金路径测试**

`internal/generatedtest/golden_path_test.go` 执行以下真实流程：

```go
func TestRepositoryNodeGoldenPath(t *testing.T) {
    repo := repositoryRoot(t)
    fixture := t.TempDir()
    cliBinary := filepath.Join(t.TempDir(), "agent-studio")
    run(t, repo, env("CGO_ENABLED", "0"), "go", "build", "-o", cliBinary, "./cmd/agent-studio")
    writeFile(t, filepath.Join(fixture, "go.mod"), fmt.Sprintf(`module example.com/sdkfixture

go 1.26.0

require github.com/yyl1212/agent-studio v0.0.0
replace github.com/yyl1212/agent-studio => %s
`, filepath.ToSlash(repo)))
    writeFile(t, filepath.Join(fixture, "agent-studio.nodes.yaml"), "apiVersion: agent-studio.dev/v1alpha1\nnodes: []\n")
    run(t, fixture, nil, cliBinary, "node", "init", "echo")
    run(t, fixture, nil, cliBinary, "node", "test", "./extensions/echo")
    run(t, fixture, nil, cliBinary, "generate")
    run(t, fixture, env("CGO_ENABLED", "0"), "go", "test", "./...", "-count=1")
}
```

测试断言临时 fixture 位于 `t.TempDir()`，清理后仓库 `git status` 不变化。

- [ ] **Step 2: 运行黄金路径测试**

```bash
CGO_ENABLED=0 go test ./internal/generatedtest -run TestRepositoryNodeGoldenPath -count=1 -v
```

Expected: PASS；若 `GOPROXY=off` 下本地 replace 未被正确识别，修复 generator 的环境覆盖，不放宽离线约束。

- [ ] **Step 3: 写画布 Echo E2E**

`apps/web/e2e/sdk-node.spec.ts`：

```ts
test('扩展 Echo 无需前端专用组件即可运行', async ({ page }) => {
  const workflowURL = await createWorkflow(page, `sdk-echo-${Date.now().toString(36)}`)
  await page.goto(workflowURL)
  await configureStartTextField(page, 'topic', '主题')
  await page.getByRole('button', { name: '添加节点' }).click()
  await page.getByRole('button', { name: 'Echo' }).click()
  await page.getByLabel('前缀').fill('回答：')
  await page.getByRole('button', { name: '关闭节点配置' }).click()
  await connectPorts(page, [
    ['start', 'topic', 'extension.echo', 'text'],
    ['extension.echo', 'text', 'end', 'result'],
  ])
  await page.getByRole('button', { name: '测试运行' }).click()
  await page.getByLabel('主题').fill('SDK')
  await page.getByRole('button', { name: '运行', exact: true }).click()
  await expect(page.getByText('回答：SDK')).toBeVisible()
})
```

把现有 `buildAndPublish` 中的创建、开始节点配置和连线 helper 移到 `apps/web/e2e/helpers.ts`，两个 spec 共用。新增 helper 的签名与行为固定为：

```ts
export async function createWorkflow(page: Page, slug: string) {
  await page.goto('/workflows')
  await page.getByRole('button', { name: '新建工作流' }).click()
  await page.getByLabel('名称').fill(`SDK Echo ${slug}`)
  await page.getByLabel('Agent 地址标识').fill(slug)
  await page.getByRole('button', { name: '创建' }).click()
  await expect(page.getByText(`SDK Echo ${slug}`)).toBeVisible()
  return page.url()
}

export async function configureStartTextField(page: Page, key: string, label: string) {
  await page.getByTestId('node-start').click()
  await page.getByRole('button', { name: '添加一项' }).first().click()
  await page.getByLabel('字段标识').fill(key)
  await page.getByLabel('字段标题').fill(label)
  await page.getByLabel('字段类型').selectOption('text')
  await page.getByRole('checkbox', { name: '必填' }).check()
}
```

`connectPorts` 与 `dragHandle` 按现有 `agent-studio.spec.ts` 的完整实现原样移动并导出，调用约定保持 `Array<[sourceType, sourcePort, targetType, targetPort]>`；原 spec 改为从 helper 文件导入，行为不得变化。

- [ ] **Step 4: 改用 Playwright 自带 Chromium**

从 `apps/web/playwright.config.ts` 删除 `channel: 'chrome'`，保留 `devices['Desktop Chrome']`。安装并运行：

```bash
corepack pnpm@10.34.5 --filter @agent-studio/web exec playwright install chromium
COMPOSE_PROJECT_NAME=agent_improve make db-up
corepack pnpm@10.34.5 --filter @agent-studio/web exec playwright test e2e/sdk-node.spec.ts
```

- [ ] **Step 5: 增加生成与 SDK E2E Make 目标**

```make
.PHONY: generate check-generated test-sdk-e2e

generate:
	CGO_ENABLED=0 go run ./cmd/agent-studio generate

check-generated: generate
	git diff --exit-code -- apps/api/internal/generated/nodes_gen.go

test-sdk-e2e: db-up
	CGO_ENABLED=0 go test ./internal/generatedtest -count=1 -v
	corepack pnpm@10.34.5 --filter @agent-studio/web exec playwright test e2e/sdk-node.spec.ts
```

把 `check-generated` 加入 `verify`，把 `apps/web/test-results/`、`apps/web/playwright-report/` 加入 `.gitignore`（若尚未忽略）。

- [ ] **Step 6: 运行 SDK E2E 和既有 E2E**

```bash
COMPOSE_PROJECT_NAME=agent_improve make test-sdk-e2e
COMPOSE_PROJECT_NAME=agent_improve make test-e2e
git status --short
```

Expected: SDK Echo 与既有 2 条 E2E 全通过；状态只包含本任务预期文件。

- [ ] **Step 7: 提交 E2E 与质量入口**

```bash
git add internal/generatedtest apps/web/e2e apps/web/playwright.config.ts Makefile .gitignore
git commit -m "test(sdk): verify node creation through canvas"
```

---

### Task 9: 完成中文开发文档和最终质量门

**Files:**
- Create: `docs/sdk/quickstart.md`
- Create: `docs/sdk/debugging.md`
- Modify: `docs/node-development.md`
- Modify: `README.md`

**Interfaces:**
- Consumes: 所有已验证 CLI 命令和 Make 目标。
- Produces: 唯一中文黄金路径和错误对照表。

- [ ] **Step 1: 写 30 分钟快速开始**

`docs/sdk/quickstart.md` 必须按真实顺序包含：

```bash
CGO_ENABLED=0 go run ./cmd/agent-studio doctor
CGO_ENABLED=0 go run ./cmd/agent-studio node init my-echo
CGO_ENABLED=0 go run ./cmd/agent-studio node test ./extensions/my-echo
CGO_ENABLED=0 go run ./cmd/agent-studio generate
make db-up
make dev-api
make dev-web
```

随后说明在画布添加 `My Echo`、连接开始/结束节点并测试运行。明确扩展与 API 同进程、只加载可信源码。

- [ ] **Step 2: 写可搜索的排错对照**

`docs/sdk/debugging.md` 至少包含以下稳定错误片段及处理：

| 错误片段 | 原因 | 处理 |
|---|---|---|
| `unsupported apiVersion` | manifest 版本不兼容 | 改为 `agent-studio.dev/v1alpha1` |
| `duplicate package` | manifest 重复登记 | 删除重复 package |
| `GOPROXY=off` | 依赖不在本地缓存或 Module | 先显式加入依赖，再重新生成 |
| `directory is not empty` | node init 目标已存在 | 更换名称或人工处理旧目录 |
| `duplicate node type` | type + version 冲突 | 修改节点 type 或 version |
| `encoded outputs` | 输出超出 SDK 上限 | 缩小并结构化输出 |

- [ ] **Step 3: 收敛 README 和旧节点文档**

README 增加“创建扩展节点”短入口并链接 quickstart；`docs/node-development.md` 保留公共 SDK 接口说明，把命令流程统一链接到 quickstart，不保留与 CLI 相冲突的手工注册步骤。

- [ ] **Step 4: 做文档一致性扫描**

```bash
rg -n 'TO[D]O|TB[D]|agentstudio.local|apps/api/go.mod|go run ./apps/api/cmd' README.md docs cmd internal extensions
rg -n 'node init|node test|generate|GOPROXY=off' README.md docs/sdk docs/node-development.md
git diff --check
```

Expected: 第一条无输出；第二条能定位 quickstart、debugging 和节点开发入口。

- [ ] **Step 5: 提交文档**

```bash
git add README.md docs
git commit -m "docs(sdk): publish repository node quickstart"
```

- [ ] **Step 6: 在 Docker PostgreSQL 上运行最终验证**

```bash
COMPOSE_PROJECT_NAME=agent_improve make verify
COMPOSE_PROJECT_NAME=agent_improve make test-e2e
COMPOSE_PROJECT_NAME=agent_improve make test-sdk-e2e
CGO_ENABLED=0 go list -deps ./sdk/go/agentnode
git diff --check
git status --short
```

Expected:

- 全部 Go test 和 vet 通过。
- OpenAPI 生成零漂移。
- 前端 12+ 测试文件全部通过并完成生产构建。
- 既有 E2E 与 Echo SDK E2E 全通过。
- `agentnode` 依赖列表只包含标准库和自身包。
- 除用户已有 `.pnpm-store/` 外工作树干净。

- [ ] **Step 7: 请求代码审查并处理到 P0/P1/P2 清零**

审查重点固定为：

- CLI 是否可能执行 shell 注入或隐式联网。
- Manifest/生成/脚手架失败是否覆盖用户文件。
- 生成代码是否确定且无导入循环。
- Echo 是否只依赖公开 SDK。
- Server 注册失败是否安全终止。
- 临时 Module 和 Playwright 是否真正覆盖黄金路径。

修复任何 P0/P1/P2 后重新运行 Step 6 全量命令，直到终审无阻塞。

---

## 计划完成定义

满足以下全部条件才可声明本计划完成：

1. `doctor`、`node init`、`node test`、`generate` 均有稳定退出码和单元测试。
2. 临时 Go Module 能通过 CLI 创建、测试、生成并编译 Echo。
3. Canonical Echo 经生成入口进入 Runtime，`/api/node-types` 可见。
4. 画布无需专用组件即可配置、连线和运行 Echo。
5. `make check-generated`、`make verify`、`make test-e2e`、`make test-sdk-e2e` 全通过。
6. 中文快速开始和故障排查与真实命令一致。
7. 代码审查 P0/P1/P2 清零，工作树只保留用户原有未跟踪内容。
