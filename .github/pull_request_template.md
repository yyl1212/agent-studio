## 变更

- 解决的问题：
- 方案与边界：

## 验证

- [ ] 新行为先有失败测试或明确的配置验证基线
- [ ] `make verify-quick` 通过
- [ ] 相关数据库、SDK 或 Playwright 回归已运行
- [ ] 生成文件由生成器更新且无漂移
- [ ] 涉及发布配置时，`make verify-release` 与 `make verify-workflows` 通过

## 兼容与安全

- [ ] 未提交密钥、个人数据或未脱敏日志
- [ ] 已说明 SDK、工作流、数据库和环境变量影响
- [ ] 文档已随行为变化更新
- [ ] 发布 Action 使用不可变 commit SHA，制品签名/公证边界已准确说明
