import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'

import { StudioQuickTools } from './StudioQuickTools'

it('提供可见的添加、适配和快捷键帮助', async () => {
  const onAdd = vi.fn()
  const onFitView = vi.fn()
  render(<StudioQuickTools disabled={false} onAdd={onAdd} onFitView={onFitView} />)

  await userEvent.click(screen.getByRole('button', { name: '添加节点' }))
  await userEvent.click(screen.getByRole('button', { name: '适配工作流' }))
  expect(onAdd).toHaveBeenCalledOnce()
  expect(onFitView).toHaveBeenCalledOnce()
  await userEvent.click(screen.getByText('快捷键帮助'))
  expect(screen.getByText('Ctrl/⌘ + K')).toBeVisible()
})

it('只在只读状态禁用添加，仍允许适配和查看帮助', () => {
  render(<StudioQuickTools disabled onAdd={vi.fn()} onFitView={vi.fn()} />)
  expect(screen.getByRole('button', { name: '添加节点' })).toBeDisabled()
  expect(screen.getByRole('button', { name: '适配工作流' })).toBeEnabled()
  expect(screen.getByText('快捷键帮助')).toBeVisible()
})
