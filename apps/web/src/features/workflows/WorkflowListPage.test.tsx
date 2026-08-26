import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { APIError, api } from '../../lib/api/client'
import { WorkflowListPage } from './WorkflowListPage'

describe('WorkflowListPage', () => {
  afterEach(() => vi.restoreAllMocks())

  it('显示空状态并可创建工作流', async () => {
    vi.spyOn(api, 'listWorkflowSummaries').mockResolvedValue({ items: [], nextCursor: null })
    vi.spyOn(api, 'createWorkflow').mockResolvedValue({
      id: 'w1', name: '演示', slug: 'demo', description: '', draftGraph: { schemaVersion: 1, nodes: [], edges: [] },
      agentPresentation: { title: '演示', description: '', accent: 'indigo', submitLabel: '运行 Agent', resultMode: 'auto' },
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
    vi.spyOn(api, 'listWorkflowSummaries').mockResolvedValue({ items: [], nextCursor: null })
    vi.spyOn(api, 'createWorkflow').mockRejectedValue(new APIError(409, 'WORKFLOW_SLUG_CONFLICT', 'Agent 地址标识已存在'))
    render(<MemoryRouter><WorkflowListPage /></MemoryRouter>)
    await screen.findByText('还没有工作流')
    await userEvent.click(screen.getByRole('button', { name: '新建工作流' }))
    await userEvent.type(screen.getByLabelText('名称'), '演示')
    await userEvent.type(screen.getByLabelText('Agent 地址标识'), 'demo')
    await userEvent.click(screen.getByRole('button', { name: '创建' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Agent 地址标识已存在')
  })

  it('从列表页打开模板导入并导航到新工作流', async () => {
    vi.spyOn(api, 'listWorkflowSummaries').mockResolvedValue({ items: [], nextCursor: null })
    vi.spyOn(api, 'previewWorkflowTemplate').mockResolvedValue({
      valid: true,
      metadata: { name: '模板', description: '' },
      summary: { nodeCount: 0, edgeCount: 0, inputSchema: {}, nodeTypes: [] },
      issues: [],
    })
    vi.spyOn(api, 'importWorkflowTemplate').mockResolvedValue({
      id: 'imported', name: '模板', slug: 'imported-workflow', description: '',
      agentPresentation: { title: '模板', description: '', accent: 'indigo', submitLabel: '运行 Agent', resultMode: 'auto' },
      draftGraph: { schemaVersion: 1, nodes: [], edges: [] }, draftRevision: 1,
      createdAt: '2026-08-19T00:00:00Z', updatedAt: '2026-08-19T00:00:00Z',
    })
    render(
      <MemoryRouter initialEntries={['/']}>
        <Routes>
          <Route path="/" element={<WorkflowListPage />} />
          <Route path="/workflows/:id" element={<p>已打开导入工作流</p>} />
        </Routes>
      </MemoryRouter>,
    )
    await screen.findByText('还没有工作流')
    await userEvent.click(screen.getByRole('button', { name: '导入模板' }))
    const template = {
      apiVersion: 'agent-studio.dev/v1alpha1' as const,
      kind: 'WorkflowTemplate' as const,
      metadata: { name: '模板', description: '' },
      spec: { graph: { schemaVersion: 1 as const, nodes: [], edges: [] } },
    }
    await userEvent.upload(screen.getByLabelText('选择模板文件'), new File([JSON.stringify(template)], 'template.json', { type: 'application/json' }))
    await screen.findByText('0 个节点 · 0 条连线')
    await userEvent.click(screen.getByRole('button', { name: '导入并打开' }))
    expect(await screen.findByText('已打开导入工作流')).toBeInTheDocument()
  })
})
