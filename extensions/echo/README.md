# Echo

该节点只依赖 `github.com/yyl1212/agent-studio/sdk/go/agentnode`，用于返回带可选前缀的输入文本。

```bash
CGO_ENABLED=0 go run ./cmd/agent-studio node test ./extensions/echo
CGO_ENABLED=0 go run ./cmd/agent-studio generate
make dev-api
```
