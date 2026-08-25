import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import type { JSONSchema } from '../../components/schema-form/types'
import { TestRunPanel } from './TestRunPanel'

const schema: JSONSchema = {
  type: 'object',
  properties: { topic: { type: 'string', title: '主题' } },
  required: ['topic'],
}

describe('TestRunPanel', () => {
  it('作为工作台内容提交输入而不创建第二个 dialog', async () => {
    const onRun = vi.fn()
    render(<TestRunPanel schema={{ type: 'object', required: ['topic'], properties: { topic: { type: 'string', title: '主题', minLength: 1 } } }} events={[]} running={false} error="" onRun={onRun} onCancel={vi.fn()} />)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    await userEvent.type(screen.getByLabelText('主题'), 'Agent Studio')
    await userEvent.click(screen.getByRole('button', { name: '运行' }))
    expect(onRun).toHaveBeenCalledWith({ topic: 'Agent Studio' })
  })

  it('父组件重渲染时保留尚未提交的运行输入', async () => {
    const onRun = vi.fn()
    const props = { schema, events: [], running: false, error: '', onRun, onCancel: vi.fn() }
    const { rerender } = render(<TestRunPanel {...props} />)

    await userEvent.type(screen.getByLabelText('主题'), '保留输入')
    rerender(<TestRunPanel {...props} error="状态已更新" />)
    await userEvent.click(screen.getByRole('button', { name: '运行' }))

    expect(onRun).toHaveBeenCalledWith({ topic: '保留输入' })
  })
})
