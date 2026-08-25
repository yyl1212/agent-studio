import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import type { DebugOverview } from '../../lib/api/client'
import type { RunEvent } from '../../lib/api/ndjson'
import { DebugWorkbench } from './DebugWorkbench'

const overview = {
	run: { id: 'r1', workflowId: 'w1', mode: 'test', status: 'completed', input: { topic: 'hello' }, inputRedactedPaths: [], startedAt: '2026-08-25T00:00:00Z', endedAt: '2026-08-25T00:00:02Z' },
	graph: { schemaVersion: 1, nodes: [], edges: [] }, nodeRuns: [], sourceChain: [], replayAvailable: true, rerunAvailable: true,
} satisfies DebugOverview

const emptyArrays = { activePorts: [], inputRedactedPaths: [], outputRedactedPaths: [] }
const events: RunEvent[] = [
	{ sequence: 1, type: 'run.started', runId: 'r1', timestamp: '2026-08-25T00:00:00Z', ...emptyArrays },
	{ sequence: 2, type: 'node.started', runId: 'r1', nodeId: 'node-1', input: { prompt: ['hello'] }, timestamp: '2026-08-25T00:00:00Z', ...emptyArrays },
	{ sequence: 3, type: 'node.completed', runId: 'r1', nodeId: 'node-1', output: '<img src=x onerror=alert(1)>', timestamp: '2026-08-25T00:00:01Z', ...emptyArrays },
]

describe('DebugWorkbench', () => {
	it('按 sequence 导航并把输出安全渲染为文本', async () => {
		const onSelectSequence = vi.fn()
		const onSelectNode = vi.fn()
		const { rerender } = render(<DebugWorkbench overview={overview} events={events} selectedSequence={2} selectedNodeID="node-1" onSelectSequence={onSelectSequence} onSelectNode={onSelectNode} />)
		expect(screen.queryByRole('complementary', { name: '调试工作台' })).not.toBeInTheDocument()
		expect(screen.getByText('已定位事件 2：node.started')).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '下一项' }))
		expect(onSelectSequence).toHaveBeenCalledWith(3)
		await userEvent.click(screen.getByRole('button', { name: /#3 node.completed node-1/ }))
		expect(onSelectNode).toHaveBeenCalledWith('node-1')
		rerender(<DebugWorkbench overview={overview} events={events} selectedSequence={3} selectedNodeID="node-1" onSelectSequence={onSelectSequence} onSelectNode={onSelectNode} />)
		expect(screen.getByText('"<img src=x onerror=alert(1)>"')).toBeInTheDocument()
		expect(document.querySelector('img')).toBeNull()
		expect(screen.getByText('1.000 秒')).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '上一项' }))
		expect(onSelectSequence).toHaveBeenCalledWith(2)
	})

	it('从选中节点进入局部重跑预览', async () => {
		const onStartRerun = vi.fn()
		render(<DebugWorkbench overview={overview} events={events} selectedNodeID="node-1" onSelectSequence={vi.fn()} onSelectNode={vi.fn()} onStartRerun={onStartRerun} />)
		await userEvent.click(screen.getByRole('button', { name: '从此节点重新运行' }))
		expect(onStartRerun).toHaveBeenCalledWith('node-1')
	})

	it('节点详情显示类型版本、状态和起止时间', () => {
		const detailed = {
			...overview,
			graph: { schemaVersion: 1, nodes: [{ id: 'node-1', type: 'template', typeVersion: '1', position: { x: 0, y: 0 }, config: {} }], edges: [] },
			nodeRuns: [{ id: 'nr1', runId: 'r1', nodeId: 'node-1', nodeType: 'template', status: 'completed', startedAt: '2026-08-25T00:00:00Z', endedAt: '2026-08-25T00:00:01Z' }],
		} satisfies DebugOverview
		render(<DebugWorkbench overview={detailed} events={events} selectedNodeID="node-1" onSelectSequence={vi.fn()} onSelectNode={vi.fn()} />)
		expect(screen.getByText('template@1')).toBeInTheDocument()
		expect(screen.getByText('状态：已完成')).toBeInTheDocument()
		expect(screen.getByText('开始：2026-08-25T00:00:00Z')).toBeInTheDocument()
		expect(screen.getByText('结束：2026-08-25T00:00:01Z')).toBeInTheDocument()
	})
})
