import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '../../lib/api/client'
import { WorkflowListPage } from './WorkflowListPage'

describe('WorkflowListPage', () => {
  afterEach(() => vi.restoreAllMocks())

  it('显示空状态并可创建工作流', async () => {
    vi.spyOn(api, 'listWorkflows').mockResolvedValue([])
    vi.spyOn(api, 'createWorkflow').mockResolvedValue({
      id: 'w1', name: '演示', slug: 'demo', description: '', draftGraph: { schemaVersion: 1, nodes: [], edges: [] },
      draftRevision: 1, createdAt: '2026-08-17T00:00:00Z', updatedAt: '2026-08-17T00:00:00Z',
    })
    render(<MemoryRouter><WorkflowListPage /></MemoryRouter>)
    expect(await screen.findByText('还没有工作流')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '新建工作流' }))
    await userEvent.type(screen.getByLabelText('名称'), '演示')
    await userEvent.type(screen.getByLabelText('Agent 地址标识'), 'demo')
    await userEvent.click(screen.getByRole('button', { name: '创建' }))
    expect(api.createWorkflow).toHaveBeenCalledWith({ name: '演示', slug: 'demo', description: '' })
  })

  it('显示 slug 冲突的可访问错误', async () => {
    vi.spyOn(api, 'listWorkflows').mockResolvedValue([])
    vi.spyOn(api, 'createWorkflow').mockRejectedValue({ code: 'WORKFLOW_SLUG_CONFLICT' })
    render(<MemoryRouter><WorkflowListPage /></MemoryRouter>)
    await screen.findByText('还没有工作流')
    await userEvent.click(screen.getByRole('button', { name: '新建工作流' }))
    await userEvent.type(screen.getByLabelText('名称'), '演示')
    await userEvent.type(screen.getByLabelText('Agent 地址标识'), 'demo')
    await userEvent.click(screen.getByRole('button', { name: '创建' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Agent 地址标识已存在')
  })
})
