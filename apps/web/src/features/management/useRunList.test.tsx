import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { APIError, api, type RunSummaryPage, type RunSummaryQuery } from '../../lib/api/client'
import { useRunList } from './useRunList'

const query = (runId?: string, cursor?: string): RunSummaryQuery => ({ statuses: [], modes: [], limit: 50, ...(runId ? { runId } : {}), ...(cursor ? { cursor } : {}) })

describe('useRunList', () => {
	afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks() })

	it('取消旧筛选请求并隔离迟到响应', async () => {
		const first = deferred<RunSummaryPage>()
		const second = deferred<RunSummaryPage>()
		const signals: AbortSignal[] = []
		vi.spyOn(api, 'listRunSummaries').mockImplementation((_query, signal) => {
			signals.push(signal as AbortSignal)
			return signals.length === 1 ? first.promise : second.promise
		})
		const { result, rerender } = renderHook(({ value }) => useRunList(value), { initialProps: { value: query('old') } })
		rerender({ value: query('new') })
		expect(signals[0]?.aborted).toBe(true)
		await act(async () => second.resolve(page('new')))
		await waitFor(() => expect(result.current.page?.items[0]?.id).toBe('new'))
		await act(async () => first.resolve(page('old')))
		expect(result.current.page?.items[0]?.id).toBe('new')
	})

	it('手工刷新立即取消旧请求，卸载再取消新请求', async () => {
		const requests = [deferred<RunSummaryPage>(), deferred<RunSummaryPage>()]
		const signals: AbortSignal[] = []
		vi.spyOn(api, 'listRunSummaries').mockImplementation((_query, signal) => {
			signals.push(signal as AbortSignal)
			return requests[signals.length - 1]!.promise
		})
		const { result, unmount } = renderHook(() => useRunList(query()))
		act(() => result.current.reload())
		expect(signals[0]?.aborted).toBe(true)
		await waitFor(() => expect(signals).toHaveLength(2))
		unmount()
		expect(signals[1]?.aborted).toBe(true)
	})

	it('分页和手工刷新不轮询，失败保留页面', async () => {
		const list = vi.spyOn(api, 'listRunSummaries').mockResolvedValueOnce(page('first')).mockResolvedValueOnce(page('next')).mockRejectedValueOnce(new APIError(500, 'INTERNAL_ERROR', '失败', 'req-run'))
		const { result, rerender } = renderHook(({ value }) => useRunList(value), { initialProps: { value: query() } })
		await waitFor(() => expect(result.current.page?.items[0]?.id).toBe('first'))
		rerender({ value: query(undefined, 'next') })
		await waitFor(() => expect(result.current.page?.items[0]?.id).toBe('next'))
		const interval = vi.spyOn(window, 'setInterval')
		await act(async () => { result.current.reload(); await Promise.resolve(); await Promise.resolve() })
		expect(result.current.error).toContain('req-run')
		expect(result.current.page?.items[0]?.id).toBe('next')
		expect(list).toHaveBeenCalledTimes(3)
		expect(interval).not.toHaveBeenCalled()
	})

	it('仅在第一页含活跃运行时每三秒刷新', async () => {
		vi.useFakeTimers()
		const list = vi.spyOn(api, 'listRunSummaries')
			.mockResolvedValueOnce(page('live', 'running'))
			.mockResolvedValueOnce(page('cancelling', 'cancelling'))
			.mockResolvedValueOnce(page('done', 'completed'))
		const { result } = renderHook(() => useRunList(query()))
		await flushPromises()
		expect(result.current.page?.items[0]?.id).toBe('live')
		await act(async () => { vi.advanceTimersByTime(3000); await Promise.resolve() })
		expect(list).toHaveBeenCalledTimes(2)
		expect(result.current.page?.items[0]?.status).toBe('cancelling')
		await act(async () => { vi.advanceTimersByTime(3000); await Promise.resolve() })
		expect(list).toHaveBeenCalledTimes(3)
		expect(result.current.page?.items[0]?.status).toBe('completed')
		await act(async () => { vi.advanceTimersByTime(6000); await Promise.resolve() })
		expect(list).toHaveBeenCalledTimes(3)
	})

	it('轮询单飞，失败保留旧页并显示非阻塞错误', async () => {
		vi.useFakeTimers()
		const pending = deferred<RunSummaryPage>()
		const list = vi.spyOn(api, 'listRunSummaries')
			.mockResolvedValueOnce(page('live', 'running'))
			.mockImplementationOnce(() => pending.promise)
			.mockRejectedValueOnce(new APIError(500, 'INTERNAL_ERROR', '轮询失败', 'req-poll'))
		const { result } = renderHook(() => useRunList(query()))
		await flushPromises()
		await act(async () => { vi.advanceTimersByTime(6000); await Promise.resolve() })
		expect(list).toHaveBeenCalledTimes(2)
		await act(async () => pending.resolve(page('live', 'running')))
		await act(async () => { vi.advanceTimersByTime(3000); await Promise.resolve(); await Promise.resolve() })
		expect(list).toHaveBeenCalledTimes(3)
		expect(result.current.page?.items[0]?.id).toBe('live')
		expect(result.current.error).toContain('req-poll')
	})

	it('页面隐藏时暂停，重新可见后立即单飞刷新', async () => {
		vi.useFakeTimers()
		let visibility: DocumentVisibilityState = 'visible'
		vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => visibility)
		const list = vi.spyOn(api, 'listRunSummaries').mockResolvedValue(page('live', 'running'))
		renderHook(() => useRunList(query()))
		await flushPromises()
		visibility = 'hidden'
		document.dispatchEvent(new Event('visibilitychange'))
		await act(async () => { vi.advanceTimersByTime(6000); await Promise.resolve() })
		expect(list).toHaveBeenCalledTimes(1)
		visibility = 'visible'
		document.dispatchEvent(new Event('visibilitychange'))
		await flushPromises()
		expect(list).toHaveBeenCalledTimes(2)
	})

	it('终态第一页不创建定时刷新', async () => {
		vi.useFakeTimers()
		const interval = vi.spyOn(window, 'setInterval')
		const list = vi.spyOn(api, 'listRunSummaries').mockResolvedValue(page('done', 'completed'))
		renderHook(() => useRunList(query()))
		await flushPromises()
		await act(async () => { vi.advanceTimersByTime(6000); await Promise.resolve() })
		expect(list).toHaveBeenCalledOnce()
		expect(interval).not.toHaveBeenCalled()
	})
})

function page(id: string, status: 'running' | 'cancelling' | 'completed' = 'running'): RunSummaryPage {
	return { items: [{ id, workflowId: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', workflowName: '演示', workflowSlug: 'demo', mode: 'test', status, startedAt: '2026-08-26T00:00:00Z' }], nextCursor: null }
}

function deferred<T>() {
	let resolve!: (value: T) => void
	const promise = new Promise<T>((resolvePromise) => { resolve = resolvePromise })
	return { promise, resolve }
}

async function flushPromises() {
	await act(async () => { await Promise.resolve(); await Promise.resolve() })
}
