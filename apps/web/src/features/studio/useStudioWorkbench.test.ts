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
})
