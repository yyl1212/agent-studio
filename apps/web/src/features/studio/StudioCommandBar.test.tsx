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
  expect(screen.getByRole('link', { name: '运行记录' })).toHaveAttribute('href', '/runs?workflowId=w1')
  await userEvent.click(screen.getByRole('button', { name: '版本历史' }))
  expect(onVersionHistory).toHaveBeenCalledOnce()
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
