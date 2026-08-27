import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'

import { BoundaryRepairBanner } from './BoundaryRepairBanner'
import type { StudioEdge, StudioNode } from './types'
import { diagnoseWorkflowBoundaries } from './workflowBoundaries'

const studioNode = (id: string, nodeType: string): StudioNode => ({
  id,
  type: 'studio',
  position: { x: 0, y: 0 },
  data: {
    nodeType,
    typeVersion: '1',
    config: {},
    ports: { inputs: [], outputs: [] },
    issues: [],
  },
})

const studioEdge = (
  id: string,
  source: string,
  target: string,
): StudioEdge => ({
  id,
  source,
  target,
  sourceHandle: 'out',
  targetHandle: 'in',
  data: {},
})

it('重复开始节点要求选择 keeper 并预览关联边', async () => {
  const user = userEvent.setup()
  const onConfirm = vi.fn()
  const nodes = [
    studioNode('start-a', 'start'),
    studioNode('start-b', 'start'),
    studioNode('end', 'end'),
  ]
  const edges = [studioEdge('start-b-end', 'start-b', 'end')]
  const diagnosis = diagnoseWorkflowBoundaries(nodes)
  render(
    <BoundaryRepairBanner
      diagnosis={diagnosis}
      nodes={nodes}
      edges={edges}
      busy={false}
      error=""
      onConfirm={onConfirm}
    />,
  )

  await user.click(screen.getByRole('button', { name: '修复工作流边界' }))
  expect(
    screen.getByRole('dialog', { name: '修复工作流边界' }),
  ).toBeVisible()
  expect(
    screen.getByText(/将移除 1 个重复节点和 1 条关联连线/),
  ).toBeInTheDocument()
  const confirm = screen.getByRole('button', { name: '确认修复' })
  expect(confirm).toBeDisabled()

  await user.click(screen.getByRole('radio', { name: /保留 start-a/ }))
  await user.click(confirm)
  expect(onConfirm).toHaveBeenCalledWith({ keepStartId: 'start-a' })
})

it('缺失边界无需 keeper，busy 与错误状态保持可访问', async () => {
  const user = userEvent.setup()
  const onConfirm = vi.fn()
  const nodes = [studioNode('end', 'end')]
  const diagnosis = diagnoseWorkflowBoundaries(nodes)
  const { rerender } = render(
    <BoundaryRepairBanner
      diagnosis={diagnosis}
      nodes={nodes}
      edges={[]}
      busy={false}
      error=""
      onConfirm={onConfirm}
    />,
  )

  await user.click(screen.getByRole('button', { name: '修复工作流边界' }))
  expect(screen.queryByRole('radio')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: '确认修复' }))
  expect(onConfirm).toHaveBeenCalledWith({})

  rerender(
    <BoundaryRepairBanner
      diagnosis={diagnosis}
      nodes={nodes}
      edges={[]}
      busy
      error="保存失败"
      onConfirm={onConfirm}
    />,
  )
  expect(screen.getByRole('alert')).toHaveTextContent('保存失败')
  expect(screen.getByRole('button', { name: '确认修复' })).toBeDisabled()
})

it('取消修复零回调并恢复横幅触发点焦点', async () => {
  const user = userEvent.setup()
  const onConfirm = vi.fn()
  const nodes = [studioNode('end', 'end')]
  render(
    <BoundaryRepairBanner
      diagnosis={diagnoseWorkflowBoundaries(nodes)}
      nodes={nodes}
      edges={[]}
      busy={false}
      error=""
      onConfirm={onConfirm}
    />,
  )

  const trigger = screen.getByRole('button', { name: '修复工作流边界' })
  await user.click(trigger)
  await user.click(screen.getByRole('button', { name: '取消' }))
  expect(onConfirm).not.toHaveBeenCalled()
  expect(trigger).toHaveFocus()
})
