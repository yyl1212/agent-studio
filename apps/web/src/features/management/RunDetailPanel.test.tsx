import { act, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api, type RunSummary } from '../../lib/api/client'
import { RunDetailPanel } from './RunDetailPanel'

describe('RunDetailPanel', () => {
	afterEach(() => vi.restoreAllMocks())

	it('切换运行会取消旧详情，并安全渲染输出和终态调试链接', async () => {
		const first = deferred<Awaited<ReturnType<typeof api.getRun>>>()
		const second = deferred<Awaited<ReturnType<typeof api.getRun>>>()
		const signals: AbortSignal[] = []
		vi.spyOn(api, 'getRun').mockImplementation((_id, signal) => {
			signals.push(signal as AbortSignal)
			return signals.length === 1 ? first.promise : second.promise
		})
		const { rerender } = render(<MemoryRouter><RunDetailPanel summary={summary('one', 'completed')} onRequestClose={vi.fn()} /></MemoryRouter>)
		rerender(<MemoryRouter><RunDetailPanel summary={summary('two', 'failed')} onRequestClose={vi.fn()} /></MemoryRouter>)
		expect(signals[0]?.aborted).toBe(true)
		await act(async () => second.resolve({ run: { id: 'two', workflowId: summary('two').workflowId, mode: 'test', status: 'failed', input: {}, inputRedactedPaths: [], output: '<script>bad</script>', startedAt: '2026-08-26T00:00:00Z' }, nodeRuns: [] }))
		expect(await screen.findByText('<script>bad</script>')).toBeInTheDocument()
		expect(screen.getByRole('link', { name: '调试回放' })).toHaveAttribute('href', `/workflows/${summary('two').workflowId}/runs/two/debug`)
		expect(screen.getByRole('heading', { name: '运行详情' })).toHaveFocus()
	})

	it('运行中的详情不显示调试回放', async () => {
		vi.spyOn(api, 'getRun').mockResolvedValue({ run: { id: 'one', workflowId: summary('one').workflowId, mode: 'test', status: 'running', input: {}, inputRedactedPaths: [], startedAt: '2026-08-26T00:00:00Z' }, nodeRuns: [] })
		render(<MemoryRouter><RunDetailPanel summary={summary('one', 'running')} onRequestClose={vi.fn()} /></MemoryRouter>)
		await screen.findByRole('heading', { name: '运行详情' })
		expect(screen.queryByRole('link', { name: '调试回放' })).not.toBeInTheDocument()
	})
})

function summary(id: string, status: RunSummary['status'] = 'running'): RunSummary {
	return { id, workflowId: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', workflowName: '演示', workflowSlug: 'demo', mode: 'test', status, startedAt: '2026-08-26T00:00:00Z' }
}

function deferred<T>() {
	let resolve!: (value: T) => void
	const promise = new Promise<T>((resolvePromise) => { resolve = resolvePromise })
	return { promise, resolve }
}
