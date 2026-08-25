import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'

import { Button } from './Button'
import { ConfirmDialog } from './ConfirmDialog'
import { StatusBadge } from './StatusBadge'

describe('基础界面控件', () => {
  it('按钮和状态徽标通过语义属性表达状态', () => {
    render(<><Button variant="danger" disabled>删除</Button><StatusBadge tone="success">已保存</StatusBadge></>)
    expect(screen.getByRole('button', { name: '删除' })).toBeDisabled()
    expect(screen.getByText('已保存')).toHaveAttribute('data-tone', 'success')
  })

  it('确认框不能通过遮罩静默放弃并在关闭后恢复触发焦点', async () => {
    const onDiscard = vi.fn()
    const onCancel = vi.fn()
    function Fixture() {
      const [open, setOpen] = useState(false)
      return <><button type="button" onClick={() => setOpen(true)}>打开配置</button><ConfirmDialog open={open} title="未应用配置" description="存在更改" confirmLabel="应用" discardLabel="放弃" cancelLabel="继续编辑" onConfirm={vi.fn()} onDiscard={onDiscard} onCancel={() => { onCancel(); setOpen(false) }} /></>
    }
    render(<Fixture />)
    const trigger = screen.getByRole('button', { name: '打开配置' })
    await userEvent.click(trigger)
    await userEvent.click(screen.getByTestId('dialog-backdrop'))
    expect(onDiscard).not.toHaveBeenCalled()
    expect(onCancel).not.toHaveBeenCalled()
    await userEvent.click(screen.getByRole('button', { name: '继续编辑' }))
    expect(onCancel).toHaveBeenCalledOnce()
    expect(trigger).toHaveFocus()
  })

  it('条件未就绪时禁用主确认操作', () => {
    render(<ConfirmDialog open title="未应用配置" description="端口仍在解析" confirmLabel="应用" discardLabel="放弃" cancelLabel="继续编辑" confirmDisabled onConfirm={vi.fn()} onDiscard={vi.fn()} onCancel={vi.fn()} />)
    expect(screen.getByRole('button', { name: '应用' })).toBeDisabled()
  })
})
