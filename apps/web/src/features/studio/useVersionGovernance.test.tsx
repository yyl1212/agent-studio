import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api, type Workflow, type WorkflowDiff, type WorkflowVersionPage } from '../../lib/api/client'
import { useVersionGovernance } from './useVersionGovernance'

const workflow: Workflow = {
	id: 'w1', name: '演示', slug: 'demo', description: '', draftRevision: 8, publishedVersion: 2,
	agentPresentation: { title: '演示', description: '', accent: 'indigo', submitLabel: '运行', resultMode: 'auto' },
	draftGraph: { schemaVersion: 1, nodes: [], edges: [] },
	createdAt: '2026-08-27T00:00:00Z', updatedAt: '2026-08-27T00:00:00Z',
}

const page: WorkflowVersionPage = {
	items: [
		{ id: 'v2', version: 2, current: true, createdAt: '2026-08-27T02:00:00Z' },
		{ id: 'v1', version: 1, current: false, createdAt: '2026-08-27T01:00:00Z' },
	],
	nextCursor: 'next', rollbackCheckpoint: null,
}

const emptyDiff = (version: number): WorkflowDiff => ({
	base: { kind: 'version', version, versionId: `v${version}`, createdAt: '2026-08-27T00:00:00Z' },
	compare: { kind: 'draft', draftRevision: 8 },
	summary: { total: version, nodes: version, startParameters: 0, connections: 0, agentPresentation: 0, layout: 0 },
	truncated: false,
	groups: { nodes: [], startParameters: [], connections: [], agentPresentation: [], layout: [] },
})

const options = (overrides: Partial<Parameters<typeof useVersionGovernance>[0]> = {}) => ({
	workflow, saveState: 'saved' as const, editSerial: 0, archived: false,
	onApplyWorkflow: vi.fn(async () => undefined), onLockChange: vi.fn(), ...overrides,
})

describe('useVersionGovernance', () => {
	afterEach(() => vi.restoreAllMocks())

	it('默认比较当前发布版本和精确草稿 revision，并随 revision 更新', async () => {
		vi.spyOn(api, 'listWorkflowVersions').mockResolvedValue(page)
		const diff = vi.spyOn(api, 'diffWorkflowVersions').mockResolvedValue(emptyDiff(2))
		const initial = options()
		const { result, rerender } = renderHook(({ value }) => useVersionGovernance(value), { initialProps: { value: initial } })
		await waitFor(() => expect(result.current.loading).toBe(false))
		expect(diff).toHaveBeenCalledWith('w1', { base: { kind: 'version', version: 2 }, compare: { kind: 'draft', draftRevision: 8 } }, expect.any(AbortSignal))

		rerender({ value: { ...initial, workflow: { ...workflow, draftRevision: 9 } } })
		await waitFor(() => expect(diff).toHaveBeenLastCalledWith('w1', { base: { kind: 'version', version: 2 }, compare: { kind: 'draft', draftRevision: 9 } }, expect.any(AbortSignal)))
		act(() => result.current.setBase({ kind: 'version', version: 1 }))
		await waitFor(() => expect(diff).toHaveBeenLastCalledWith('w1', { base: { kind: 'version', version: 1 }, compare: { kind: 'draft', draftRevision: 9 } }, expect.any(AbortSignal)))
	})

	it('未发布工作流只加载列表，不请求差异', async () => {
		vi.spyOn(api, 'listWorkflowVersions').mockResolvedValue({ ...page, items: [], nextCursor: null })
		const diff = vi.spyOn(api, 'diffWorkflowVersions').mockResolvedValue(emptyDiff(1))
		const { result } = renderHook(() => useVersionGovernance(options({ workflow: { ...workflow, publishedVersion: null } })))
		await waitFor(() => expect(result.current.loading).toBe(false))
		expect(diff).not.toHaveBeenCalled()
		expect(result.current.base).toBeUndefined()
	})

	it('加载更多按 ID 去重并保持版本倒序', async () => {
		vi.spyOn(api, 'listWorkflowVersions')
			.mockResolvedValueOnce(page)
			.mockResolvedValueOnce({ items: [page.items[1], { id: 'v3', version: 3, current: false, createdAt: '2026-08-27T03:00:00Z' }], nextCursor: null, rollbackCheckpoint: null })
		vi.spyOn(api, 'diffWorkflowVersions').mockResolvedValue(emptyDiff(2))
		const { result } = renderHook(() => useVersionGovernance(options()))
		await waitFor(() => expect(result.current.versions).toHaveLength(2))
		await act(() => result.current.loadMore())
		expect(result.current.versions.map((item) => item.version)).toEqual([3, 2, 1])
		expect(api.listWorkflowVersions).toHaveBeenLastCalledWith('w1', { limit: 20, cursor: 'next' }, expect.any(AbortSignal))
	})

	it('切换选择会取消旧差异请求，旧响应不能覆盖新结果', async () => {
		vi.spyOn(api, 'listWorkflowVersions').mockResolvedValue(page)
		const first = deferred<WorkflowDiff>()
		const second = deferred<WorkflowDiff>()
		const signals: AbortSignal[] = []
		vi.spyOn(api, 'diffWorkflowVersions').mockImplementation((_id, request, signal) => {
			signals.push(signal as AbortSignal)
			return request.base.kind === 'version' && request.base.version === 2 ? first.promise : second.promise
		})
		const { result } = renderHook(() => useVersionGovernance(options()))
		await waitFor(() => expect(signals).toHaveLength(1))
		act(() => result.current.setBase({ kind: 'version', version: 1 }))
		expect(signals[0].aborted).toBe(true)
		await act(async () => second.resolve(emptyDiff(1)))
		await waitFor(() => expect(result.current.diff?.summary.total).toBe(1))
		await act(async () => first.resolve(emptyDiff(2)))
		expect(result.current.diff?.summary.total).toBe(1)
	})

	it('回滚和撤销应用服务端工作流、同步检查点及交互锁', async () => {
		vi.spyOn(api, 'listWorkflowVersions').mockResolvedValue(page)
		vi.spyOn(api, 'diffWorkflowVersions').mockResolvedValue(emptyDiff(2))
		const rolledBack = { ...workflow, draftRevision: 9 }
		const undone = { ...workflow, draftRevision: 10 }
		vi.spyOn(api, 'rollbackWorkflow').mockResolvedValue({ workflow: rolledBack, rollbackCheckpoint: { sourceRevision: 8, restoredRevision: 9, restoredFromVersion: 1, createdAt: '2026-08-27T03:00:00Z' } })
		vi.spyOn(api, 'undoWorkflowRollback').mockResolvedValue(undone)
		const onApplyWorkflow = vi.fn(async () => undefined)
		const onLockChange = vi.fn()
		const { result } = renderHook(() => useVersionGovernance(options({ onApplyWorkflow, onLockChange })))
		await waitFor(() => expect(result.current.loading).toBe(false))
		act(() => result.current.openRollback(1))
		expect(result.current.locked).toBe(true)
		await act(() => result.current.confirmRollback())
		expect(api.rollbackWorkflow).toHaveBeenCalledWith('w1', { targetVersion: 1, expectedDraftRevision: 8 }, expect.any(AbortSignal))
		expect(onApplyWorkflow).toHaveBeenCalledWith(rolledBack)
		expect(result.current.checkpoint?.restoredRevision).toBe(9)
		expect(result.current.locked).toBe(false)
		await act(() => result.current.undoRollback())
		expect(api.undoWorkflowRollback).toHaveBeenCalledWith('w1', { expectedDraftRevision: 9 }, expect.any(AbortSignal))
		expect(onApplyWorkflow).toHaveBeenLastCalledWith(undone)
		expect(result.current.checkpoint).toBeUndefined()
	})

	it('卸载时取消列表和差异请求并解除外部锁', async () => {
		const listSignal = deferred<WorkflowVersionPage>()
		const diffSignal = deferred<WorkflowDiff>()
		let capturedList!: AbortSignal
		let capturedDiff!: AbortSignal
		vi.spyOn(api, 'listWorkflowVersions').mockImplementation((_id, _query, signal) => { capturedList = signal as AbortSignal; return listSignal.promise })
		vi.spyOn(api, 'diffWorkflowVersions').mockImplementation((_id, _body, signal) => { capturedDiff = signal as AbortSignal; return diffSignal.promise })
		const onLockChange = vi.fn()
		const { unmount } = renderHook(() => useVersionGovernance(options({ onLockChange })))
		await waitFor(() => expect(capturedDiff).toBeDefined())
		unmount()
		expect(capturedList.aborted).toBe(true)
		expect(capturedDiff.aborted).toBe(true)
		expect(onLockChange).toHaveBeenLastCalledWith(false)
	})
})

function deferred<T>() {
	let resolve!: (value: T) => void
	let reject!: (reason?: unknown) => void
	const promise = new Promise<T>((resolvePromise, rejectPromise) => { resolve = resolvePromise; reject = rejectPromise })
	return { promise, resolve, reject }
}
