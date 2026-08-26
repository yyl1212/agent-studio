import { act, renderHook } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { useStudioWorkbench } from './useStudioWorkbench'

describe('useStudioWorkbench', () => {
  it('脏配置延迟节点切换且取消后保持原节点', () => {
    const { result } = renderHook(() => useStudioWorkbench())
    act(() => result.current.request({ kind: 'config', nodeId: 'a' }, false))
    act(() => result.current.request({ kind: 'config', nodeId: 'b' }, true))
    expect(result.current.mode).toEqual({ kind: 'config', nodeId: 'a' })
    expect(result.current.pendingIntent).toEqual({ kind: 'config', nodeId: 'b' })
    act(() => result.current.resolveDirty('cancel'))
    expect(result.current.mode).toEqual({ kind: 'config', nodeId: 'a' })
    expect(result.current.pendingIntent).toBeUndefined()
  })

  it('放弃草稿后执行延迟意图', () => {
    const { result } = renderHook(() => useStudioWorkbench())
    act(() => result.current.request({ kind: 'config', nodeId: 'a' }, false))
    act(() => result.current.request({ kind: 'test' }, true))
    act(() => result.current.resolveDirty('discard'))
    expect(result.current.mode).toEqual({ kind: 'test' })
  })

  it('保留发布等外部意图供 Studio 在处理草稿后执行', () => {
    const { result } = renderHook(() => useStudioWorkbench())
    act(() => result.current.request({ kind: 'config', nodeId: 'a' }, false))
    act(() => result.current.request({ kind: 'publish' }, true))
    let intent
    act(() => { intent = result.current.resolveDirty('discard') })
    expect(intent).toEqual({ kind: 'publish' })
    expect(result.current.mode).toEqual({ kind: 'config', nodeId: 'a' })
  })

  it('脏配置会延迟 Agent 页面设置外部意图', () => {
    const { result } = renderHook(() => useStudioWorkbench())
    act(() => result.current.request({ kind: 'config', nodeId: 'a' }, false))
    act(() => result.current.request({ kind: 'agent-presentation' }, true))
    expect(result.current.pendingIntent).toEqual({ kind: 'agent-presentation' })
    let intent
    act(() => { intent = result.current.resolveDirty('discard') })
    expect(intent).toEqual({ kind: 'agent-presentation' })
    expect(result.current.mode).toEqual({ kind: 'config', nodeId: 'a' })
  })

  it('应用草稿后取出并清除待执行意图', () => {
    const { result } = renderHook(() => useStudioWorkbench())
    act(() => result.current.request({ kind: 'config', nodeId: 'a' }, false))
    act(() => result.current.request({ kind: 'test' }, true))
    let intent
    act(() => { intent = result.current.resolveDirty('apply') })
    expect(intent).toEqual({ kind: 'test' })
    expect(result.current.pendingIntent).toBeUndefined()
    expect(result.current.mode).toEqual({ kind: 'config', nodeId: 'a' })
  })
})
