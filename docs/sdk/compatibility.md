# SDK v0.5 兼容性策略

本文冻结 Agent Studio Go SDK v0.5 的兼容边界。

## 协议与 SDK 版本

- v0.5 使用 API Version `agent-studio.dev/v1alpha1`。
- 相同 API Version 内新增可选 JSON 字段属于兼容变更，旧宿主必须忽略未知可选字段。
- 删除字段、修改已有枚举值语义、改变 `Resolve`/`Execute` 生命周期或端口解释方式属于不兼容变更，必须提升 API Version。
- Go SDK 在 v1.0 前允许 minor 版本包含编译期变更，但发布时必须提供迁移说明；patch 版本不得故意破坏对外 API。

扩展在加载 manifest 或连接宿主前，应使用 `agentnode.SupportsAPIVersion` 拒绝未知协议版本。

### 应用构建版本

SDK 版本与应用构建版本独立：

- `agentnode.Version = 0.5.0` 表示公开 Go SDK 契约。
- `agentnode.APIVersion = agent-studio.dev/v1alpha1` 表示节点协议。
- 本地源码构建显示 `0.5.0-dev`。
- 从 tag 安装的 CLI 显示对应模块版本，例如 `v0.5.0-rc.1`。通过 Go Module 缓存安装时，构建信息可能不包含 VCS revision；此时应以模块版本、SDK 版本和 API Version 为准，原生发布附件则会写入精确提交。

应用构建版本变化不自动改变 SDK 或节点协议；只有公开契约变化时才按
本文件的兼容规则提升 SDK/API 版本。

## 节点版本

节点的 `Type + Version` 标识一份工作流可固定引用的行为契约。同一版本可以修复不改变输入、输出和语义的缺陷；删除端口、改变配置含义或改变结果类型时必须发布新的节点版本，并保留旧版本供已有工作流执行。

### Runtime Core 内置节点

`llm@2` 是 Agent Studio Runtime Core 的内置节点版本，不是公开 Go SDK 或 Node API 的新版本。它不会改变 `agent-studio.dev/v1alpha1`、第三方节点的 `Resolve`/`Execute` 生命周期或现有 `llm@1` 行为。Go SDK v0.5 继续保留 `ExecutionSafety` 交互契约，而不是把 `llm@2` 作为新的 Node API。

工作流和模板始终按 `Type + Version` 精确匹配节点。包含 `llm@2` 的模板导入到未注册该版本的旧 Runtime 时必须报告节点类型或版本缺失并阻止导入；宿主不得静默替换为 `llm@1`、丢弃结构化字段或改写动态端口。

## Manifest 与 Go SDK

扩展 manifest 和 Go SDK 分别版本化。manifest 声明宿主发现、身份、打包信息和 Runtime 半开兼容区间，Go SDK 定义进程内节点协议。v0.5 节点包必须显式声明一个覆盖 v0.5 Runtime 的区间；仓库根目录官方 manifest 使用 `[v0.2.0, v0.6.0)`。CLI 必须拒绝未知 manifest API Version 或不兼容 Runtime，不能因为 Go 包可以编译就默认兼容，也不会自动改写、下载或安装节点包。

v0.5 的 durable execution、backup schema 和 runtime 状态属于宿主运行能力：它们不自动改变第三方 Node API 生命周期，也不改变 `agent-studio.dev/v1alpha1` 的 `Resolve`/`Execute` 契约。节点包如需依赖恢复、备份或队列状态，仍应通过明确的宿主能力与升级说明协商，不能把 Runtime 的实现变化解释为 Node API 变更。

## 安全边界

在当前 v0.5 边界内，Capability 只是用于展示和审计的声明元数据，不是权限授予或沙箱，也不提供进程、网络、密钥或文件系统隔离。节点仍与 API 运行在同一进程中；宿主部署者必须只加载可信代码，并使用容器、操作系统权限和网络策略建立真正的隔离边界。

`Definition.executionSafety` 是同一 API Version 内新增的可选字段。使用旧 SDK 编译、未携带该字段的节点仍可被新宿主加载，但有效等级会保守设为 `side_effect`，从历史节点局部重跑时必须进行副作用确认；空值和未来未知值采用相同默认。节点升级安全声明时必须继续遵循“按最危险可能行为声明”的原则。

执行安全等级是交互和确认契约，不构成进程沙箱、权限边界或对节点行为的技术保证，也不能替代 Capability、容器权限和网络策略。

扩展不得导入 `apps/api/internal`。该目录没有兼容承诺，且 Go 的 internal 导入规则会阻止仓库外代码依赖它。公开扩展只应导入 `sdk/go/agentnode`，测试可额外导入 `sdk/go/agenttest`。
