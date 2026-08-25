import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { APIError, api, type WorkflowSummaryPage, type WorkflowSummaryQuery } from '../../lib/api/client'
import { useWorkflowList } from './useWorkflowList'

const query = (q: string, cursor?: string): WorkflowSummaryQuery => ({ q, state: 'active', limit: 50, ...(cursor ? { cursor } : {}) })

describe('useWorkflowList', () => {
	afterEach(() => vi.restoreAllMocks())

	it('取消旧请求且旧结果不能覆盖新页面', async () => {
		const first = deferred<WorkflowSummaryPage>()
		const second = deferred<WorkflowSummaryPage>()
		const signals: AbortSignal[] = []
		vi.spyOn(api, 'listWorkflowSummaries').mockImplementation((_query, signal) => {
			signals.push(signal as AbortSignal)
			return signals.length === 1 ? first.promise : second.promise
		})
		const { result, rerender } = renderHook(({ value }) => useWorkflowList(value), { initialProps: { value: query('旧') } })
		rerender({ value: query('新') })
		expect(signals[0]?.aborted).toBe(true)
		await act(async () => second.resolve(page('new')))
		await waitFor(() => expect(result.current.page?.items[0]?.id).toBe('new'))
		await act(async () => first.resolve(page('old')))
		expect(result.current.page?.items[0]?.id).toBe('new')
	})

	it('游标变化会请求新页，手工刷新失败保留已有页面', async () => {
		const list = vi.spyOn(api, 'listWorkflowSummaries')
			.mockResolvedValueOnce(page('first'))
			.mockResolvedValueOnce(page('next'))
			.mockRejectedValueOnce(new APIError(500, 'INTERNAL_ERROR', '加载失败', 'req-7'))
		const { result, rerender } = renderHook(({ value }) => useWorkflowList(value), { initialProps: { value: query('') } })
		await waitFor(() => expect(result.current.page?.items[0]?.id).toBe('first'))
		rerender({ value: query('', 'cursor-2') })
		await waitFor(() => expect(result.current.page?.items[0]?.id).toBe('next'))
		expect(list).toHaveBeenLastCalledWith(expect.objectContaining({ cursor: 'cursor-2' }), expect.any(AbortSignal))
		act(() => result.current.reload())
		await waitFor(() => expect(result.current.error).toContain('req-7'))
		expect(result.current.page?.items[0]?.id).toBe('next')
	})
})

function page(id: string): WorkflowSummaryPage {
	return { items: [{ id, name: id, slug: id, description: '', draftRevision: 1, createdAt: '2026-08-26T00:00:00Z', updatedAt: '2026-08-26T00:00:00Z' }], nextCursor: null }
}

function deferred<T>() {
	let resolve!: (value: T) => void
	let reject!: (reason?: unknown) => void
	const promise = new Promise<T>((resolvePromise, rejectPromise) => { resolve = resolvePromise; reject = rejectPromise })
	return { promise, resolve, reject }
}
