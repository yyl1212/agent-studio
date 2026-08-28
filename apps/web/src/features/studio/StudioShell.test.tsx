import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { createRef } from 'react'
import { expect, it, vi } from 'vitest'

import { StudioShell } from './StudioShell'
import { WorkbenchPanel } from './WorkbenchPanel'

it('按固定层级呈现画布、命令条、工具和当前浮层', () => {
  const { rerender } = render(<StudioShell layer="library" commandBar={<span>命令条</span>} quickTools={<span>快捷工具</span>}
    canvas={<span>工作流画布</span>} nodeLibrary={<span>节点库</span>} onOpenNodeLibrary={vi.fn()} onRequestCloseTopLayer={vi.fn()} />)

  expect(screen.getByText('工作流画布').parentElement).toHaveClass('studio-shell-canvas')
  expect(screen.getByText('命令条').parentElement).toHaveClass('studio-shell-command')
  expect(screen.getByText('快捷工具').parentElement).toHaveClass('studio-shell-tools')
  expect(screen.getByText('节点库').parentElement).toHaveClass('studio-shell-library')

  rerender(<StudioShell layer="workbench" commandBar={<span>命令条</span>} quickTools={<span>快捷工具</span>}
    canvas={<span>工作流画布</span>} workbench={<span>配置工作台</span>} onOpenNodeLibrary={vi.fn()} onRequestCloseTopLayer={vi.fn()} />)
  expect(screen.queryByText('节点库')).not.toBeInTheDocument()
  expect(screen.getByText('配置工作台').parentElement).toHaveClass('studio-shell-workbench')
})

it('Escape 只关闭当前浮层并在关闭后恢复触发点焦点', async () => {
  const trigger = createRef<HTMLButtonElement>()
  const onClose = vi.fn()
  const props = { returnFocusRef: trigger, onOpenNodeLibrary: vi.fn(), onRequestCloseTopLayer: onClose, quickTools: <span>工具</span>, canvas: <span>画布</span> }
  const { rerender } = render(<StudioShell {...props} layer="library" commandBar={<button ref={trigger}>命令条</button>} nodeLibrary={<span>节点库</span>} />)

  fireEvent.keyDown(window, { key: 'Escape' })
  expect(onClose).toHaveBeenCalledOnce()
  rerender(<StudioShell {...props} layer="none" commandBar={<button ref={trigger}>命令条</button>} />)

  await waitFor(() => expect(trigger.current).toHaveFocus())
  fireEvent.keyDown(window, { key: 'Escape' })
  expect(onClose).toHaveBeenCalledOnce()
})

it('把 Ctrl/Cmd+K 转交给节点库动作', () => {
  const onOpenNodeLibrary = vi.fn()
  render(<StudioShell layer="none" commandBar={<span>命令条</span>} quickTools={<span>工具</span>} canvas={<span>画布</span>}
    onOpenNodeLibrary={onOpenNodeLibrary} onRequestCloseTopLayer={vi.fn()} />)

  fireEvent.keyDown(window, { key: 'k', ctrlKey: true })

  expect(onOpenNodeLibrary).toHaveBeenCalledOnce()
})

it('工作台已处理 Escape 时不会重复关闭', () => {
  const onClose = vi.fn()
  render(<StudioShell layer="workbench" commandBar={<span>命令条</span>} quickTools={<span>工具</span>} canvas={<span>画布</span>}
    workbench={<WorkbenchPanel titleId="panel-title" onRequestClose={onClose}><h2 id="panel-title">节点配置</h2></WorkbenchPanel>}
    onOpenNodeLibrary={vi.fn()} onRequestCloseTopLayer={onClose} />)

  fireEvent.keyDown(window, { key: 'Escape' })

  expect(onClose).toHaveBeenCalledOnce()
})

it('placement 是顶层 Escape 目标且输入框不会误触发取消', () => {
  const onClose = vi.fn()
  render(
    <StudioShell
      layer="placement"
      commandBar={<input aria-label="节点名称" />}
      quickTools={<span>工具</span>}
      canvas={<span>放置预览</span>}
      onOpenNodeLibrary={vi.fn()}
      onRequestCloseTopLayer={onClose}
    />,
  )

  fireEvent.keyDown(screen.getByLabelText('节点名称'), { key: 'Escape' })
  expect(onClose).not.toHaveBeenCalled()
  fireEvent.keyDown(window, { key: 'Escape' })
  expect(onClose).toHaveBeenCalledOnce()
})
