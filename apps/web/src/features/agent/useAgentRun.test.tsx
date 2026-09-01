import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { APIError, api, type AgentManifest, type AgentRunPublicView } from '../../lib/api/client'
import { useAgentRun } from './useAgentRun'

const presentation = { title: '助手', description: '', accent: 'indigo' as const, submitLabel: '运行', resultMode: 'auto' as const }
const manifest: AgentManifest = { workflowVersionId: 'version-1', version: 1, title: '助手', description: '', inputSchema: {}, presentation }

function view(status: AgentRunPublicView['run']['status'] = 'running', overrides: Partial<AgentRunPublicView> = {}): AgentRunPublicView {
  return {
    run: { runId: 'run-1', workflowVersionId: 'version-1', version: 1, status, startedAt: '2026-08-26T00:00:00Z', endedAt: null, output: null, error: null },
    presentation, events: [], nextSequence: 0, hasMore: false, ...overrides,
  }
}

describe('useAgentRun', () => {
  beforeEach(() => { vi.spyOn(crypto, 'randomUUID').mockReturnValue('123e4567-e89b-42d3-a456-426614174000') })
  afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks() })

  it('有 runId 时立即恢复并按 sequence 去重追加事件', async () => {
    vi.spyOn(api, 'getAgentRunView')
      .mockResolvedValueOnce(view('running', { events: [{ sequence: 1, type: 'node.started', timestamp: 't' }], nextSequence: 1, hasMore: true }))
      .mockResolvedValueOnce(view('completed', { events: [{ sequence: 1, type: 'node.started', timestamp: 't' }, { sequence: 2, type: 'node.completed', timestamp: 't' }], nextSequence: 2 }))
    const { result } = renderHook(() => useAgentRun({ slug: 'demo', runId: 'run-1', onAccepted: vi.fn() }))
    expect(result.current.phase).toBe('recovering')
    await waitFor(() => expect(result.current.phase).toBe('completed'))
    expect(api.getAgentRunView).toHaveBeenNthCalledWith(1, 'demo', 'run-1', 0, expect.any(AbortSignal))
    expect(api.getAgentRunView).toHaveBeenNthCalledWith(2, 'demo', 'run-1', 1, expect.any(AbortSignal))
    expect(result.current.events.map((event) => event.sequence)).toEqual([1, 2])
  })

  it('接受运行后稳定展示 queued，直到服务状态推进', async () => {
    let resolvePoll!: (value: AgentRunPublicView) => void
    vi.spyOn(api, 'startAgentRun').mockResolvedValue(view('queued').run)
    vi.spyOn(api, 'getAgentRunView').mockImplementation(() => new Promise((resolve) => { resolvePoll = resolve }))
    const { result } = renderHook(() => useAgentRun({ slug: 'demo', onAccepted: vi.fn() }))
    act(() => { void result.current.start(manifest, { topic: '排队任务' }) })
    await waitFor(() => expect(result.current.phase).toBe('queued'))
    expect(result.current.servicePhase).toBe('queued')
    act(() => resolvePoll(view('completed')))
  })

  it('刷新恢复 queued 运行并继续轮询', async () => {
    vi.useFakeTimers()
    const get = vi.spyOn(api, 'getAgentRunView').mockResolvedValueOnce(view('queued')).mockResolvedValueOnce(view('running'))
    const { result } = renderHook(() => useAgentRun({ slug: 'demo', runId: 'run-1', onAccepted: vi.fn() }))
    await act(async () => { await Promise.resolve() })
    expect(result.current.phase).toBe('queued')
    await act(async () => { await vi.advanceTimersByTimeAsync(1000) })
    expect(result.current.phase).toBe('running')
    expect(get).toHaveBeenCalledTimes(2)
  })

  it('recovery_required 使用低频轮询且网络瞬态不丢失服务状态', async () => {
    vi.useFakeTimers()
    const get = vi.spyOn(api, 'getAgentRunView')
      .mockResolvedValueOnce(view('recovery_required'))
      .mockRejectedValueOnce(new TypeError('offline'))
      .mockResolvedValueOnce(view('queued'))
    const { result } = renderHook(() => useAgentRun({ slug: 'demo', runId: 'run-1', onAccepted: vi.fn() }))
    await act(async () => { await Promise.resolve() })
    expect(result.current.phase).toBe('recovery_required')
    expect(result.current.servicePhase).toBe('recovery_required')
    await act(async () => { await vi.advanceTimersByTimeAsync(4999) })
    expect(get).toHaveBeenCalledTimes(1)
    await act(async () => { await vi.advanceTimersByTimeAsync(1) })
    expect(result.current.phase).toBe('reconnecting')
    expect(result.current.servicePhase).toBe('recovery_required')
    await act(async () => { await vi.advanceTimersByTimeAsync(1000) })
    expect(result.current.phase).toBe('queued')
  })

  it('终态仍有分页时追平全部事件后再停止', async () => {
    const get = vi.spyOn(api, 'getAgentRunView')
      .mockResolvedValueOnce(view('completed', { events: [{ sequence: 1, type: 'node.started', timestamp: 't' }], nextSequence: 1, hasMore: true }))
      .mockResolvedValueOnce(view('completed', { events: [{ sequence: 2, type: 'node.completed', timestamp: 't' }], nextSequence: 2 }))
    const { result } = renderHook(() => useAgentRun({ slug: 'demo', runId: 'run-1', onAccepted: vi.fn() }))
    await waitFor(() => expect(get).toHaveBeenCalledTimes(2))
    expect(result.current.events.map((event) => event.sequence)).toEqual([1, 2])
  })

  it('失败重试复用幂等键，接受后立即通知并轮询', async () => {
    const start = vi.spyOn(api, 'startAgentRun').mockRejectedValueOnce(new TypeError('offline')).mockResolvedValueOnce(view().run)
    vi.spyOn(api, 'getAgentRunView').mockResolvedValue(view('completed'))
    const onAccepted = vi.fn()
    const { result } = renderHook(() => useAgentRun({ slug: 'demo', onAccepted }))
    await act(async () => { await result.current.start(manifest, { b: 2, a: 1 }) })
    await act(async () => { await result.current.start(manifest, { a: 1, b: 2 }) })
    expect(start.mock.calls[0]?.[2]).toBe(start.mock.calls[1]?.[2])
    expect(onAccepted).toHaveBeenCalledWith('run-1')
    await waitFor(() => expect(result.current.phase).toBe('completed'))
  })

  it('输入变化或成功接受后生成新的幂等键', async () => {
    vi.mocked(crypto.randomUUID)
      .mockReturnValueOnce('11111111-1111-4111-8111-111111111111')
      .mockReturnValueOnce('22222222-2222-4222-8222-222222222222')
      .mockReturnValueOnce('33333333-3333-4333-8333-333333333333')
    const start = vi.spyOn(api, 'startAgentRun').mockResolvedValue(view().run)
    vi.spyOn(api, 'getAgentRunView').mockResolvedValue(view('completed'))
    const { result } = renderHook(() => useAgentRun({ slug: 'demo', onAccepted: vi.fn() }))
    await act(async () => { await result.current.start(manifest, { topic: 'a' }) })
    await act(async () => { await result.current.start(manifest, { topic: 'a' }) })
    await act(async () => { await result.current.start(manifest, { topic: 'b' }) })
    expect(start.mock.calls.map((call) => call[2])).toEqual([
      '11111111-1111-4111-8111-111111111111',
      '22222222-2222-4222-8222-222222222222',
      '33333333-3333-4333-8333-333333333333',
    ])
  })

  it('连续临时故障按 1、2、4、8、10、10 秒退避', async () => {
    vi.useFakeTimers()
    const get = vi.spyOn(api, 'getAgentRunView')
    for (let index = 0; index < 6; index += 1) get.mockRejectedValueOnce(new TypeError('offline'))
    get.mockResolvedValueOnce(view('completed'))
    const { result } = renderHook(() => useAgentRun({ slug: 'demo', runId: 'run-1', onAccepted: vi.fn() }))
    await act(async () => { await Promise.resolve() })
    for (const [index, delay] of [1000, 2000, 4000, 8000, 10000, 10000].entries()) {
      await act(async () => { await vi.advanceTimersByTimeAsync(delay) })
      expect(get).toHaveBeenCalledTimes(index + 2)
    }
    expect(result.current.phase).toBe('completed')
  })

  it('临时故障退避重连，404 停止并显示安全文案', async () => {
    vi.useFakeTimers()
    const get = vi.spyOn(api, 'getAgentRunView')
      .mockRejectedValueOnce(new APIError(500, 'INTERNAL_ERROR', '内部细节'))
      .mockResolvedValueOnce(view('running'))
      .mockRejectedValueOnce(new APIError(404, 'RUN_NOT_FOUND', '内部细节'))
    const { result } = renderHook(() => useAgentRun({ slug: 'demo', runId: 'run-1', onAccepted: vi.fn() }))
    await act(async () => { await Promise.resolve() })
    expect(result.current.phase).toBe('reconnecting')
    await act(async () => { await vi.advanceTimersByTimeAsync(1000) })
    expect(result.current.phase).toBe('running')
    await act(async () => { await vi.advanceTimersByTimeAsync(1000) })
    expect(result.current.phase).toBe('failed')
    expect(result.current.error).toBe('运行不存在或不属于该 Agent')
    expect(get).toHaveBeenCalledTimes(3)
  })

  it('取消只请求一次并继续轮询到 cancelled', async () => {
    vi.useFakeTimers()
    vi.spyOn(api, 'getAgentRunView').mockResolvedValueOnce(view('running')).mockResolvedValueOnce(view('cancelled'))
    const cancel = vi.spyOn(api, 'cancelAgentRun').mockResolvedValue(view('cancelling').run)
    const { result } = renderHook(() => useAgentRun({ slug: 'demo', runId: 'run-1', onAccepted: vi.fn() }))
    await act(async () => { await Promise.resolve() })
    expect(result.current.phase).toBe('running')
    await act(async () => { await Promise.all([result.current.cancel(), result.current.cancel()]) })
    expect(cancel).toHaveBeenCalledTimes(1)
    await act(async () => { await vi.advanceTimersByTimeAsync(1000) })
    expect(result.current.phase).toBe('cancelled')
  })

  it('queued 状态可以取消并推进到取消中', async () => {
    vi.useFakeTimers()
    vi.spyOn(api, 'getAgentRunView').mockResolvedValueOnce(view('queued')).mockResolvedValueOnce(view('cancelled'))
    const cancel = vi.spyOn(api, 'cancelAgentRun').mockResolvedValue(view('cancelling').run)
    const { result } = renderHook(() => useAgentRun({ slug: 'demo', runId: 'run-1', onAccepted: vi.fn() }))
    await act(async () => { await Promise.resolve() })
    expect(result.current.phase).toBe('queued')
    await act(async () => { await result.current.cancel() })
    expect(result.current.phase).toBe('cancelling')
    expect(cancel).toHaveBeenCalledTimes(1)
  })

  it('取消响应丢失时转为重连并继续观察真实终态', async () => {
    vi.useFakeTimers()
    vi.spyOn(api, 'getAgentRunView').mockResolvedValueOnce(view('running')).mockResolvedValueOnce(view('cancelled'))
    vi.spyOn(api, 'cancelAgentRun').mockRejectedValue(new TypeError('offline'))
    const { result } = renderHook(() => useAgentRun({ slug: 'demo', runId: 'run-1', onAccepted: vi.fn() }))
    await act(async () => { await Promise.resolve() })
    await act(async () => { await result.current.cancel() })
    expect(result.current.phase).toBe('reconnecting')
    await act(async () => { await vi.advanceTimersByTimeAsync(1000) })
    expect(result.current.phase).toBe('cancelled')
  })

  it('slug 改变时中止旧请求且旧响应不能覆盖新状态', async () => {
    let resolveOld!: (value: AgentRunPublicView) => void
    const get = vi.spyOn(api, 'getAgentRunView')
      .mockImplementationOnce(() => new Promise((resolve) => { resolveOld = resolve }))
      .mockResolvedValueOnce(view('completed'))
    const { result, rerender } = renderHook(({ slug }) => useAgentRun({ slug, runId: 'run-1', onAccepted: vi.fn() }), { initialProps: { slug: 'old' } })
    rerender({ slug: 'new' })
    await waitFor(() => expect(result.current.phase).toBe('completed'))
    act(() => resolveOld(view('failed')))
    await act(async () => { await Promise.resolve() })
    expect(result.current.phase).toBe('completed')
    expect((get.mock.calls[0]?.[3] as AbortSignal).aborted).toBe(true)
  })
})
