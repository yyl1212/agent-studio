# 可观测性运行手册

Agent Studio 可选启用 OpenTelemetry Metrics 与 Traces。默认配置不创建 Exporter、不探测网络，也不启动遥测导出后台任务；JSON 日志始终写入 API 标准输出，不通过 OTLP 导出。

## 启用配置

基础配置使用以下六个环境变量：

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | 空 | OTLP/HTTP 基础地址；留空即关闭遥测 |
| `OTEL_SERVICE_NAME` | `agent-studio-api` | 服务资源名称 |
| `OTEL_RESOURCE_ATTRIBUTES` | 空 | 逗号分隔的部署级资源属性 |
| `OTEL_EXPORTER_OTLP_TIMEOUT` | `5000` | 单次导出超时，单位毫秒 |
| `OTEL_EXPORTER_OTLP_COMPRESSION` | `gzip` | 只允许 `gzip` 或 `none` |
| `OTEL_METRIC_EXPORT_INTERVAL` | `10000` | Metrics 导出周期，单位毫秒 |

Endpoint 只接受不含 userinfo、query、fragment 的 HTTP(S) 地址，并允许路径前缀。应用分别追加 `/v1/traces` 与 `/v1/metrics`。资源属性最多 32 项，每项最长 256 字符；只能描述服务和部署环境，禁止写入用户、租户、工作流、节点、密钥或凭据。

标准 OpenTelemetry 认证和证书变量（例如 `OTEL_EXPORTER_OTLP_HEADERS`、signal-specific headers 与 certificate 变量）由部署环境直接提供。不要把值写入仓库、命令历史、工作流配置或运维文档；应用日志不会回显这些值。生产环境应优先使用平台 Secret 注入。

## 本地观测栈

启动固定版本的 Collector、Prometheus 与 Jaeger：

```bash
make observability-up
```

本机入口：

- Prometheus：`http://127.0.0.1:9090`
- Jaeger：`http://127.0.0.1:16686`
- Collector OTLP/HTTP：`http://127.0.0.1:4318`
- Collector 健康检查：`http://127.0.0.1:13133/`

这些服务属于 Compose `observability` profile，只绑定回环地址，不依赖 PostgreSQL，也不挂载 PostgreSQL 数据卷。Collector 仅包含 Metrics 和 Traces pipeline，没有 Logs pipeline。

停止观测服务：

```bash
make observability-down
```

该命令只执行 `stop`，不会停止数据库或删除 volume。检查 Compose 和 Collector 配置：

```bash
make observability-check
```

执行真实 API、Prometheus、Jaeger 闭环验证：

```bash
make observability-verify
```

验证脚本会临时启动 API，执行成功与失败工作流，查询 Run、Node、HTTP 指标和链路，并验证 Collector 停止后业务仍能成功。脚本退出时保留 PostgreSQL。

## 查询与关联

Prometheus 可查询以下基础指标：

- `agent_studio_http_server_requests_total`
- `agent_studio_workflow_runs_total`
- `agent_studio_workflow_node_executions_total`
- `agent_studio_postgres_pool_connections`

独立 Worker 还提供以下耐久运行指标（Prometheus 会把点号规范化为下划线）：

- `agent_studio_worker_queue_depth` 与 `agent_studio_worker_oldest_queued_age_seconds`：队列深度和最老排队时长；
- `agent_studio_worker_run_claim_total`、`agent_studio_worker_claim_latency_seconds`：领取结果与耗时；
- `agent_studio_worker_active_leases`、`agent_studio_worker_lease_renew_total`：活动 lease 和续租结果；
- `agent_studio_worker_expired_lease_reclaim_total`、`agent_studio_worker_fencing_rejected_total`：过期租约接管和旧 token 写入拒绝；
- `agent_studio_worker_auto_recovery_total`、`agent_studio_worker_run_recovery_required_total`：自动恢复与人工恢复；
- `agent_studio_worker_payload_decrypt_failure_total`：按有限类别统计的载荷解密失败。

在 Jaeger 中选择配置的 `OTEL_SERVICE_NAME`，可看到 `HTTP ...`、`workflow.run` 与 `workflow.node`。同步测试运行沿 W3C Trace Context 形成父子链；异步 Agent Run 使用新根 span，并通过 Link 关联原请求。JSON 日志仅在已有值时写入 `requestId`、`traceId`、`spanId`、`run_id` 与 `node_id`，便于跨信号定位。

## 数据安全边界

日志、span、metric 与 resource 禁止记录以下内容：

- HTTP 原始 path、query、body、header；
- workflow input/output、Prompt、节点 config；
- SQL、数据库地址、密钥、认证 header；
- `err.Error()`、NodeError details、错误链类型、panic value；
- 将 requestId、runId、nodeId、workflowId、slug 或错误码作为 metric 标签。

Span 只记录固定名称、有限错误类别和允许的 ID；Metric 只使用代码内白名单低基数标签。Collector 或导出目标不可达时允许丢失遥测，但不得改变 HTTP、Run、Node 或持久化结果。

## 故障处理

| 现象 | 业务语义 | 检查方式 |
|---|---|---|
| Endpoint 留空 | 遥测完全关闭，业务正常 | 检查 API 环境变量 |
| Endpoint 配置非法 | API 拒绝启动 | 检查 scheme、host、compression 和毫秒配置 |
| Collector 暂时不可达 | API 正常启动和处理请求，后台安全限频记录导出失败 | `make observability-up`，再检查 Collector 日志 |
| Prometheus 没有数据 | 不影响业务 | 检查 Collector metrics pipeline 和 scrape target |
| Jaeger 没有链路 | 不影响业务 | 检查 Collector traces pipeline 和服务名 |
| 关闭时遥测超时 | 业务关闭继续，记录固定 `telemetry/shutdown` 类别 | 检查 Collector 可达性和导出超时 |
| 队列深度或最老排队时间持续上升 | Worker 未启动、容量不足或领取失败 | 检查 Worker 日志、`run_claim_total{outcome="error"}` 和数据库连接 |
| lease 续租失败或 fencing 拒绝增加 | Worker 卡顿、数据库中断或旧 Worker 恢复写入 | 检查 `lease_renew_total`、进程时钟与数据库延迟；不要放宽 fencing |
| 人工恢复持续增加 | Worker 故障发生在只读/副作用节点，或私有载荷不可用 | 在管理端核对恢复原因；检查密钥与节点版本，不要自动批量重试副作用 |
| Worker drain 超时 | 节点未在关闭窗口内响应取消 | 检查 `WORKER_SHUTDOWN_TIMEOUT` 和节点实现；等待 lease 到期后由新 Worker 安全接管 |

API 先完成业务关闭、注销连接池指标，再使用独立且最长 5 秒的 Context flush 遥测，最后关闭 Store。Worker 收到终止信号后进入 drain：停止领取新运行，在 `WORKER_SHUTDOWN_TIMEOUT` 内等待活动运行，并持续维护租约；超时后退出，由租约到期与 fencing 保证后续接管安全。Runtime shutdown 幂等；启动中途失败也会关闭已创建的 Provider。

## 生产责任

本地 profile 只用于开发和发布前验证。生产部署必须另外设计并验证：

- Collector 与后端的 TLS、认证、Secret 轮换和网络策略；
- Collector 高可用、容量、队列、重试与资源限制；
- Prometheus/Trace 后端的保留期、备份、访问控制和成本；
- 告警规则、SLO、值班流程与故障演练；
- 采样策略。当前应用侧启用后为全量采集，Tail Sampling 不能降低应用到 Collector 的出口流量；应用侧 sampler 治理属于后续阶段。
