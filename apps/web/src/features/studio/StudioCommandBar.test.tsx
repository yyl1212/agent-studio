import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { expect, it, vi } from 'vitest'

import { StudioCommandBar, type StudioCommandBarProps } from './StudioCommandBar'

const commandProps: StudioCommandBarProps = {
  workflowName: '演示助手', saveState: 'saved', archived: false, exporting: false,
  runsHref: '/runs?workflowId=w1', actionError: '', testLabel: '测试运行', testDisabled: false,
  onTest: vi.fn(), onPublish: vi.fn(), onAgentPresentation: vi.fn(), onVersionHistory: vi.fn(), onExport: vi.fn(),
}

function renderCommandBar(props: Partial<StudioCommandBarProps> = {}) {
  return render(<MemoryRouter><StudioCommandBar {...commandProps} {...props} /></MemoryRouter>)
}

it('突出试运行和发布，并把低频动作放入更多操作', async () => {
  const onTest = vi.fn()
  const onVersionHistory = vi.fn()
  renderCommandBar({ onTest, onVersionHistory })

  await userEvent.click(screen.getByRole('button', { name: '测试运行' }))
  expect(onTest).toHaveBeenCalledOnce()
  expect(screen.getByRole('button', { name: '发布' })).toBeVisible()
  expect(screen.getByText('已保存')).toBeVisible()
  await userEvent.click(screen.getByText('更多操作'))
  const moreActions = screen.getByText('更多操作').closest('details')
  expect(moreActions).toHaveAttribute('open')
  expect(screen.getByRole('link', { name: '运行记录' })).toHaveAttribute('href', '/runs?workflowId=w1')
  await userEvent.click(screen.getByRole('button', { name: '版本历史' }))
  expect(onVersionHistory).toHaveBeenCalledOnce()
  expect(moreActions).not.toHaveAttribute('open')
})

it('归档时禁用写操作但保留版本历史和运行记录', async () => {
  renderCommandBar({ archived: true, testDisabled: true })
  expect(screen.getByRole('button', { name: '测试运行' })).toBeDisabled()
  expect(screen.getByRole('button', { name: '发布' })).toBeDisabled()
  await userEvent.click(screen.getByText('更多操作'))
  expect(screen.getByRole('button', { name: 'Agent 页面设置' })).toBeDisabled()
  expect(screen.getByRole('button', { name: '版本历史' })).toBeEnabled()
  expect(screen.getByRole('link', { name: '运行记录' })).toBeVisible()
})

it('持续呈现页面动作错误', () => {
  renderCommandBar({ actionError: '导出失败，请稍后重试' })
  expect(screen.getByRole('alert')).toHaveTextContent('导出失败，请稍后重试')
})

it('明确呈现失效连线门禁原因', () => {
  renderCommandBar({ invalidEdgeCount: 2, testDisabled: true })
  expect(screen.getByRole('status')).toHaveTextContent('存在 2 条失效连线，请修复后测试或发布')
  expect(screen.getByRole('button', { name: '测试运行' })).toBeDisabled()
  expect(screen.getByRole('button', { name: '发布' })).toBeDisabled()
})

it('保存失败提供重试，冲突只提供刷新并阻断提交动作', async () => {
  const onRetrySave = vi.fn()
  const view = renderCommandBar({ saveState: 'error', onRetrySave })
  await userEvent.click(screen.getByRole('button', { name: '重试保存' }))
  expect(onRetrySave).toHaveBeenCalledOnce()

  const onRefreshConflict = vi.fn()
  view.rerender(<MemoryRouter><StudioCommandBar {...commandProps} saveState="conflict" onRefreshConflict={onRefreshConflict} /></MemoryRouter>)
  expect(screen.getByRole('button', { name: '测试运行' })).toBeDisabled()
  expect(screen.getByRole('button', { name: '发布' })).toBeDisabled()
  await userEvent.click(screen.getByRole('button', { name: '刷新工作流' }))
  expect(onRefreshConflict).toHaveBeenCalledOnce()
})
