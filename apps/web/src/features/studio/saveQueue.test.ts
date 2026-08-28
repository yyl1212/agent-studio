import { describe, expect, it, vi } from 'vitest'

import type { Graph } from '../../lib/api/client'
import { SaveQueue } from './saveQueue'

function graph(id: string): Graph {
  return { schemaVersion: 1, nodes: [{ id, type: 'end', typeVersion: '1', position: { x: 0, y: 0 }, config: {} }], edges: [] }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => { resolve = done })
  return { promise, resolve }
}

describe('SaveQueue', () => {
	it('空闲时接纳外部推进的 revision 并发布 saved 状态', () => {
		const states: string[] = []
		const queue = new SaveQueue(4, vi.fn(), 800, (state) => states.push(state))
		queue.adoptRevision(5)
		expect(queue.getRevision()).toBe(5)
		expect(states).toEqual(['saved'])
	})

	it('拒绝未推进或非空闲状态下接纳 revision', async () => {
		const idle = new SaveQueue(4, vi.fn())
		expect(() => idle.adoptRevision(4)).toThrow('adopted revision must advance')

		vi.useFakeTimers()
		const timerQueue = new SaveQueue(1, vi.fn(), 800)
		timerQueue.enqueue(graph('timer'))
		expect(() => timerQueue.adoptRevision(2)).toThrow('save queue must be idle before adopting a revision')
		vi.useRealTimers()

		const active = deferred<{ draftRevision: number }>()
		const inFlightQueue = new SaveQueue(1, vi.fn().mockReturnValue(active.promise), 0)
		inFlightQueue.enqueue(graph('active'))
		const flushing = inFlightQueue.flush()
		expect(() => inFlightQueue.adoptRevision(2)).toThrow('save queue must be idle before adopting a revision')
		inFlightQueue.enqueue(graph('pending'))
		expect(() => inFlightQueue.adoptRevision(2)).toThrow('save queue must be idle before adopting a revision')
		active.resolve({ draftRevision: 2 })
		await flushing
	})

  it('同一时刻只保存一次并合并为最新草稿', async () => {
    vi.useFakeTimers()
    const first = deferred<{ draftRevision: number }>()
    const save = vi.fn().mockReturnValueOnce(first.promise).mockResolvedValueOnce({ draftRevision: 3 })
    const queue = new SaveQueue(1, save, 800)
    queue.enqueue(graph('a'))
    await vi.advanceTimersByTimeAsync(800)
    queue.enqueue(graph('b'))
    queue.enqueue(graph('c'))
    expect(save).toHaveBeenCalledTimes(1)
    first.resolve({ draftRevision: 2 })
    await queue.flush()
    expect(save).toHaveBeenCalledTimes(2)
    expect(save.mock.calls[1][0]).toEqual({ draftRevision: 2, graph: graph('c') })
    vi.useRealTimers()
  })

  it('409 后停止队列并暴露冲突状态', async () => {
    const states: string[] = []
    const save = vi.fn().mockRejectedValue({ status: 409 })
    const queue = new SaveQueue(1, save, 0, (state) => states.push(state))
    queue.enqueue(graph('a'))
    await expect(queue.flush()).rejects.toEqual({ status: 409 })
    queue.enqueue(graph('b'))
    expect(save).toHaveBeenCalledTimes(1)
    expect(states.at(-1)).toBe('conflict')
  })

  it('停止时取消尚未开始的保存并忽略后续入队', async () => {
    vi.useFakeTimers()
    const save = vi.fn()
    const queue = new SaveQueue(1, save, 800)
    queue.enqueue(graph('pending'))
    queue.stop()
    queue.enqueue(graph('ignored'))
    await vi.advanceTimersByTimeAsync(800)
    expect(save).not.toHaveBeenCalled()
    vi.useRealTimers()
  })
})
