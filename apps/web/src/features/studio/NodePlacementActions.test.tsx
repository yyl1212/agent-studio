import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'

import { NodePlacementActions } from './NodePlacementActions'

it('确认按钮进入焦点并只调用一次确认', async () => {
  const onConfirm = vi.fn()
  const onCancel = vi.fn()
  render(<NodePlacementActions nodeTitle="提示词模板" onConfirm={onConfirm} onCancel={onCancel} />)

  expect(screen.getByRole('button', { name: '确认放置' })).toHaveFocus()
  await userEvent.click(screen.getByRole('button', { name: '确认放置' }))

  expect(onConfirm).toHaveBeenCalledOnce()
  expect(onCancel).not.toHaveBeenCalled()
})

it('取消只调用取消', async () => {
  const onConfirm = vi.fn()
  const onCancel = vi.fn()
  render(<NodePlacementActions nodeTitle="提示词模板" onConfirm={onConfirm} onCancel={onCancel} />)

  await userEvent.click(screen.getByRole('button', { name: '取消' }))

  expect(onCancel).toHaveBeenCalledOnce()
  expect(onConfirm).not.toHaveBeenCalled()
})

it('操作条提供节点名与中文步骤提示', () => {
  render(<NodePlacementActions nodeTitle="提示词模板" onConfirm={vi.fn()} onCancel={vi.fn()} />)

  expect(screen.getByText('提示词模板')).toBeVisible()
  expect(screen.getByText('选择位置 → 确认放置')).toBeVisible()
})
