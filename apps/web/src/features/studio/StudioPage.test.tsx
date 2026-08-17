import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '../../lib/api/client'
import { StudioPage } from './StudioPage'

vi.mock('../../lib/api/client', async (importOriginal) => {
  const original = await importOriginal<typeof import('../../lib/api/client')>()
  return { ...original, api: { ...original.api } }
})

const workflow = {
  id: 'w1', name: '演示助手', slug: 'demo', description: '', draftRevision: 1,
  draftGraph: {
    schemaVersion: 1 as const,
    nodes: [
      { id: 'start', type: 'start', typeVersion: '1', position: { x: 100, y: 100 }, config: { fields: [] } },
      { id: 'end', type: 'end', typeVersion: '1', position: { x: 500, y: 100 }, config: {} },
    ],
    edges: [],
  },
  createdAt: '2026-08-17T00:00:00Z', updatedAt: '2026-08-17T00:00:00Z',
}

const definitions = [
  { type: 'start', version: '1', title: '开始', description: '', category: '流程', configSchema: { type: 'object' }, inputs: [], outputs: [] },
  { type: 'end', version: '1', title: '结束', description: '', category: '流程', configSchema: { type: 'object' }, inputs: [], outputs: [] },
  { type: 'template', version: '1', title: '提示词模板', description: '', category: '文本', configSchema: { type: 'object', properties: { template: { type: 'string', title: '模板', 'x-ui-widget': 'textarea' } }, required: ['template'] }, inputs: [], outputs: [{ key: 'text', title: '文本', type: 'string' as const, required: false, cardinality: 'one' as const }] },
]

describe('StudioPage', () => {
  beforeEach(() => {
    vi.spyOn(api, 'getWorkflow').mockResolvedValue(workflow)
    vi.spyOn(api, 'listNodeTypes').mockResolvedValue(definitions)
    vi.spyOn(api, 'resolveNodeType').mockResolvedValue({ inputs: [], outputs: [] })
    vi.spyOn(api, 'saveWorkflow').mockResolvedValue({ ...workflow, draftRevision: 2 })
  })

  it('打开节点库、添加节点并在右侧配置', async () => {
    render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
    expect(await screen.findByText('演示助手')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '添加节点' }))
    await userEvent.click(screen.getByRole('button', { name: '提示词模板' }))
    expect(screen.getByRole('dialog', { name: '节点配置' })).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('模板'), { target: { value: '回答：{{topic}}' } })
    await vi.waitFor(() => expect(api.resolveNodeType).toHaveBeenCalledWith('template', '1', expect.objectContaining({ template: '回答：{{topic}}' }), expect.any(AbortSignal)))
  })

  it('发布前等待保存队列完成并使用新 revision', async () => {
    let resolveSave!: (value: typeof workflow) => void
    vi.mocked(api.saveWorkflow).mockReturnValueOnce(new Promise((resolve) => { resolveSave = resolve }))
    vi.spyOn(api, 'validateWorkflow').mockResolvedValue({ valid: true, issues: [] })
    vi.spyOn(api, 'publishWorkflow').mockResolvedValue({
      id: 'v1', workflowId: 'w1', version: 1, graph: workflow.draftGraph, inputSchema: {}, createdAt: '2026-08-17T00:00:00Z',
    })
    render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
    await screen.findByText('演示助手')
    await userEvent.click(screen.getByRole('button', { name: '添加节点' }))
    await userEvent.click(screen.getByRole('button', { name: '提示词模板' }))
    await userEvent.click(screen.getByRole('button', { name: '发布' }))
    await userEvent.click(screen.getByRole('button', { name: '确认发布' }))
    await vi.waitFor(() => expect(api.saveWorkflow).toHaveBeenCalled())
    expect(api.validateWorkflow).not.toHaveBeenCalled()
    resolveSave({ ...workflow, draftRevision: 2 })
    await vi.waitFor(() => expect(api.publishWorkflow).toHaveBeenCalledWith('w1', 2))
  })
})
