import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { APIError, api, type WorkflowSummary } from '../../lib/api/client'
import { ManagementPage } from './ManagementPage'

describe('WorkflowManagementView', () => {
	afterEach(() => vi.restoreAllMocks())

	it('规范非法 URL、显示提示和空状态', async () => {
		vi.spyOn(api, 'listWorkflowSummaries').mockResolvedValue({ items: [], nextCursor: null })
		renderPage('/workflows?state=deleted&unknown=true')
		expect(await screen.findByText('还没有工作流')).toBeInTheDocument()
		expect(screen.getByText('已移除无效筛选条件')).toBeInTheDocument()
		await waitFor(() => expect(screen.getByTestId('location-search')).toHaveTextContent('?state=active&limit=50'))
	})

	it('搜索、状态和下一页写入 URL 并请求对应页面', async () => {
		const list = vi.spyOn(api, 'listWorkflowSummaries')
			.mockResolvedValueOnce({ items: [summary('one')], nextCursor: 'next' })
			.mockResolvedValueOnce({ items: [summary('two')], nextCursor: null })
			.mockResolvedValue({ items: [summary('three')], nextCursor: null })
		renderPage('/workflows')
		await screen.findByRole('link', { name: 'one' })
		await userEvent.click(screen.getByRole('button', { name: '下一页' }))
		await screen.findByRole('link', { name: 'two' })
		expect(list).toHaveBeenCalledWith(expect.objectContaining({ cursor: 'next' }), expect.any(AbortSignal))
		await userEvent.type(screen.getByRole('searchbox', { name: '搜索工作流' }), 'Agent')
		await waitFor(() => {
			expect(list.mock.calls.at(-1)?.[0]).toMatchObject({ q: 'Agent' })
			expect(list.mock.calls.at(-1)?.[0].cursor).toBeUndefined()
		})
		await userEvent.selectOptions(screen.getByLabelText('工作流状态'), 'all')
		await waitFor(() => {
			expect(list.mock.calls.at(-1)?.[0]).toMatchObject({ state: 'all' })
			expect(list.mock.calls.at(-1)?.[0].cursor).toBeUndefined()
		})
	})

	it('复制冲突保留表单，归档成功后刷新并提示', async () => {
		vi.spyOn(api, 'listWorkflowSummaries')
			.mockResolvedValueOnce({ items: [summary('one')], nextCursor: null })
			.mockResolvedValue({ items: [], nextCursor: null })
		vi.spyOn(api, 'copyWorkflow').mockRejectedValue(new APIError(409, 'WORKFLOW_SLUG_CONFLICT', 'Agent 地址标识已存在', 'req-copy'))
		vi.spyOn(api, 'archiveWorkflow').mockResolvedValue({ ...workflow('one'), archivedAt: '2026-08-26T01:00:00Z' })
		renderPage('/workflows')
		await screen.findByRole('link', { name: 'one' })
		await userEvent.click(screen.getByRole('button', { name: 'one 的操作' }))
		await userEvent.click(screen.getByRole('button', { name: '复制' }))
		await userEvent.clear(screen.getByLabelText('Agent 地址标识'))
		await userEvent.type(screen.getByLabelText('Agent 地址标识'), 'one-copy')
		await userEvent.click(screen.getByRole('button', { name: '创建副本' }))
		expect(await screen.findByRole('alert')).toHaveTextContent('Agent 地址标识已存在 · Request ID: req-copy')
		expect(screen.getByLabelText('Agent 地址标识')).toHaveValue('one-copy')
		await userEvent.click(screen.getByRole('button', { name: '取消' }))
		await userEvent.click(screen.getByRole('button', { name: 'one 的操作' }))
		await userEvent.click(screen.getByRole('button', { name: '归档' }))
		await userEvent.click(screen.getByRole('button', { name: '确认归档' }))
		expect(await screen.findByText('已归档', { selector: 'p' })).toBeInTheDocument()
		expect(screen.queryByRole('link', { name: 'one' })).not.toBeInTheDocument()
	})
})

function renderPage(initialEntry: string) {
	return render(<MemoryRouter initialEntries={[initialEntry]}><Routes>
		<Route path="/workflows" element={<><ManagementPage section="workflows" /><LocationSearch /></>} />
		<Route path="/workflows/:id" element={<p>编辑器</p>} />
	</Routes></MemoryRouter>)
}

function LocationSearch() {
	return <output data-testid="location-search">{useLocation().search}</output>
}

function summary(id: string): WorkflowSummary {
	return { id, name: id, slug: id, description: '', draftRevision: 1, createdAt: '2026-08-26T00:00:00Z', updatedAt: '2026-08-26T00:00:00Z' }
}

function workflow(id: string) {
	return {
		...summary(id),
		agentPresentation: { title: id, description: '', accent: 'indigo' as const, submitLabel: '运行 Agent', resultMode: 'auto' as const },
		draftGraph: { schemaVersion: 1 as const, nodes: [], edges: [] },
	}
}
