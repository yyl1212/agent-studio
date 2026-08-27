import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import type { WorkflowDiff } from '../../lib/api/client'
import { VersionDiffView } from './VersionDiffView'

const diff: WorkflowDiff = {
	base: { kind: 'version', version: 1, versionId: '11111111-1111-4111-8111-111111111111', createdAt: '2026-08-27T00:00:00Z' },
	compare: { kind: 'draft', draftRevision: 8 },
	summary: { total: 8, nodes: 2, startParameters: 1, connections: 1, agentPresentation: 1, layout: 1 },
	truncated: true,
	groups: {
		nodes: [
			{
				nodeId: 'writer', title: '写作节点', kind: 'modified', config: [
					{ path: '/config/template', kind: 'modified', before: '旧模板', after: '新模板' },
					{ path: '/config/nullable', kind: 'modified', before: null },
					{ path: '/config/secret', kind: 'modified', before: 'before-secret', after: 'after-secret', valueOmitted: 'secret' },
					{ path: '/config/schema', kind: 'modified', valueOmitted: 'definition_unavailable' },
					{ path: '/config/large', kind: 'modified', valueOmitted: 'too_large' },
				],
			},
			{ nodeId: 'output', title: '输出节点', kind: 'added', config: [] },
		],
		startParameters: [{ key: 'topic', kind: 'reordered', beforeOrder: 2, afterOrder: 1, changes: [] }],
		connections: [{ kind: 'added', connection: { source: 'writer', sourcePort: 'text', target: 'output', targetPort: 'value' } }],
		agentPresentation: [{ field: 'submitLabel', change: { path: '/submitLabel', kind: 'modified', before: '运行', after: '开始写作' } }],
		layout: [{ nodeId: 'writer', title: '写作节点', before: { x: 10, y: 20 }, after: { x: 80, y: 120 } }],
	},
}

describe('VersionDiffView', () => {
	it('展示五组语义差异、截断提示，并优先隐藏敏感值', async () => {
		render(<VersionDiffView diff={diff} />)

		const nodes = screen.getByRole('button', { name: /节点.*2 项变化/ })
		expect(nodes).toHaveAttribute('aria-expanded', 'true')
		expect(screen.getByText('旧模板')).toBeInTheDocument()
		expect(screen.getByText('新模板')).toBeInTheDocument()
		expect(screen.getByText('null')).toBeInTheDocument()
		expect(screen.getByText('未设置')).toBeInTheDocument()
		expect(screen.getByText('值已变化，内容不可查看')).toBeInTheDocument()
		expect(screen.getByText('节点定义不可用，仅展示变化摘要')).toBeInTheDocument()
		expect(screen.getByText('值过大，已省略前后内容')).toBeInTheDocument()
		expect(screen.queryByText('before-secret')).not.toBeInTheDocument()
		expect(screen.queryByText('after-secret')).not.toBeInTheDocument()
		expect(screen.getByRole('status')).toHaveTextContent('仅展示前 500 项详细差异')

		const startParameters = screen.getByRole('button', { name: /开始参数.*1 项变化/ })
		expect(startParameters).toHaveAttribute('aria-expanded', 'false')
		await userEvent.click(startParameters)
		expect(screen.getByText('topic').closest('li')).toHaveTextContent('topic：顺序 2 → 1')

		await userEvent.click(screen.getByRole('button', { name: /连线.*1 项变化/ }))
		expect(screen.getByText('writer.text → output.value')).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: /Agent 页面.*1 项变化/ }))
		expect(screen.getByText('提交按钮文案')).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: /画布布局.*1 项变化/ }))
		expect(screen.getByText('写作节点：(10, 20) → (80, 120)')).toBeInTheDocument()
	})

	it('没有差异时展示明确空状态且五组均可访问', () => {
		const empty: WorkflowDiff = {
			...diff,
			summary: { total: 0, nodes: 0, startParameters: 0, connections: 0, agentPresentation: 0, layout: 0 },
			truncated: false,
			groups: { nodes: [], startParameters: [], connections: [], agentPresentation: [], layout: [] },
		}
		render(<VersionDiffView diff={empty} />)
		expect(screen.getByText('两个快照没有差异')).toBeInTheDocument()
		expect(screen.getAllByRole('button')).toHaveLength(5)
	})
})
