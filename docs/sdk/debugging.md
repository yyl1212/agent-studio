# 节点开发排错

先从仓库根目录运行：

```bash
CGO_ENABLED=0 go run ./cmd/agent-studio doctor
CGO_ENABLED=0 go run ./cmd/agent-studio node test ./extensions/<节点名>
```

命令会保留 Go 工具的原始错误输出。可按以下稳定错误片段检索原因：

| 错误片段 | 原因 | 处理 |
|---|---|---|
| `unsupported apiVersion` | manifest 版本不兼容 | 把 `agent-studio.nodes.yaml` 的版本改为 `agent-studio.dev/v1alpha1`。 |
| `duplicate package` | manifest 重复登记同一个包 | 删除重复的 `package` 项，只保留一次。 |
| `GOPROXY=off` | 依赖不在当前 Module 或本地缓存，离线包校验无法完成 | 先在根 Module 显式加入并下载依赖，再重新运行 `node test` 或 `generate`；不要关闭离线预检。 |
| `directory is not empty` | `node init` 的目标目录已存在内容 | 更换节点名称，或人工审查并处理旧目录；命令不会覆盖文件。 |
| `duplicate node type` | 两个节点使用相同的 `Type + Version` | 修改新节点的 `Type` 或 `Version`，重新测试并生成。 |
| `encoded outputs` | 契约测试编码后的输出超过 SDK 上限 | 缩小输出，删除无关内容，并改用结构化字段；不要返回完整响应或二进制正文。 |

## Webhook 稳定错误

| 错误 | 原因 | 处理 |
|---|---|---|
| `internal/missing_webhook_configuration` | URL 缺失或非法 | 检查 API 进程环境中的 http/https 基地址。 |
| `config/invalid_config` | path 或 timeout 非法 | 使用不含 host、query、fragment、`..` 的相对路径，以及 1–30000 毫秒超时。 |
| `input/invalid_body` | 类型、基数、编码或请求体超限 | 传入一个不超过 1 MiB 的 JSON 值。 |
| `input/webhook_rejected` | 普通上游 4xx | 修正业务请求；节点不会输出上游响应正文。 |
| `temporary/upstream_failed` | 超时、429、5xx、重定向、响应超限或非法 JSON | 检查受信任上游后重试。 |

这些错误不会包含 Webhook 基地址、Token、请求正文或底层网络错误。URL 留空不影响 API 启动；执行对应节点时才返回缺少配置错误。

## 常见流程问题

### 修改 manifest 后生成失败

确认 `package` 是当前 Go Module 中可导入的包路径，包已存在且能通过：

```bash
CGO_ENABLED=0 GOPROXY=off go list -mod=readonly <package>
```

生成器在全部包校验成功前不会替换旧生成文件。修复 manifest 或依赖后再次运行：

```bash
CGO_ENABLED=0 go run ./cmd/agent-studio generate
```

### 节点测试通过但画布中不存在

先运行 `generate`，确认生成文件已变化；再重启 API。可请求 `GET /api/node-types`，检查节点的 `type` 和 `version`。API 启动时遇到非法 Definition 或重复节点会安全退出，应先查看启动日志。

### 配置表单没有预期字段

画布表单来自 `Definition.ConfigSchema`。检查 Schema 的 `properties`、`title`、`required` 与 `additionalProperties`，并用 `agenttest.Contract` 同时覆盖有效和无效配置。

### 连线端口与配置不一致

`Resolve` 必须只根据配置稳定推导端口，不得依赖时间、网络或外部可变状态。修改端口后重新运行契约测试，并重新保存或调整画布连线。
