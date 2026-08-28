import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { api, type NodeDefinition, type Workflow } from '../../lib/api/client'
import { RECENT_NODE_STORAGE_KEY } from './nodeLibraryModel'
import { StudioPage } from './StudioPage'
import type { WorkflowCanvasHandle, WorkflowCanvasProps } from './WorkflowCanvas'

vi.mock('../../lib/api/client', async (importOriginal) => {
  const original = await importOriginal<typeof import('../../lib/api/client')>()
  return { ...original, api: { ...original.api } }
})

vi.mock('./WorkflowCanvas', async () => {
  const React = await import('react')
  type MockCanvasProps = Pick<
    WorkflowCanvasProps,
    | 'placement'
    | 'onPlacementConfirm'
    | 'onNodeDefinitionDrop'
    | 'onConnectionEnd'
  >
  return {
    WorkflowCanvas: React.forwardRef<WorkflowCanvasHandle, MockCanvasProps>(
      function MockCanvas(props, ref) {
        React.useImperativeHandle(ref, () => ({
          getViewportCenter: () => ({ x: 640, y: 360 }),
          screenToFlowPosition: () => ({ x: 480, y: 260 }),
          fitView: async () => true,
        }))
        return (
          <div aria-label="工作流画布">
            {props.placement && <span>点击画布放置，Esc 取消</span>}
            <button type="button" onClick={() => props.onPlacementConfirm?.()}>
              确认预览位置
            </button>
            <button type="button" onClick={() => props.onNodeDefinitionDrop?.('template@1', { x: 700, y: 300 })}>
              拖放模板节点
            </button>
            <button type="button" onClick={() => props.onNodeDefinitionDrop?.('removed@1', { x: 700, y: 300 })}>
              拖放失效节点
            </button>
            <button type="button" onClick={() => props.onConnectionEnd?.({ sourceNodeId: 'start', sourceHandleId: 'topic', clientX: 480, clientY: 260 })}>
              从 topic 释放到空白
            </button>
          </div>
        )
      },
    ),
  }
})

const packageSummary = {
  name: 'agent-studio.dev/core',
  displayName: 'Agent Studio Core',
  version: 'v0.5.0',
  license: 'Apache-2.0',
  repository: 'https://github.com/yyl1212/agent-studio',
  source: 'builtin' as const,
}

const port = (key: string, type: 'string' | 'any'): NodeDefinition['inputs'][number] => ({
  key,
  title: key,
  type,
  required: false,
  cardinality: 'one',
})

const definitions = [
  { type: 'start', version: '1', title: '开始', description: '', category: '流程', configSchema: { type: 'object' }, inputs: [], outputs: [port('topic', 'string')], capabilities: [], executionSafety: 'pure' as const, package: packageSummary },
  { type: 'end', version: '1', title: '结束', description: '', category: '流程', configSchema: { type: 'object' }, inputs: [port('result', 'any')], outputs: [], capabilities: [], executionSafety: 'pure' as const, package: packageSummary },
  { type: 'template', version: '1', title: '提示词模板', description: '', category: '文本', configSchema: { type: 'object', required: ['template'], properties: { template: { type: 'string', title: '模板' } } }, inputs: [port('topic', 'string'), port('context', 'any')], outputs: [port('text', 'string')], capabilities: [], executionSafety: 'pure' as const, package: packageSummary },
] satisfies NodeDefinition[]

const workflow = {
  id: 'w1', name: '演示助手', slug: 'demo', description: '', draftRevision: 1,
  agentPresentation: { title: '演示助手', description: '', accent: 'indigo' as const, submitLabel: '运行 Agent', resultMode: 'auto' as const },
  draftGraph: {
    schemaVersion: 1 as const,
    nodes: [
      { id: 'start', type: 'start', typeVersion: '1', position: { x: 100, y: 100 }, config: { fields: [{ key: 'topic', label: '主题', type: 'text', required: true }] } },
      { id: 'end', type: 'end', typeVersion: '1', position: { x: 500, y: 100 }, config: {} },
    ],
    edges: [],
  },
  createdAt: '2026-08-17T00:00:00Z', updatedAt: '2026-08-17T00:00:00Z',
} satisfies Workflow

const renderStudio = () => render(
  <MemoryRouter initialEntries={['/workflows/w1']}>
    <Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes>
  </MemoryRouter>,
)

const openLibraryAndChoose = async () => {
  await userEvent.click(await screen.findByRole('button', { name: '添加节点' }))
  await userEvent.click(screen.getByRole('button', { name: /^提示词模板/ }))
}

describe('Studio node creation', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    window.localStorage.clear()
    vi.spyOn(api, 'getWorkflow').mockResolvedValue(workflow)
    vi.spyOn(api, 'listNodeTypes').mockResolvedValue(definitions)
    vi.spyOn(api, 'resolveNodeType').mockImplementation(async (type) => {
      const definition = definitions.find((candidate) => candidate.type === type)
      return { inputs: definition?.inputs ?? [], outputs: definition?.outputs ?? [] }
    })
    vi.spyOn(api, 'saveWorkflow').mockResolvedValue({ ...workflow, draftRevision: 2 })
  })

  it('点击卡片只进入预览，画布确认后才保存和记录最近', async () => {
    renderStudio()
    await openLibraryAndChoose()

    expect(screen.getByText('点击画布放置，Esc 取消')).toBeVisible()
    expect(api.saveWorkflow).not.toHaveBeenCalled()
    expect(localStorage.getItem(RECENT_NODE_STORAGE_KEY)).toBeNull()

    await userEvent.click(screen.getByRole('button', { name: '确认预览位置' }))

    await vi.waitFor(() => expect(api.saveWorkflow).toHaveBeenCalledOnce(), { timeout: 2000 })
    expect(screen.getByRole('dialog', { name: '提示词模板' })).toBeVisible()
    expect(JSON.parse(localStorage.getItem(RECENT_NODE_STORAGE_KEY) ?? '[]')).toEqual(['template@1'])
  })

  it('连线末端选择端口后原子创建节点和一条边', async () => {
    renderStudio()
    await userEvent.click(await screen.findByRole('button', { name: '从 topic 释放到空白' }))
    await userEvent.click(screen.getByRole('button', { name: /^提示词模板/ }))
    await userEvent.click(screen.getByRole('radio', { name: /context/ }))
    await userEvent.click(screen.getByRole('button', { name: '添加并连接' }))

    await vi.waitFor(() => expect(api.saveWorkflow).toHaveBeenCalledOnce(), { timeout: 2000 })
    const graph = vi.mocked(api.saveWorkflow).mock.calls[0][1].graph
    expect(graph.nodes.filter((node) => node.type === 'template')).toHaveLength(1)
    expect(graph.edges).toContainEqual(expect.objectContaining({
      source: 'start', sourcePort: 'topic', targetPort: 'context',
    }))
  })

  it('Escape 取消预览不保存', async () => {
    renderStudio()
    await openLibraryAndChoose()

    await userEvent.keyboard('{Escape}')

    expect(screen.queryByText('点击画布放置，Esc 取消')).not.toBeInTheDocument()
    expect(api.saveWorkflow).not.toHaveBeenCalled()
  })

  it('拖放直接复用创建事务并只保存一次', async () => {
    renderStudio()
    await userEvent.click(await screen.findByRole('button', { name: '添加节点' }))
    await userEvent.click(screen.getByRole('button', { name: '拖放模板节点' }))

    await vi.waitFor(() => expect(api.saveWorkflow).toHaveBeenCalledOnce(), { timeout: 2000 })
    expect(screen.getByRole('dialog', { name: '提示词模板' })).toBeVisible()
  })

  it('失效定义拖放保留目录并提示', async () => {
    renderStudio()
    await screen.findByText('演示助手')
    await userEvent.click(screen.getByRole('button', { name: '添加节点' }))
    await userEvent.click(screen.getByRole('button', { name: '拖放失效节点' }))

    expect(api.saveWorkflow).not.toHaveBeenCalled()
    expect(screen.getByRole('dialog', { name: '节点库' })).toBeVisible()
    expect(screen.getByRole('alert')).toHaveTextContent('节点定义已更新，请重新选择')
  })
})
