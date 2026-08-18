# Webhook 节点

`extension.webhook@1.0.0` 用于向运维侧预先配置的 HTTP 服务发送受约束的 JSON `POST` 请求。

## 配置

节点配置只接受两个字段：

- `path`：必填的相对路径，例如 `v1/orders`。
- `timeoutMs`：可选，范围为 1～30000 毫秒，默认 5000 毫秒。

服务启动前由运维配置：

- `AGENT_STUDIO_WEBHOOK_URL`：必填的 HTTP/HTTPS 基地址。
- `AGENT_STUDIO_WEBHOOK_TOKEN`：可选。设置后，节点以 `Authorization: Bearer <token>` 请求头发送。

工作流作者不能覆盖基地址、请求方法、请求头或令牌。`path` 禁止绝对地址、查询参数、片段、反斜杠、父目录跳转以及对应的多重 URL 编码形式。

## 输入与输出

输入端口 `body` 必须且只能接收一个 JSON 值。节点固定发送 `Content-Type: application/json`，请求体上限为 1 MiB。

成功响应输出：

- `status`：HTTP 状态码。
- `body`：解析后的 JSON；空响应或 `204` 输出 `null`。

响应体上限为 1 MiB。节点不跟随重定向。若响应 JSON 中出现与运维令牌相同的字符串片段，会在返回工作流前递归替换为 `[REDACTED]`。

## 状态与错误映射

- `2xx`：成功。
- 普通 `4xx`：映射为不可重试的上游拒绝错误。
- `408`、`429`、`5xx`、超时、重定向和网络失败：映射为可重试的上游错误。
- 调用方取消：映射为取消错误。

错误只暴露稳定的公开分类和消息，不透传底层网络地址、环境变量值、响应体或令牌。

## 安全边界

该节点声明 `network` 与 `secrets` capability，用于编译和策略检查；capability 本身不是进程级网络沙箱。基地址必须由可信运维人员配置，并应配合运行环境的出口网络策略使用。
