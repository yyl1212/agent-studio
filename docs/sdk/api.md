# Go 节点 SDK API（v0.2）

Agent Studio Go SDK 允许扩展作者在运行时包之外实现节点。公开协议的 Go Module 路径为：

```go
import "github.com/yyl1212/agent-studio/sdk/go/agentnode"
```

当前 SDK 版本是 `0.2.0`，协议版本是 `agent-studio.dev/v1alpha1`。第三方节点可使用 `sdk/go/agenttest` 在接入前运行统一契约测试。

## Node 生命周期

每种节点实现一个无状态或并发安全的 `agentnode.Node`：

```go
type Node interface {
    Definition() Definition
    Resolve(config json.RawMessage) (ResolvedPorts, error)
    Execute(ctx context.Context, request Request) (Result, error)
}
```

运行时按以下顺序使用节点：

1. 注册时调用 `Definition`，校验身份、配置 Schema 和静态端口。
2. 编译工作流时调用 `Resolve`，把当前配置对应的动态端口固化到执行计划。
3. 运行时调用 `Execute`，并以编译期端口校验结果。

`Definition` 和 `Resolve` 应可重复调用，不应修改接收参数或返回共享的可变切片。`Resolve` 必须只由配置稳定推导端口；不得依赖时间、网络或进程外可变状态。

## 可编译的 Echo 节点

```go
package echo

import (
    "context"
    "encoding/json"
    "errors"

    "github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

type Config struct {
    Prefix string `json:"prefix,omitempty"`
}

type Node struct{}

func (Node) Definition() agentnode.Definition {
    return agentnode.Definition{
        Type: "example.echo", Version: "1.0.0", Title: "Echo",
        Description: "为文本增加前缀", Category: "文本",
        ConfigSchema: agentnode.MustSchema(`{
          "type":"object",
          "properties":{"prefix":{"type":"string"}},
          "additionalProperties":false
        }`),
        Inputs: []agentnode.Port{{
            Key: "text", Title: "文本", Type: agentnode.DataTypeString,
            Required: true, Cardinality: agentnode.CardinalityOne,
        }},
        Outputs: []agentnode.Port{{
            Key: "text", Title: "文本", Type: agentnode.DataTypeString,
            Cardinality: agentnode.CardinalityOne,
        }},
    }
}

func (node Node) Resolve(config json.RawMessage) (agentnode.ResolvedPorts, error) {
    var parsed Config
    if err := agentnode.DecodeConfig(config, &parsed); err != nil {
        return agentnode.ResolvedPorts{}, err
    }
    definition := node.Definition()
    return agentnode.ResolvedPorts{Inputs: definition.Inputs, Outputs: definition.Outputs}, nil
}

func (Node) Execute(ctx context.Context, request agentnode.Request) (agentnode.Result, error) {
    if err := ctx.Err(); err != nil {
        return agentnode.Result{}, agentnode.NewError(
            agentnode.ErrorKindCanceled, "run_canceled", err, nil,
        )
    }
    var config Config
    if err := agentnode.DecodeConfig(request.Config, &config); err != nil {
        return agentnode.Result{}, err
    }
    values := request.Inputs["text"]
    if len(values) == 0 {
        return agentnode.Result{}, agentnode.NewError(
            agentnode.ErrorKindInput, "missing_input",
            errors.New("text input is required"), map[string]any{"field": "text"},
        )
    }
    text, ok := values[0].(string)
    if !ok || len(values) != 1 {
        return agentnode.Result{}, agentnode.NewError(
            agentnode.ErrorKindInput, "invalid_input",
            errors.New("text input must contain one string"), nil,
        )
    }
    return agentnode.Result{Outputs: map[string]any{
        "text": config.Prefix + text,
    }}, nil
}

var _ agentnode.Node = Node{}
```

## Definition 与端口

`Definition` 包含节点身份、展示信息、JSON Schema、静态端口和能力声明。`Type + Version` 在注册表中必须唯一。

数据类型为 `string`、`number`、`boolean`、`json`、`any`。普通端口使用 `CardinalityOne`；需要从多个条件分支中恰好选择一个值时，输入端口可使用 `CardinalitySingleActive`。

端口 `Key` 在同一方向必须非空且唯一。端口字段是持久化工作流和画布的稳定契约，发布后不要在同一节点版本中删除或改变语义。

## 动态端口

动态节点在 `Resolve` 中解析配置并返回 `ResolvedPorts`。例如模板节点可从 `{{variable}}` 占位符生成输入端口。无输入或输出时返回空切片而不是 `nil`，确保 JSON 是 `[]`。

配置错误应返回 `config/invalid_config`。运行时会在编译阶段保存解析结果，因此 `Execute` 不应自行重新解释出另一套端口。

## Request 与 Result

`Request.Inputs` 按端口保存上游值列表，`RunInput` 仅供开始节点读取，`Config` 是当前节点的原始 JSON 配置。

`Result.Outputs` 的键必须是已解析的输出端口，值必须可 JSON 编码。`ActivePorts` 用于条件分支；省略时，含输出值的端口默认激活。不要在 Result 中返回密钥、请求凭据、完整配置或底层错误文本。

## Registrar

宿主通过公开接口注册节点：

```go
func Register(registrar agentnode.Registrar) error {
    return registrar.Register(Node{})
}
```

注册失败必须立即返回。SDK 不保证重复注册同一 `Type + Version` 成功。

## 错误分类

使用 `agentnode.NewError` 返回稳定类别和代码：

- `config`：配置无法解析或不符合节点约束。
- `input`：上游输入缺失、类型错误或基数错误。
- `temporary`：上游超时等可重试故障。
- `canceled`：调用方主动取消。
- `internal`：其他执行失败。

`NodeError.Error()` 只公开 Kind 和 Code。完整 cause 与 Details 供本地契约测试和宿主脱敏日志使用；不要把密钥放入 Code。`errors.Is` 和 `errors.As` 可沿 `Unwrap` 链检查原始错误。

## Capability

可声明 `network`、`secrets`、`filesystem-read`、`filesystem-write`。只声明节点确实需要的能力。Capability 在 v0.2 仅用于画布展示、审计和未来调度，不会创建进程、网络或文件系统沙箱。

## 并发与取消

同一个节点实例可能被多个工作流并发调用。实现应保持无状态，或自行保护共享状态。不得修改 Request 内的 map、slice 和 RawMessage。

长耗时网络、文件或计算操作必须持续监听 `ctx.Done()`，并把主动取消映射为 `canceled/run_canceled`，把上游 deadline 映射为 `temporary/upstream_timeout`。禁止启动无法在 context 取消后退出的永久 goroutine。

## 官方参考实现

仓库的 `extensions/retriever` 与 `extensions/webhook` 展示了两种互补边界：

- Retriever 的打分、排序和分词是本地纯计算；相同配置与输入产生相同输出。节点不保留请求状态，不修改输入容器，并在计算循环中检查 context。
- Webhook 声明 `network`、`secrets` capability，固定使用 JSON `POST`，主机和 Token 只从 API 进程环境读取。请求体与响应体均限制为 1 MiB，禁止重定向，并持续使用 request context 处理取消与超时。

Webhook 用稳定哨兵构造 `NodeError` 错误链，公开层只看到 kind、code 与安全消息。外部节点同样不得把 DNS、连接地址、代理信息、上游响应正文或其他底层 transport 错误直接作为 `NodeError` cause；应先映射到不含动态敏感内容的本地哨兵，再交给宿主处理。

## 契约测试

在节点包测试中导入 `agentnode` 与 `agenttest` 后调用：

```go
agenttest.Run(t, agenttest.Contract{
    Node: Node{},
    ValidConfigs: []json.RawMessage{json.RawMessage(`{"prefix":"回答："}`)},
    Executions: []agenttest.ExecutionCase{{
        Name: "echo",
        Request: agentnode.Request{
            Config: json.RawMessage(`{"prefix":"回答："}`),
            Inputs: map[string][]any{"text": {"你好"}},
        },
        WantOutputs: map[string]any{"text": "回答：你好"},
    }},
})
```

契约工具会检查身份、Schema、端口、配置错误分类、输出 JSON/大小、活动端口和可选取消行为。单个执行用例默认最多等待 1 秒，可通过 `ExecutionCase.Timeout` 调整。

当前 v0.2 节点运行在宿主 Go 进程内，没有进程级隔离。契约工具可以在 deadline 后停止等待并报告失败，但 Go 无法强制终止一个忽略 `context` 的节点 goroutine；这类实现仍可能遗留 goroutine，必须由节点作者修复。需要强制终止保证的场景应把第三方节点放到独立进程或容器中运行。
