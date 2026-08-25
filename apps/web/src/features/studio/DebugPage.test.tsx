import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { api, type DebugOverview, type NodeDefinition } from '../../lib/api/client'
import type { RunEvent } from '../../lib/api/ndjson'
import { DebugPage } from './DebugPage'

vi.mock('../../lib/api/client', async (importOriginal) => {
	const original = await importOriginal<typeof import('../../lib/api/client')>()
	return { ...original, api: { ...original.api } }
})

const definition = (type: string, title: string): NodeDefinition => ({
	type, version: '1', title, description: '', category: '流程', configSchema: {}, inputs: [], outputs: [], capabilities: [], executionSafety: 'pure',
	package: { name: 'agent-studio.dev/core', displayName: 'Core', license: 'Apache-2.0', repository: 'https://example.com/core', source: 'builtin' },
})

const overview = (replayAvailable = true): DebugOverview => ({
	run: { id: 'r1', workflowId: 'w1', mode: 'test', status: 'completed', input: {}, startedAt: '2026-08-25T00:00:00Z', endedAt: '2026-08-25T00:00:02Z' },
	graph: {
		schemaVersion: 1,
		nodes: [
			{ id: 'start', type: 'start', typeVersion: '1', position: { x: 0, y: 0 }, config: {} },
			{ id: 'end', type: 'end', typeVersion: '1', position: { x: 300, y: 0 }, config: {} },
		],
		edges: [],
	},
	nodeRuns: [], sourceChain: [], replayAvailable, rerunAvailable: replayAvailable,
	...(replayAvailable ? {} : { unavailableReason: '当前运行缺少完整事件' }),
})

const arrays = { activePorts: [], inputRedactedPaths: [], outputRedactedPaths: [] }
const pageEvents: RunEvent[] = [
	{ sequence: 1, type: 'run.started', runId: 'r1', timestamp: '2026-08-25T00:00:00Z', ...arrays },
	{ sequence: 2, type: 'node.started', runId: 'r1', nodeId: 'start', input: {}, timestamp: '2026-08-25T00:00:00Z', ...arrays },
	{ sequence: 3, type: 'node.completed', runId: 'r1', nodeId: 'start', output: {}, timestamp: '2026-08-25T00:00:01Z', ...arrays },
]

describe('DebugPage', () => {
	beforeEach(() => {
		vi.restoreAllMocks()
		vi.spyOn(api, 'getDebugOverview').mockResolvedValue(overview())
		vi.spyOn(api, 'listNodeTypes').mockResolvedValue([definition('start', '开始'), definition('end', '结束')])
		vi.spyOn(api, 'resolveNodeType').mockResolvedValue({ inputs: [], outputs: [] })
		vi.spyOn(api, 'saveWorkflow').mockResolvedValue({} as never)
		vi.spyOn(api, 'listRunEvents').mockResolvedValueOnce({ events: pageEvents, nextAfterSequence: 3 }).mockResolvedValueOnce({ events: [], nextAfterSequence: 3 })
	})

	it('加载全部事件页、保留只读画布并联动时间线节点', async () => {
		renderDebugPage()
		expect(await screen.findByText('只读回放')).toBeInTheDocument()
		await waitFor(() => expect(api.listRunEvents).toHaveBeenNthCalledWith(2, 'r1', 3, expect.any(AbortSignal)))
		expect(api.resolveNodeType).toHaveBeenCalledTimes(2)
		expect(api.saveWorkflow).not.toHaveBeenCalled()
		await userEvent.click(screen.getByRole('button', { name: /#2 node.started start/ }))
		expect(await screen.findByText('已定位事件 2：node.started')).toBeInTheDocument()
		expect(screen.getByTestId('node-start')).toHaveClass('debug-current')
	})

	it('legacy 只显示摘要且不请求精确事件', async () => {
		vi.mocked(api.getDebugOverview).mockResolvedValue(overview(false))
		renderDebugPage()
		expect(await screen.findByText('当前运行缺少完整事件')).toBeInTheDocument()
		expect(api.listRunEvents).not.toHaveBeenCalled()
		expect(screen.queryByRole('button', { name: '从此节点重新运行' })).not.toBeInTheDocument()
	})
})

function renderDebugPage() {
	return render(
		<MemoryRouter initialEntries={['/workflows/w1/runs/r1/debug']}>
			<Routes><Route path="/workflows/:id/runs/:runId/debug" element={<DebugPage />} /></Routes>
		</MemoryRouter>,
	)
}
