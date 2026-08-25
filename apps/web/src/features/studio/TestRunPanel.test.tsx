import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'

import { TestRunPanel } from './TestRunPanel'

it('作为工作台内容提交输入而不创建第二个 dialog', async () => {
  const onRun = vi.fn()
  render(<TestRunPanel schema={{ type: 'object', required: ['topic'], properties: { topic: { type: 'string', title: '主题', minLength: 1 } } }} events={[]} running={false} error="" onRun={onRun} onCancel={vi.fn()} />)
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  await userEvent.type(screen.getByLabelText('主题'), 'Agent Studio')
  await userEvent.click(screen.getByRole('button', { name: '运行' }))
  expect(onRun).toHaveBeenCalledWith({ topic: 'Agent Studio' })
})
