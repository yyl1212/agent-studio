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
})
