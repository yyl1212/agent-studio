# 节点扩展开发指南

Agent Studio 的普通节点由 Go 后端注册表驱动。节点通过 `Definition` 暴露名称、分类、配置 JSON Schema 和静态端口，通过 `Resolve` 解析动态端口，通过 `Execute` 执行业务。前端从 `/api/node-types` 获取定义并使用通用 Schema 表单渲染配置，因此新增普通节点不需要修改前端。

## 完整 Echo 节点

创建 `apps/api/internal/nodes/builtin/echo.go`：

```go
package builtin

import (
    "context"
    "encoding/json"
    "fmt"

    "agentstudio.local/api/internal/domain"
)

type echoNode struct{}

type echoConfig struct {
    Prefix string `json:"prefix,omitempty"`
}

func NewEcho() *echoNode { return &echoNode{} }

func (*echoNode) Definition() domain.NodeDefinition {
    return domain.NodeDefinition{
        Type: "echo", Version: "1", Title: "Echo",
        Description: "为输入文本增加前缀", Category: "文本",
        ConfigSchema: json.RawMessage(`{
          "type":"object",
          "properties":{"prefix":{"type":"string","title":"前缀","x-ui-placeholder":"Echo: "}},
          "additionalProperties":false
        }`),
        Inputs: []domain.PortDefinition{{
            Key: "text", Title: "文本", Type: domain.TypeString,
            Required: true, Cardinality: domain.CardinalityOne,
        }},
        Outputs: []domain.PortDefinition{{
            Key: "text", Title: "文本", Type: domain.TypeString,
            Cardinality: domain.CardinalityOne,
        }},
    }
}

func (node *echoNode) Resolve(config json.RawMessage) (domain.ResolvedPorts, error) {
    var parsed echoConfig
    if err := decodeConfig(config, &parsed); err != nil {
        return domain.ResolvedPorts{}, err
    }
    definition := node.Definition()
    return domain.ResolvedPorts{Inputs: definition.Inputs, Outputs: definition.Outputs}, nil
}

func (*echoNode) Execute(_ context.Context, request domain.NodeRequest) (domain.NodeResult, error) {
    var config echoConfig
    if err := decodeConfig(request.Config, &config); err != nil {
        return domain.NodeResult{}, err
    }
    value, err := exactlyOneInput(request.Inputs, "text")
    if err != nil { return domain.NodeResult{}, err }
    text, ok := value.(string)
    if !ok { return domain.NodeResult{}, fmt.Errorf("%w: text", ErrInputTypeMismatch) }
    return domain.NodeResult{Outputs: map[string]any{"text": config.Prefix + text}}, nil
}
```

在 `apps/api/internal/nodes/builtin/register.go` 的 `RegisterCore` 列表中加入 `NewEcho()`：

```go
for _, node := range []nodes.NodeType{
    NewStart(), NewTemplate(), NewEcho(), NewCondition(), NewEnd(),
} {
    if err := registry.Register(node); err != nil { return err }
}
```

添加 `apps/api/internal/nodes/builtin/echo_test.go`：

```go
func TestEchoResolveAndExecute(t *testing.T) {
    node := NewEcho()
    ports, err := node.Resolve(json.RawMessage(`{"prefix":"回答："}`))
    if err != nil { t.Fatal(err) }
    if len(ports.Inputs) != 1 || ports.Inputs[0].Key != "text" { t.Fatalf("ports=%+v", ports) }

    result, err := node.Execute(context.Background(), domain.NodeRequest{
        Config: json.RawMessage(`{"prefix":"回答："}`),
        Inputs: map[string][]any{"text": {"你好"}},
    })
    if err != nil { t.Fatal(err) }
    if result.Outputs["text"] != "回答：你好" { t.Fatalf("outputs=%+v", result.Outputs) }
}
```

运行验证：

```bash
cd apps/api
CGO_ENABLED=0 go test ./internal/nodes/builtin -run TestEcho -v
CGO_ENABLED=0 go test ./... -count=1
```

## 扩展约束

- `type + version` 必须唯一；行为或契约不兼容时增加版本，不覆盖旧版本。
- 配置 Schema 应设置 `additionalProperties: false`，标题使用中文，并尽量使用标准 JSON Schema 约束。
- 动态端口必须由配置稳定推导；端口消失时前端会保留并标红旧连线，服务端编译器仍是最终裁决者。
- 节点只返回稳定、可序列化的数据；不要把密钥、Authorization 或底层异常写入输出和公开错误。
- 网络节点需沿用 SSRF 防护；长任务必须尊重 `context.Context` 取消和超时。
