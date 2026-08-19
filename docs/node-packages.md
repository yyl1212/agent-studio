# 节点包开发与兼容检查

Agent Studio 的节点包把同一个 Go Module 内的一组节点声明为可核验的软件包。构建工具会离线读取 Module 根目录的 `agent-studio.node-package.json`，检查 Node API、Runtime、SDK 与实际注册节点是否一致；API 启动时先在临时注册表完成整包校验，再原子提交，避免只注册一部分节点。

## 创建节点包

在一个已经初始化 `go.mod` 的 Module 根目录运行：

```bash
CGO_ENABLED=0 go run ./cmd/agent-studio node package init \
  --display-name "示例节点" \
  --license "Apache-2.0" \
  --repository "https://github.com/example/agent-nodes" \
  --runtime-min "v0.2.0" \
  --runtime-max-exclusive "v0.4.0"
CGO_ENABLED=0 go run ./cmd/agent-studio node init echo
CGO_ENABLED=0 go run ./cmd/agent-studio node test ./extensions/echo
CGO_ENABLED=0 go run ./cmd/agent-studio node inspect example.com/project/extensions/echo
CGO_ENABLED=0 go run ./cmd/agent-studio generate
```

请把示例中的 Module 路径、仓库地址和版本范围替换为项目的真实值。`node package init` 创建 Module 根清单；`node init` 同时更新本地安装清单和节点包 registration；`node test` 运行公共契约测试；`node inspect` 输出经清理的兼容诊断；`generate` 生成确定性的包级注册代码。

## 安装第三方节点包

当前版本不提供远程索引或自动安装。维护者必须先审查来源，再在 Agent Studio 源码仓库中手工执行明确版本的 `go get example.com/nodes@v1.2.3`，把需要的 registration 写入 `agent-studio.nodes.yaml`，最后运行 `CGO_ENABLED=0 go run ./cmd/agent-studio generate` 并提交 `go.mod`、`go.sum`、安装清单和生成物。生成和检查阶段使用 `GOPROXY=off`、`go list -mod=readonly`，不会下载依赖或修改 Module 文件。

节点与 API 在同一个进程运行，可以访问宿主进程拥有的资源。Manifest 中的 capability 只用于展示和审计，不是沙箱；只安装可信源码，并用容器权限、只读文件系统、网络策略和密钥最小授权建立真正的边界。

## 清单与发布边界

- 清单固定放在 Go Module 根目录，名称为 `agent-studio.node-package.json`，`apiVersion` 为 `agent-studio.dev/v1alpha1`。
- `compatibility.runtime` 是半开区间：`minVersion` 包含，`maxVersionExclusive` 不包含。
- 清单只声明包身份、兼容范围和预期节点集合；Schema、端口、capability 与执行行为始终来自实际 Go 节点实现。
- 本地 `replace` Module 可用于开发，但其来源不可作为可发布节点包。发布前应移除本地替换，使用可解析的正式 Module 版本重新检查和生成。
- 检查失败时不会部分注册节点；修复所有诊断后再启动 API。

## 工作流模板中的节点包提示

新导出的工作流模板使用 `agent-studio.dev/v1alpha2`，并在 `spec.nodePackages` 中记录导出环境使用的节点包与节点类型。该信息仅用于导入预览中的兼容性提示，不是权威安装清单：它不会触发下载、`go get`、URL 打开、代码加载，也不能绕过编译器的真实节点与端口校验。

导入方应根据缺失包提示人工核对包名、版本和许可证，完成源码审查及手工安装，再重新预览模板。模板仍兼容旧的 v1alpha1；导入后再次导出会升级为 v1alpha2。

## 常用排查

```bash
CGO_ENABLED=0 go run ./cmd/agent-studio doctor
CGO_ENABLED=0 go run ./cmd/agent-studio node inspect example.com/project/extensions/echo
CGO_ENABLED=0 go run ./cmd/agent-studio generate
git diff --exit-code -- apps/api/internal/generated/nodes_gen.go
```

`doctor` 汇总 Runtime、SDK 和已安装包诊断。公开错误与 JSON 输出只包含稳定诊断，不暴露本地绝对路径、`replace` 目标或完整 `go list` 输出。
