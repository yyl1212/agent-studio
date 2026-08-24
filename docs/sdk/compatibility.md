# SDK 兼容性策略

本文冻结 Agent Studio Go SDK v0.3 的兼容边界。

## 协议与 SDK 版本

- v0.3 使用 API Version `agent-studio.dev/v1alpha1`。
- 相同 API Version 内新增可选 JSON 字段属于兼容变更，旧宿主必须忽略未知可选字段。
- 删除字段、修改已有枚举值语义、改变 `Resolve`/`Execute` 生命周期或端口解释方式属于不兼容变更，必须提升 API Version。
- Go SDK 在 v1.0 前允许 minor 版本包含编译期变更，但发布时必须提供迁移说明；patch 版本不得故意破坏已公开 API。

扩展在加载 manifest 或连接宿主前，应使用 `agentnode.SupportsAPIVersion` 拒绝未知协议版本。

### 应用构建版本

SDK 版本与应用构建版本独立：

- `agentnode.Version = 0.3.1` 表示公开 Go SDK 契约。
- `agentnode.APIVersion = agent-studio.dev/v1alpha1` 表示节点协议。
- 本地源码构建显示 `0.3.1-dev`。
- 从 tag 安装的 CLI 显示对应模块版本，例如 `v0.3.1`。

应用构建版本变化不自动改变 SDK 或节点协议；只有公开契约变化时才按
本文件的兼容规则提升 SDK/API 版本。

## 节点版本

节点的 `Type + Version` 标识一份工作流可固定引用的行为契约。同一版本可以修复不改变输入、输出和语义的缺陷；删除端口、改变配置含义或改变结果类型时必须发布新的节点版本，并保留旧版本供已有工作流执行。

### Runtime Core 内置节点

`llm@2` 是 Agent Studio Runtime Core 的内置节点版本，不是公开 Go SDK 或 Node API 的新版本。它不会改变 `agent-studio.dev/v1alpha1`、第三方节点的 `Resolve`/`Execute` 生命周期或现有 `llm@1` 行为；当前补丁 SDK 版本为 `agentnode.Version = 0.3.1`。

工作流和模板始终按 `Type + Version` 精确匹配节点。包含 `llm@2` 的模板导入到未注册该版本的旧 Runtime 时必须报告节点类型或版本缺失并阻止导入；宿主不得静默替换为 `llm@1`、丢弃结构化字段或改写动态端口。

## Manifest 与 Go SDK

扩展 manifest 和 Go SDK 分别版本化。manifest 声明宿主发现、身份和打包信息，Go SDK 定义进程内节点协议。CLI 必须拒绝未知 manifest API Version，不能因为 Go 包可以编译就默认 manifest 兼容。

## 安全边界

Capability 在 v0.3 只用于展示、审计和未来调度，不提供进程、网络、密钥或文件系统隔离。节点仍与 API 运行在同一进程中；宿主部署者必须只加载可信代码，并使用容器、操作系统权限和网络策略建立真正的隔离边界。

扩展不得导入 `apps/api/internal`。该目录没有兼容承诺，且 Go 的 internal 导入规则会阻止仓库外代码依赖它。公开扩展只应导入 `sdk/go/agentnode`，测试可额外导入 `sdk/go/agenttest`。
