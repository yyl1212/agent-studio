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
const makeNode = (id: string): StudioNode => ({ id, type: 'studio', position: { x: 0, y: 0 }, data: { nodeType: 'dynamic', typeVersion: '1', config: { model: '' }, definition, ports: { inputs: [], outputs: [] }, issues: [] } })
const deferred = <T,>() => { let resolve!: (value: T) => void; const promise = new Promise<T>((done) => { resolve = done }); return { promise, resolve } }

describe('useNodeConfigDraft', () => {
  it('取消旧节点解析并拒绝迟到 generation', async () => {
    const oldResult = deferred<ResolvedPorts>()
    const newResult = deferred<ResolvedPorts>()
    const signals: AbortSignal[] = []
    const resolve: ResolveNodePorts = vi.fn((_type, _version, _config, signal) => { signals.push(signal); return signals.length === 1 ? oldResult.promise : newResult.promise })
    const { result, rerender } = renderHook(({ node }) => useNodeConfigDraft({ node, edges: [], resolve, debounceMs: 0 }), { initialProps: { node: makeNode('a') } })
    act(() => result.current.setDraft({ model: 'old' }))
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

  it('无效草稿不会解析且重置恢复节点配置', async () => {
    const resolve: ResolveNodePorts = vi.fn()
    const { result } = renderHook(() => useNodeConfigDraft({ node: makeNode('a'), edges: [], resolve, debounceMs: 0 }))
    expect(result.current.status).toBe('invalid')
    expect(resolve).not.toHaveBeenCalled()
    act(() => result.current.setDraft({ model: 'valid' }))
    expect(result.current.dirty).toBe(true)
    act(() => result.current.reset())
    expect(result.current.draft).toEqual({ model: '' })
    expect(result.current.dirty).toBe(false)
  })
})
