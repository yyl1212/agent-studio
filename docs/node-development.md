# 节点扩展开发指南

Agent Studio 的普通节点由 Go 注册表驱动。节点通过 `Definition` 暴露配置 Schema 和静态端口，通过 `Resolve` 推导动态端口，通过 `Execute` 处理输入。画布从 `/api/node-types` 获取定义并渲染通用 Schema 表单，因此新增普通节点不需要修改前端。

## 第三方节点

第三方作者只使用公开包：

```go
import (
    "github.com/yyl1212/agent-studio/sdk/go/agentnode"
    "github.com/yyl1212/agent-studio/sdk/go/agenttest"
)
```

完整接口、Echo 示例、错误分类和取消规则见 [Go 节点 SDK API](sdk/api.md)，版本承诺见 [SDK 兼容性策略](sdk/compatibility.md)。建议先实现 `agentnode.Node`，再通过 `agenttest.Run` 验证配置、端口、输出和取消契约。

扩展节点必须使用命名空间 Type，例如 `acme.search`；版本使用 `1`、`1.0` 或 `1.0.0`。配置 Schema 应设置 `additionalProperties: false`，密钥只能使用环境变量名或宿主引用，不能提供明文 API Key、Token、Cookie 或 Authorization 字段。

## 内置节点维护

官方内置节点位于 `apps/api/internal/nodes/builtin`，但这里是宿主实现而不是第三方 SDK。维护内置节点时仍应直接实现 `agentnode.Node`，并在 `contract_test.go` 中加入 `agenttest.Contract`，不能通过内部 wrapper 绕过公开契约。

注册入口接受 `agentnode.Registrar`。核心节点保持 Start、Template、Condition、End 的顺序；集成节点保持 LLM、HTTP、Code 的既有注入与注册方式。

## 验证

从仓库根目录运行：

```bash
CGO_ENABLED=0 go test ./sdk/go/... -count=1
CGO_ENABLED=0 go test ./apps/api/internal/nodes/builtin -count=1
CGO_ENABLED=0 go test ./... -count=1
CGO_ENABLED=0 go vet ./...
```

发布前还应运行 `make verify` 和 `make test-e2e`，确认画布、Agent 页面和存量工作流兼容。
