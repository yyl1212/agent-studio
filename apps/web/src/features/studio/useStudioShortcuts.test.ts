import { fireEvent, renderHook } from '@testing-library/react'
import { expect, it, vi } from 'vitest'

import { useStudioShortcuts } from './useStudioShortcuts'

it('Ctrl/Cmd+K 打开节点面板，Escape 关闭最上层', () => {
  const onOpenNodeLibrary = vi.fn()
  const onEscape = vi.fn()
  renderHook(() => useStudioShortcuts({ onOpenNodeLibrary, onEscape }))

  fireEvent.keyDown(window, { key: 'k', metaKey: true })
  fireEvent.keyDown(window, { key: 'K', ctrlKey: true })
  fireEvent.keyDown(window, { key: 'Escape' })

  expect(onOpenNodeLibrary).toHaveBeenCalledTimes(2)
  expect(onEscape).toHaveBeenCalledOnce()
})

it('输入控件、contenteditable 和组合输入期间不触发', () => {
  const onOpenNodeLibrary = vi.fn()
  const onEscape = vi.fn()
  renderHook(() => useStudioShortcuts({ onOpenNodeLibrary, onEscape }))
  const input = document.createElement('input')
  const editable = document.createElement('div')
  editable.setAttribute('contenteditable', 'true')
  document.body.append(input, editable)

  fireEvent.keyDown(input, { key: 'k', metaKey: true })
  fireEvent.keyDown(editable, { key: 'Escape' })
  fireEvent.keyDown(window, { key: 'k', metaKey: true, isComposing: true })

  expect(onOpenNodeLibrary).not.toHaveBeenCalled()
  expect(onEscape).not.toHaveBeenCalled()
  input.remove()
  editable.remove()
})

it('子组件已处理的按键不再触发全局动作', () => {
  const onEscape = vi.fn()
  renderHook(() => useStudioShortcuts({ onOpenNodeLibrary: vi.fn(), onEscape }))
  const event = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true })
  event.preventDefault()

  window.dispatchEvent(event)

  expect(onEscape).not.toHaveBeenCalled()
})

it('卸载时移除全局监听', () => {
  const onOpenNodeLibrary = vi.fn()
  const { unmount } = renderHook(() => useStudioShortcuts({ onOpenNodeLibrary, onEscape: vi.fn() }))
  unmount()

  fireEvent.keyDown(window, { key: 'k', metaKey: true })

  expect(onOpenNodeLibrary).not.toHaveBeenCalled()
})
