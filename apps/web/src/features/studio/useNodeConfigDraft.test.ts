import { act, renderHook, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import type { NodeDefinition, ResolvedPorts } from '../../lib/api/client'
import type { StudioNode } from './types'
import { useNodeConfigDraft, type ResolveNodePorts } from './useNodeConfigDraft'

const definition: NodeDefinition = {
  type: 'dynamic', version: '1', title: '动态节点', description: '', category: 'logic',
  configSchema: { type: 'object', required: ['model'], properties: { model: { type: 'string', minLength: 1 } }, additionalProperties: false },
  inputs: [], outputs: [], capabilities: [], executionSafety: 'pure',
  package: { name: 'builtin', displayName: '内置', license: 'Apache-2.0', repository: 'https://example.com', source: 'builtin' },
}
const makeNode = (id: string): StudioNode => ({ id, type: 'studio', position: { x: 0, y: 0 }, data: { nodeType: 'dynamic', typeVersion: '1', config: { model: 'old' }, definition, ports: { inputs: [], outputs: [] }, issues: [] } })
const deferred = <T,>() => { let resolve!: (value: T) => void; const promise = new Promise<T>((done) => { resolve = done }); return { promise, resolve } }
const nextPorts: ResolvedPorts = { inputs: [], outputs: [] }

describe('useNodeConfigDraft', () => {
  it('按 idle dirty resolving ready error 输出稳定状态', async () => {
    const resolveDeferred = deferred<ResolvedPorts>()
    const resolve: ResolveNodePorts = vi.fn().mockReturnValue(resolveDeferred.promise)
    const { result } = renderHook(() => useNodeConfigDraft({ node: makeNode('a'), edges: [], resolve, debounceMs: 20 }))
    expect(result.current.status).toBe('idle')
    act(() => result.current.setDraft({ model: '' }))
    expect(result.current.status).toBe('error')
    expect(result.current.errorKind).toBe('validation')
    act(() => result.current.setDraft({ model: 'new' }))
    expect(result.current.status).toBe('dirty')
    await waitFor(() => expect(result.current.status).toBe('resolving'))
    resolveDeferred.resolve(nextPorts)
    await waitFor(() => expect(result.current.status).toBe('ready'))
  })

  it('端口解析失败后用同一草稿重试并进入 ready', async () => {
    const resolve: ResolveNodePorts = vi.fn()
      .mockRejectedValueOnce(new Error('解析服务暂不可用'))
      .mockResolvedValueOnce({ inputs: [], outputs: [] })
    const { result } = renderHook(() => useNodeConfigDraft({ node: makeNode('a'), edges: [], resolve, debounceMs: 0 }))
    act(() => result.current.setDraft({ model: 'valid' }))
    await waitFor(() => expect(result.current.status).toBe('error'))
    expect(result.current.errorKind).toBe('resolve')
    act(() => result.current.retry())
    await waitFor(() => expect(result.current.status).toBe('ready'))
    expect(resolve).toHaveBeenCalledTimes(2)
    expect(result.current.draft).toEqual({ model: 'valid' })
  })

  it('取消旧节点解析并拒绝迟到 generation', async () => {
    const oldResult = deferred<ResolvedPorts>()
    const newResult = deferred<ResolvedPorts>()
    const signals: AbortSignal[] = []
    const resolve: ResolveNodePorts = vi.fn((_type, _version, _config, signal) => { signals.push(signal); return signals.length === 1 ? oldResult.promise : newResult.promise })
    const { result, rerender } = renderHook(({ node }) => useNodeConfigDraft({ node, edges: [], resolve, debounceMs: 0 }), { initialProps: { node: makeNode('a') } })
    act(() => result.current.setDraft({ model: 'old-a' }))
    await waitFor(() => expect(resolve).toHaveBeenCalledTimes(1))
    rerender({ node: makeNode('b') })
    act(() => result.current.setDraft({ model: 'new' }))
    await waitFor(() => expect(resolve).toHaveBeenCalledTimes(2))
    oldResult.resolve({ inputs: [], outputs: [{ key: 'stale', title: 'Stale', type: 'string', required: false, cardinality: 'one' }] })
    await waitFor(() => expect(result.current.nodeId).toBe('b'))
    expect(signals[0].aborted).toBe(true)
    expect(result.current.preview).toBeUndefined()
    newResult.resolve({ inputs: [], outputs: [] })
    await waitFor(() => expect(result.current.status).toBe('ready'))
  })

  it('reset、markApplied 和节点切换清理正确状态', async () => {
    const resolve: ResolveNodePorts = vi.fn().mockResolvedValue(nextPorts)
    const { result, rerender } = renderHook(({ currentNode }) => useNodeConfigDraft({ node: currentNode, edges: [], resolve, debounceMs: 0 }), { initialProps: { currentNode: makeNode('a') } })
    act(() => result.current.setDraft({ model: 'new' }))
    act(() => result.current.reset())
    expect(result.current.status).toBe('idle')
    act(() => result.current.setDraft({ model: 'new' }))
    await waitFor(() => expect(result.current.status).toBe('ready'))
    act(() => result.current.markApplied({ model: 'new' }, nextPorts))
    expect(result.current.dirty).toBe(false)
    expect(result.current.status).toBe('idle')
    rerender({ currentNode: makeNode('b') })
    expect(result.current.nodeId).toBe('b')
    expect(result.current.status).toBe('idle')
  })

  it('无效草稿不会解析且重置恢复节点配置', async () => {
    const resolve: ResolveNodePorts = vi.fn()
    const { result } = renderHook(() => useNodeConfigDraft({ node: makeNode('a'), edges: [], resolve, debounceMs: 0 }))
    expect(result.current.status).toBe('idle')
    expect(resolve).not.toHaveBeenCalled()
    act(() => result.current.setDraft({ model: '' }))
    expect(result.current.status).toBe('error')
    expect(result.current.errorKind).toBe('validation')
    expect(result.current.dirty).toBe(true)
    act(() => result.current.reset())
    expect(result.current.draft).toEqual({ model: 'old' })
    expect(result.current.dirty).toBe(false)
  })

  it('没有选中节点时返回有效的空验证状态', () => {
    const resolve: ResolveNodePorts = vi.fn()
    const { result } = renderHook(() => useNodeConfigDraft({ node: undefined, edges: [], resolve }))
    expect(result.current.validation).toEqual({ normalized: {}, errors: {}, valid: true })
    expect(result.current.status).toBe('idle')
  })
})
