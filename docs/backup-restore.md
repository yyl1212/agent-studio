# 实例备份与恢复

本手册面向本地 Agent Studio 实例的运维人员。备份是应用原生的 `.asbak` 归档：先在线创建，再离线检查，并只向空实例恢复。请把每次 dry-run 的安全摘要与备份文件一起留档。

## 能备份什么

当前 `v1alpha2` `.asbak` 包含 PostgreSQL 的全部业务数据：工作流、工作流版本、运行、节点运行、运行事件、加密运行载荷和工作流草稿检查点。因此，**备份包含完整运行输入、输出和节点错误详情**。其中 `run_payloads` 始终以数据库中的 AES-GCM 密文字节写入归档，恢复时原样保留；归档不会包含 `RUN_PAYLOAD_ENCRYPTION_KEY`。它也不包含环境变量、`.env`、模型或其他外部服务密钥、容器镜像、节点索引缓存和遥测后端配置。

创建和恢复从非空 `DATABASE_URL` 环境变量读取数据库连接；没有数据库 URL 参数。`inspect` 是离线操作，不读取 `DATABASE_URL`。

## 安全边界

`.asbak` 仍是敏感归档：私有运行载荷虽然保持应用层密文，但其他业务字段不是整包加密；创建的最终**文件权限为 0600，但备份包未加密**。只把它写入受访问控制的位置，并**使用加密磁盘或外部加密工具**保护静态文件及其副本；不要将归档、终端输出或解压内容提交到仓库、发送到不受控的聊天工具，或上传到未获批准的存储。

`backup create` 可以在当前 API 运行时获取一致性快照。恢复完全不同：必须停止 API 和所有直接写库的工具，并且**仅允许恢复到空实例**。维护锁会协调支持该协议的当前 Runtime，但无法识别不支持维护锁的旧进程，也无法阻止手工 SQL 写入。

恢复不会覆盖、合并或选择性导入已有数据；正式恢复也不是删除目标数据的工具。若目标不为空，请新建干净的目标实例或按组织的数据处置流程人工处理，切勿用恢复命令替代清理审批。

## 创建在线备份

先创建备份目录，并选择尚不存在的输出路径；命令不会覆盖已有文件。大型实例建议选择低写入时段，长时间一致性读取可能推迟 PostgreSQL 的旧版本清理。

```bash
mkdir -p ./backups
DATABASE_URL='postgres://user:password@host:5432/agent_studio?sslmode=disable' \
  agent-studio backup create --output ./backups/studio-20260829.asbak
```

也可使用本地 Docker 快捷入口（它使用 `TEST_DATABASE_URL`）：

```bash
make backup-create OUTPUT=./backups/studio-20260829.asbak
```

成功输出只包含归档路径、格式/迁移版本、记录数、字节数和摘要，不会打印连接串或运行正文。创建失败时删除未发布的临时文件；不要把中断产生的临时文件当作可恢复备份。

## 离线检查

在传输、归档或恢复前，先检查文件。此操作不连接数据库，适合在离线环境验证格式、固定条目、JSONL、记录数和 SHA-256 校验和。

```bash
agent-studio backup inspect ./backups/studio-20260829.asbak
agent-studio backup inspect --json ./backups/studio-20260829.asbak
```

本地快捷入口：

```bash
make backup-inspect BACKUP=./backups/studio-20260829.asbak
```

检查成功不代表目标数据库一定可恢复；目标的维护锁、迁移版本、空实例状态和引用关系由下一步 dry-run 验证。不要用通用 ZIP 工具修改 `.asbak`。

## 在空实例执行 dry-run

在目标数据库上执行 dry-run。它完整检查归档、取得独占维护锁、检查兼容性和业务表是否为空，并在回滚事务中验证引用关系；它不会写入业务数据、迁移记录或持久表。

```bash
DATABASE_URL='postgres://user:password@target-host:5432/agent_studio?sslmode=disable' \
  agent-studio backup restore --dry-run ./backups/studio-20260829.asbak
```

本地快捷入口：

```bash
make backup-restore-dry-run BACKUP=./backups/studio-20260829.asbak
```

保存 dry-run 的安全摘要，其中会列出归档/目标迁移版本、待执行迁移、恢复顺序、表计数、未压缩字节数和 `target-empty: true` 结论。若目标尚无 schema，dry-run 只计算迁移计划；不要在 dry-run 中期待创建表。

## 正式恢复

正式恢复是不可逆的数据库写入。执行前按以下固定清单逐项完成：

1. 用 `inspect` 验证归档校验和。
2. **停止所有旧版本 API**、当前 API、在线备份进程和其他会写入目标库的 SQL 客户端。
3. 确认目标业务表为空；如果 schema 已存在，可使用只读计数检查：

   ```bash
   DATABASE_URL='postgres://user:password@target-host:5432/agent_studio?sslmode=disable'
   export DATABASE_URL
   psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -c 'SELECT (SELECT count(*) FROM workflows) AS workflows, (SELECT count(*) FROM workflow_versions) AS workflow_versions, (SELECT count(*) FROM runs) AS runs, (SELECT count(*) FROM node_runs) AS node_runs, (SELECT count(*) FROM run_events) AS run_events, (SELECT count(*) FROM run_payloads) AS run_payloads, (SELECT count(*) FROM workflow_draft_checkpoints) AS workflow_draft_checkpoints;'
   ```

   若 schema 尚不存在，保持目标为空并让 dry-run 计算迁移计划；不要预先写入业务数据。
4. 执行 dry-run，并归档其安全摘要。
5. 仅在以上检查完成后执行正式确认命令：

   ```bash
   DATABASE_URL='postgres://user:password@target-host:5432/agent_studio?sslmode=disable' \
     agent-studio backup restore --confirm-empty-instance \
     ./backups/studio-20260829.asbak
   ```

6. 启动当前 API，检查就绪状态、工作流、版本和运行历史。
7. 重新输入外部环境密钥；这些密钥从未包含在 `.asbak` 中。

本地 Docker 入口还要求显式的 Make 确认值；仍须先人工完成上述清单：

```bash
make backup-restore BACKUP=./backups/studio-20260829.asbak CONFIRM=empty-instance
```

恢复前会检查归档、锁、兼容性、引用关系和空实例状态。业务数据在单个事务内导入：失败会回滚业务数据，但必要的空 schema 迁移可能已经提交。恢复命令不会删除 Docker volume，也不会替你清空目标库。

## 恢复后验证

启动当前 API 后，确认服务就绪：

```bash
curl -fsS http://127.0.0.1:8080/readyz
```

然后在 Studio 或只读数据库查询中核对工作流、发布版本、运行历史、节点运行和事件计数是否与 dry-run 摘要一致。检查归档中的迁移版本：当前 Runtime 支持同版本恢复以及受支持旧归档向前恢复；归档或目标版本高于当前 Runtime 时必须升级 Runtime，而不是尝试降级恢复。

保留原始 `.asbak`、`inspect` 输出与 dry-run 摘要，直到已完成业务验收和下一次可验证备份。不要把成功恢复视为密钥已经恢复：外部服务连接和密钥需要由运维重新配置。

## 活动运行与旧 Runtime

`v1alpha2` 恢复会清空活动运行的 `lease_owner`/`lease_expires_at` 并把 fencing token 归零，避免沿用来源实例的执行权。受支持的旧 `v1alpha1` 归档中，`running` 会转换为 `recovery_required/legacy_active_run` 交由人工处理，`cancelling` 会保留取消意图。当前已发布恢复流程**不会续跑活动运行**，也不会自动重放可能产生模型费用、Webhook 或其他外部副作用的节点。

维护锁只能约束实现该协议的当前版本。恢复前务必人工停止所有旧 Runtime；恢复窗口内不要启动旧 API，也不要使用 psql、管理 GUI 或脚本修改目标数据库。

## 运行载荷密钥

`v1alpha2` 归档保存 `run_payloads` 密文但不保存密钥。若要让恢复后的排队中、运行中或等待人工恢复的运行继续处理，目标 API 与 Worker 必须配置来源实例相同的 `RUN_PAYLOAD_ENCRYPTION_KEY`；同时应通过受控的 Secret 备份渠道独立保存该密钥。`v1alpha1` 归档没有这些私有检查点，遗留活动运行只会进入 `legacy_active_run` 人工恢复。

恢复后使用错误密钥不会降级为明文或静默执行。Worker 会把受影响运行置为 `recovery_required/payload_unavailable`，管理端不允许确认重试，只能终止后用可用输入重新提交。此时应停止 Worker，核对 Secret 来源和部署版本；如果能找回原密钥，恢复相同密钥后再启动同版本 Worker。不要反复尝试随机密钥，也不要修改归档中的密文字节。

轮换密钥前必须先让所有活动运行进入终态并创建可验证备份。本版本不提供多密钥 keyring 或在线重加密，不能在队列未清空时直接替换密钥。

## 错误码与处理

CLI 以稳定错误码返回失败（用法错误退出码为 2，其余备份/恢复失败为 1）。错误输出刻意不包含数据库 URL、密码、归档正文或运行输入输出。

| 错误码 | 含义与安全处理 |
| --- | --- |
| `BACKUP_ARCHIVE_INVALID` | 归档容器、条目、路径、JSONL 或计数不合法。停止恢复，重新取得受信任的备份。 |
| `BACKUP_CHECKSUM_MISMATCH` | Manifest、数据条目或数据集摘要不一致。不要重试导入；重新传输或重新创建并检查归档。 |
| `BACKUP_FORMAT_UNSUPPORTED` | 当前 Runtime 不支持该归档格式。使用仍支持该格式的 Runtime 或按发布说明升级。 |
| `BACKUP_RUNTIME_TOO_OLD` | 归档或目标的迁移版本高于当前 Runtime。升级当前 Runtime，不要降级目标。 |
| `BACKUP_SCHEMA_NOT_CURRENT` | 创建源库未处于当前 Runtime 最新迁移。按常规升级流程停止旧 API、迁移并验证后重新创建备份。 |
| `BACKUP_SIZE_LIMIT_EXCEEDED` | 归档超过格式资源边界。不要尝试绕过限制；评估受支持的升级或拆分迁移方案。 |
| `BACKUP_API_RUNNING` | 无法取得恢复独占锁，通常仍有当前 API 或在线备份运行。确认并停止所有相关进程后重新执行 dry-run。 |
| `BACKUP_TARGET_NOT_EMPTY` | 目标任一业务表已有数据。不要覆盖或合并；改用干净实例。 |
| `BACKUP_REFERENCE_INVALID` | 归档含重复主键、缺失/跨工作流引用或运行父链问题。停止导入，使用受信任备份。 |
| `BACKUP_CREATE_FAILED` | 一致性读取、归档写入或原子发布失败。检查磁盘空间、目录权限和源库状态，然后使用新输出路径重试。 |
| `BACKUP_RESTORE_FAILED` | 连接、迁移、导入、约束、计数或提交失败。保留安全错误码和 dry-run 摘要，确认目标仍为空后排查。 |

## 自动化和保留策略边界

首版只提供 CLI 与 Makefile 快捷入口，不提供内置定时执行、保留策略、远端对象存储、增量备份或 Web 管理界面。可用组织现有调度和加密存储在外部编排 `create` 与 `inspect`，但外部作业仍应保存检查结果、保护凭据，并在正式恢复前执行本手册的人工清单。

不要把归档格式当作稳定的通用 ZIP 协议：当前创建格式是 `agent-studio.dev/backup/v1alpha2`，当前 Runtime 仍显式兼容 `v1alpha1/migration 6` 导入。每次升级后，应通过创建、检查和 dry-run 验证当前 Runtime 与备份的兼容性。
