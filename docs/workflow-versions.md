# 工作流版本治理 API

工作流版本治理用于查看不可变发布版本、比较草稿与历史版本，以及把某个历史版本安全恢复成新的草稿修订。当前交付范围是管理 API 和前端生成类型；Studio 中的版本历史面板将在后续 PR 提供。

以下示例统一使用：

```bash
API=http://localhost:8080
WORKFLOW_ID=11111111-1111-4111-8111-111111111111
```

## 四个管理 API

### 列出发布版本

版本按版本号倒序返回，`limit` 范围为 1–100，默认 20；有下一页时继续传服务端返回的 `nextCursor`。

```bash
curl "$API/api/workflows/$WORKFLOW_ID/versions?limit=20"
```

响应中的 `current` 表示当前线上 Agent 指向的发布版本。`rollbackCheckpoint` 非空时表示最近一次草稿回滚仍可撤销。

### 比较两个快照

快照可以是指定 `version` 的发布版本，也可以是指定 `draftRevision` 的当前草稿。可比较草稿与版本，也可比较两个发布版本。

```bash
curl -X POST "$API/api/workflows/$WORKFLOW_ID/version-diffs" \
  -H 'Content-Type: application/json' \
  -d '{
    "base":{"kind":"version","version":1},
    "compare":{"kind":"draft","draftRevision":7}
  }'
```

服务端按五组返回结构化差异：节点、开始参数、连线、Agent 页面配置和画布布局。结果顺序稳定，不提供原始 JSON patch。单次响应最多返回 500 条差异明细；汇总计数仍反映完整差异，发生截断时 `truncated=true`。单个前值或后值的 JSON 编码超过 4 KiB 时不返回内容，只标记 `valueOmitted: "too_large"`。

### 将发布版本恢复为新草稿

```bash
curl -X POST "$API/api/workflows/$WORKFLOW_ID/rollbacks" \
  -H 'Content-Type: application/json' \
  -d '{"targetVersion":1,"expectedDraftRevision":7}'
```

回滚先使用当前节点注册表编译目标版本，再在一个 PostgreSQL 事务中保存回滚前草稿检查点，并将目标版本的图和 Agent 页面配置写成 revision 8 的新草稿。它不会自动发布。

### 撤销最近一次草稿回滚

```bash
curl -X POST "$API/api/workflows/$WORKFLOW_ID/rollback-undo" \
  -H 'Content-Type: application/json' \
  -d '{"expectedDraftRevision":8}'
```

撤销把检查点中的图和 Agent 页面配置恢复为又一个新草稿修订，并删除检查点。系统只保留单级检查点：连续执行第二次回滚会覆盖第一次检查点；成功撤销后不能再次撤销。

## Revision 与并发语义

回滚和撤销都必须携带页面当前看到的正整数 `expectedDraftRevision`。服务端先锁定工作流行，再比较实际 revision：只有持有最新 revision 的请求可以成功；草稿已被其他请求保存时返回 `WORKFLOW_REVISION_CONFLICT`。客户端遇到冲突应重新加载工作流和版本列表，不应自动重试写操作。

回滚成功后，只要继续保存画布图或 Agent 页面配置，检查点就会在同一事务内失效；之后撤销返回 `ROLLBACK_UNDO_UNAVAILABLE`。修改名称或说明、查看、导出、测试运行和发布不会改动草稿内容，因此不会删除检查点。

## 安全与兼容边界

- 节点配置 Schema 中标为 `writeOnly` 或 `x-agent-studio-secret` 的字段、启发式识别出的凭据键，以及 HTTP 节点规则识别出的敏感 URL/Header，只返回变化路径和 `valueOmitted: "secret"`，不会返回前后值。
- 某一侧节点定义缺失时，配置差异只返回 `/config` 和 `valueOmitted: "definition_unavailable"`，不会猜测或展开原始配置。该版本仍可出现在列表中，但回滚会被当前 Compiler 阻断。
- 非法 JSON、错误 `schemaVersion`、重复节点/连线 ID、过深或过大的图，以及发布时输入 Schema 与当前重新派生结果不一致的历史快照，比较和回滚均以 `WORKFLOW_SNAPSHOT_UNSUPPORTED` 失败关闭。
- 回滚只更新当前草稿的图、Agent 页面配置与 `draft_revision`。它不修改 `published_version_id`，不修改或删除 `workflow_versions`，也不改写历史 Run；因此线上 Agent 继续运行原发布版本，直到用户通过既有发布流程显式发布新版本。
- 归档工作流仍可只读查看版本和差异，但不能回滚或撤销。恢复工作流后，如果 revision 与检查点仍匹配，检查点仍可使用。

所有请求体都采用严格 JSON 解码，未知字段、同一请求中的多个 JSON 值、非正版本号或 revision 均返回 `REQUEST_INVALID`。完整字段定义以 [OpenAPI 契约](../contracts/openapi.yaml) 为准。
