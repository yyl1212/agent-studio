import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { WorkbenchPanel } from './WorkbenchPanel'

describe('WorkbenchPanel', () => {
  beforeEach(() => window.localStorage.clear())

  it('夹紧已保存宽度并支持键盘调整', async () => {
    window.localStorage.setItem('agent-studio.workbench-width', '9999')
    render(<WorkbenchPanel titleId="panel-title" onRequestClose={vi.fn()}><h2 id="panel-title">节点配置</h2></WorkbenchPanel>)
    const separator = screen.getByRole('separator', { name: '调整工作台宽度' })
    expect(separator).toHaveAttribute('aria-valuemin', '320')
    expect(separator).toHaveAttribute('aria-valuemax', '480')
    expect(separator).toHaveAttribute('aria-valuenow', '480')
    await userEvent.type(separator, '{arrowleft}')
    expect(separator).toHaveAttribute('aria-valuenow', '464')
    expect(window.localStorage.getItem('agent-studio.workbench-width')).toBe('464')
  })

  it('保持非模态语义并从 400 像素按步长调整', async () => {
    window.localStorage.setItem('agent-studio.workbench-width', '400')
    render(<WorkbenchPanel titleId="panel-title" onRequestClose={vi.fn()}><h2 id="panel-title">节点配置</h2></WorkbenchPanel>)
    expect(screen.getByRole('dialog', { name: '节点配置' })).not.toHaveAttribute('aria-modal', 'true')
    const separator = screen.getByRole('separator', { name: '调整工作台宽度' })
    separator.focus()
    await userEvent.keyboard('{ArrowLeft}')
    expect(separator).toHaveAttribute('aria-valuenow', '384')
  })

  it('关闭按钮使用调用方行为', async () => {
    const onClose = vi.fn()
    render(<WorkbenchPanel titleId="panel-title" onRequestClose={onClose}><h2 id="panel-title">测试运行</h2></WorkbenchPanel>)
    await userEvent.click(screen.getByRole('button', { name: '关闭工作台' }))
    expect(onClose).toHaveBeenCalledOnce()
  })

  it('直接使用时 Escape 请求关闭工作台', async () => {
    const onClose = vi.fn()
    render(<WorkbenchPanel titleId="panel-title" onRequestClose={onClose}><h2 id="panel-title">节点配置</h2></WorkbenchPanel>)
    await userEvent.keyboard('{Escape}')
    expect(onClose).toHaveBeenCalledOnce()
  })

	 it('关闭被锁定时禁用按钮并拦截 Escape，解锁后恢复', async () => {
		const onClose = vi.fn()
		const { rerender } = render(<WorkbenchPanel titleId="panel-title" onRequestClose={onClose} closeDisabled><h2 id="panel-title">版本记录</h2></WorkbenchPanel>)
		expect(screen.getByRole('button', { name: '关闭工作台' })).toBeDisabled()
		await userEvent.keyboard('{Escape}')
		expect(onClose).not.toHaveBeenCalled()
		rerender(<WorkbenchPanel titleId="panel-title" onRequestClose={onClose} closeDisabled={false}><h2 id="panel-title">版本记录</h2></WorkbenchPanel>)
		await userEvent.keyboard('{Escape}')
		expect(onClose).toHaveBeenCalledOnce()
	 })
})
