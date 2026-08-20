export const nodePackageCategories = [
	{ slug: 'model', label: '模型' },
	{ slug: 'search', label: '搜索' },
	{ slug: 'retrieval', label: '检索' },
	{ slug: 'vector', label: '向量' },
	{ slug: 'file', label: '文件' },
	{ slug: 'integration', label: '集成' },
	{ slug: 'data', label: '数据' },
	{ slug: 'utility', label: '工具' },
] as const

const labels = new Map<string, string>(nodePackageCategories.map(({ slug, label }) => [slug, label]))

export function nodePackageCategoryLabel(slug: string) {
	return labels.get(slug) ?? slug
}
