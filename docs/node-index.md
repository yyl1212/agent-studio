# 官方节点包索引

Agent Studio 自带一个只读、Git 驱动的官方节点包索引，用于发现节点包和判断其声明的 Runtime / Node API 兼容性。索引只提供经过审核的固定来源元数据，不自动下载、安装、启用或执行第三方代码。

> 收录表示索引元数据已经审核，不代表节点包代码安全。安装和执行前，仍须人工审核源码、固定 Tag、Commit、依赖和节点能力。

## 发布边界

Agent Studio 的 `v0.3.0` Release 与官方节点索引 Release 相互独立。Agent Studio 发布不会自动收录任何节点包；只有节点包经独立的、stable 且 immutable 的索引 Release 审核后才可进入索引。索引中的“已审核”仅说明元数据与固定来源通过收录检查，绝不是代码安全认证，也不改变“不自动下载、安装、启用或执行”的边界。

## 离线快照与状态

当前程序内嵌官方索引仓库不可变 Release `v0.1.0` 的 `index.json`，因此首次运行和 GitHub 不可用时仍可离线查询。内嵌快照来自 [agent-studio-node-index v0.1.0](https://github.com/yyl1212/agent-studio-node-index/releases/tag/v0.1.0)；来源、文件名和固定 SHA-256 记录在 `contracts/node-index-source.json` 与 `contracts/node-index-source.checksums`。

查看当前来源和兼容包数量不会联网，也不会创建缓存目录：

```bash
agent-studio node index status
agent-studio node index status --json
```

`source` 为 `embedded` 表示正在使用程序内嵌快照，`cache` 表示正在使用本地刷新缓存。`stale: true` 只表示仍在使用 embedded 快照或本地缓存存在加载警告，不表示程序已经查询过远端是否有更新。

## 显式刷新

只有以下命令会访问网络和写入节点索引缓存：

```bash
agent-studio node index refresh
agent-studio node index refresh --json
```

刷新固定访问官方 GitHub 仓库的最新完整公开 Release，要求 Release 为 stable、immutable，且只接受摘要与大小一致的 `index.json`。成功结果为 `updated OLD -> NEW` 或 `already current VERSION`。失败会保留原有效缓存；程序不会后台刷新，也不会从 API 服务或 Web 页面发起刷新。

GitHub 请求不携带 Token、Cookie 或其他本机凭据，使用匿名配额。遇到 `INDEX_RATE_LIMITED` 时请稍后重试；不要把个人 Token 写入命令参数或配置文件。

## 搜索和详情

空查询列出与当前 Runtime 兼容的包；搜索和详情都只读取本地缓存或 embedded 快照：

```bash
agent-studio node search
agent-studio node search echo
agent-studio node search --category integration --category utility
agent-studio node search --all --json webhook

agent-studio node info github.com/example/node-package
agent-studio node info --version v1.2.0 github.com/example/node-package
agent-studio node info --json github.com/example/node-package
```

默认搜索只显示具有当前兼容推荐版本的包。`--all` 也显示不兼容包，但只 withdrawn 的包仍只在精确详情中出现。推荐只考虑 `approved`、`active`、无 prerelease、Node API 精确相同且 Runtime 位于声明半开区间内的版本。

CLI 会显示 Module、展示名、许可证、推荐版本和兼容声明；详情还显示固定仓库、Tag、Commit、manifest digest、审核记录、生命周期和节点类型。命令刻意不输出可直接执行的安装命令。

## Web 节点包目录

启动 API 和 Web 后，从顶部主导航进入“节点包”，或直接打开 `http://localhost:5173/node-packages`。这个聚焦式单栏页面只调用本机 API，API 只读取当前 cache 或 embedded 快照；浏览、搜索和展开版本详情都不会访问 GitHub。

目录支持以下筛选，并把状态保存在 URL 中，刷新页面或浏览器前进、后退后仍可恢复：

- 搜索名称、描述、关键字和节点类型；输入停止 250 ms 后执行查询。
- 可同时选择多个分类，多个分类使用 OR 语义。
- 默认仅显示具有当前 Runtime / Node API 兼容推荐版本的包；关闭“仅显示兼容包”可查看无兼容推荐的条目。
- 列表每页 50 个包；改变搜索、分类或兼容条件会回到第一页。

状态提示“当前使用内置索引”时，API 正在读取随程序发布的 embedded 快照；提示“当前使用本地缓存索引”时，API 正在读取配置目录中的有效 `index.json`。若本地缓存损坏或不可读，页面会显示加载警告，API 使用上一个有效快照或回退到 embedded 快照。`stale` 和 warning 只反映本地状态，不表示页面查询过远端更新。

Web 目录没有刷新或安装按钮，也不会在后台联网。需要更新索引时，只能在与 API 共享缓存目录的环境中显式运行：

```bash
agent-studio node index refresh
```

页面展示的“已审核”、推荐版本和兼容结果只说明索引元数据及声明通过了收录检查，不代表第三方代码安全，也不会自动下载、安装、启用或执行节点包。展开详情后，应记录固定仓库、Tag、源码 Commit、manifest digest、索引审核 Commit、生命周期和节点类型，再按本文“安装前人工审核”完成源码与依赖审查。

## 缓存目录

默认缓存文件为 Go 用户缓存目录下的 `agent-studio/node-index/index.json`：

- macOS 通常为 `~/Library/Caches/agent-studio/node-index/index.json`；
- Linux 通常为 `${XDG_CACHE_HOME:-~/.cache}/agent-studio/node-index/index.json`。

可用环境变量指定绝对、规范化目录：

```bash
export AGENT_STUDIO_NODE_INDEX_CACHE_DIR=/srv/agent-studio/node-index
agent-studio node index refresh
```

只读节点索引 API 与 CLI 要共享刷新结果时，两者必须指向同一个目录。Docker 部署可预先把同一个持久卷挂载到固定容器路径，并为 CLI 与 API 设置相同变量。例如：

```yaml
services:
  api:
    environment:
      AGENT_STUDIO_NODE_INDEX_CACHE_DIR: /var/lib/agent-studio/node-index
    volumes:
      - node-index:/var/lib/agent-studio/node-index

volumes:
  node-index:
```

刷新时，应使用包含 `agent-studio` CLI 的同版本容器并挂载同一个 `node-index` 卷。API 不负责联网刷新或写入该目录；仅 `node index refresh` 创建目录并原子替换 `index.json`。

缓存目录只配置给 API/CLI，不应传给浏览器。Web 只接收经过 API 规范化的索引 DTO，不会看到宿主机或容器内的缓存绝对路径。

## 损坏恢复

缓存被截断、手工篡改或替换为非法文件时，Store 不会把无效内容用于查询：运行中的进程保留上一个有效快照，新进程回退到 embedded 快照，并通过 `status` 的 `warningCode` 报告问题。

通常直接再次执行刷新即可修复：

```bash
agent-studio node index status
agent-studio node index refresh
agent-studio node index status
```

刷新在下载、摘要校验、严格解析全部成功后才原子替换缓存；并发刷新会返回 `INDEX_REFRESH_IN_PROGRESS`，不会无锁写入。

## 安装前人工审核

准备使用某个收录版本前，至少完成以下检查：

1. 用 `node info --version` 记录仓库、固定 Tag、Commit、manifest digest、审核状态和生命周期。
2. 从详情显示的仓库获取源码，确认 Tag 精确解析到记录的 Commit，而不是只信任可移动的分支名。
3. 在固定 Commit 上检查 `agent-studio.node-package.json`、Go 依赖、注册节点、网络/文件/进程能力和构建脚本。
4. 对 manifest 重新计算 SHA-256 并与索引记录比对；检查仓库是否披露撤回、安全公告或所有权变化。
5. 在隔离环境运行节点契约测试，再按[节点包开发与兼容检查](node-packages.md)中的手工流程安装。

官方索引的审核是额外信号，不替代组织自身的供应链、安全和许可证审核。发现可疑条目或安全问题时，请按项目 [SECURITY.md](../SECURITY.md) 的私密渠道报告。

## 本地验证门禁

以下门禁完全离线：它运行索引核心和 CLI 测试，并校验 vendored Schema 与 embedded 快照的固定摘要。

```bash
make verify-node-index
```

更新 embedded 资产必须来自新的不可变官方索引 Release，并同步更新来源记录和摘要；不要从分支、PR artifact 或临时 URL 替换这些文件。
