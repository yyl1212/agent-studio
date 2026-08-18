# Retriever

Retriever 是一个完全本地、确定性的 SDK 示例节点。它对工作流配置中的文档执行 Jaccard 词集合匹配，不访问网络或外部存储。

## 配置与端口

配置示例：

```json
{
  "documents": [
    {"id": "doc-1", "text": "Agent Studio 使用 Go 构建"},
    {"id": "doc-2", "text": "React 负责工作流画布"}
  ],
  "topK": 2
}
```

- 输入：`query`，必填的单个字符串。
- 输出：`matches`，按相关度排序的 JSON 数组。
- `documents`：1–1000 项；ID 去除首尾空白后必须唯一，文本必须至少产生一个 token。
- `topK`：1–100；实际返回数量不会超过文档数量。

输出示例：

```json
{
  "matches": [
    {"id": "doc-1", "text": "Agent Studio 使用 Go 构建", "score": 0.5}
  ]
}
```

## 算法与确定性

节点对 query 和文档文本做 Unicode 小写转换，以非 Unicode 字母或数字分词，去重为 token 集合，然后计算交集大小除以并集大小。结果按未舍入分数稳定降序排列，同分时保持配置中的文档顺序；输出分数四舍五入到小数点后 6 位。

相同配置和输入会产生字节级一致的 JSON 结果。即使所有分数为 0，也会按原始顺序返回前 `topK` 项。

## 本地验证

```bash
CGO_ENABLED=0 go run ./cmd/agent-studio node test ./extensions/retriever
CGO_ENABLED=0 go run ./cmd/agent-studio generate
make dev-api
```

## 使用边界

Retriever 只用于展示公开 Node SDK、通用 SchemaForm 和确定性本地处理能力。它不是向量检索，不使用 Embedding，也不是面向生产环境的知识库；需要语义检索、权限、索引更新或大规模数据集时，应接入受治理的专用检索服务。
