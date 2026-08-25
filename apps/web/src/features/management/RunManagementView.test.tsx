import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api, type RunSummary } from '../../lib/api/client'
import { ManagementPage } from './ManagementPage'

describe('RunManagementView', () => {
	afterEach(() => vi.restoreAllMocks())

	it('规范非法 URL 并把筛选写回 URL', async () => {
		vi.spyOn(api, 'listRunSummaries').mockResolvedValue({ items: [], nextCursor: null })
		renderPage('/runs?status=unknown&unknown=true')
		expect(await screen.findByText('还没有运行记录')).toBeInTheDocument()
		expect(screen.getByText('已移除无效筛选条件')).toBeInTheDocument()
		await userEvent.type(screen.getByLabelText('工作流 ID'), 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa')
		await userEvent.click(screen.getByLabelText('失败'))
		await userEvent.click(screen.getByLabelText('草稿测试'))
		await userEvent.type(screen.getByLabelText('运行 ID'), '11111111-1111-4111-8111-111111111111')
		fireEvent.change(screen.getByLabelText('开始时间下限'), { target: { value: '2026-08-01T00:00:00.000Z' } })
		fireEvent.change(screen.getByLabelText('开始时间上限'), { target: { value: '2026-08-25T00:00:00.000Z' } })
		await waitFor(() => expect(screen.getByTestId('location-search')).toHaveTextContent('workflowId=aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa'))
		expect(screen.getByTestId('location-search')).toHaveTextContent('status=failed')
		expect(screen.getByTestId('location-search')).toHaveTextContent('mode=test')
		expect(screen.getByTestId('location-search')).toHaveTextContent('runId=11111111-1111-4111-8111-111111111111')
		expect(screen.getByTestId('location-search')).toHaveTextContent('startedAfter=2026-08-01T00%3A00%3A00.000Z')
		expect(screen.getByTestId('location-search')).toHaveTextContent('startedBefore=2026-08-25T00%3A00%3A00.000Z')
	})

	it('显示摘要、只在选择后取详情，Escape 关闭并恢复焦点', async () => {
		vi.spyOn(api, 'listRunSummaries').mockResolvedValue({ items: [summary('done', 'completed'), summary('live', 'running')], nextCursor: null })
		vi.spyOn(api, 'getRun').mockResolvedValue({ run: { id: 'done', workflowId: summary('done').workflowId, mode: 'published', status: 'completed', input: {}, inputRedactedPaths: [], startedAt: '2026-08-26T00:00:00Z', endedAt: '2026-08-26T00:00:02Z' }, nodeRuns: [] })
		renderPage('/runs')
		expect(await screen.findByText('2.0 秒')).toBeInTheDocument()
		expect(screen.getAllByText('演示')).toHaveLength(2)
		expect(api.getRun).not.toHaveBeenCalled()
		const trigger = screen.getByRole('button', { name: '查看运行 done' })
		await userEvent.click(trigger)
		await screen.findByRole('heading', { name: '运行详情' })
		expect(api.getRun).toHaveBeenCalledOnce()
		fireEvent.keyDown(window, { key: 'Escape' })
		await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
		expect(trigger).toHaveFocus()
		expect(screen.queryByRole('button', { name: '取消运行' })).not.toBeInTheDocument()
		expect(screen.queryByRole('button', { name: '重新运行' })).not.toBeInTheDocument()
	})
})

function renderPage(entry: string) {
	return render(<MemoryRouter initialEntries={[entry]}><Routes><Route path="/runs" element={<><ManagementPage section="runs" /><Location /></>} /></Routes></MemoryRouter>)
}

function Location() { return <output data-testid="location-search">{useLocation().search}</output> }

function summary(id: string, status: RunSummary['status'] = 'running'): RunSummary {
	return { id, workflowId: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', workflowName: '演示', workflowSlug: 'demo', workflowVersion: 2, mode: 'published', status, startedAt: '2026-08-26T00:00:00Z', ...(status === 'running' ? {} : { endedAt: '2026-08-26T00:00:02Z' }) }
}
