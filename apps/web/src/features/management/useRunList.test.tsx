import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { APIError, api, type RunSummaryPage, type RunSummaryQuery } from '../../lib/api/client'
import { useRunList } from './useRunList'

const query = (runId?: string, cursor?: string): RunSummaryQuery => ({ statuses: [], modes: [], limit: 50, ...(runId ? { runId } : {}), ...(cursor ? { cursor } : {}) })

describe('useRunList', () => {
	afterEach(() => vi.restoreAllMocks())

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

	it('分页和手工刷新不使用定时器，失败保留页面', async () => {
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
})

function page(id: string): RunSummaryPage {
	return { items: [{ id, workflowId: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', workflowName: '演示', workflowSlug: 'demo', mode: 'test', status: 'running', startedAt: '2026-08-26T00:00:00Z' }], nextCursor: null }
}

function deferred<T>() {
	let resolve!: (value: T) => void
	const promise = new Promise<T>((resolvePromise) => { resolve = resolvePromise })
	return { promise, resolve }
}
