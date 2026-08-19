import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { APIError, api, type NodeDefinition } from '../../lib/api/client'
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

const rawDefinitions = [
  { type: 'start', version: '1', title: '开始', description: '', category: '流程', configSchema: { type: 'object' }, inputs: [], outputs: [] },
  { type: 'end', version: '1', title: '结束', description: '', category: '流程', configSchema: { type: 'object' }, inputs: [], outputs: [] },
  { type: 'template', version: '1', title: '提示词模板', description: '', category: '文本', configSchema: { type: 'object', properties: { template: { type: 'string', title: '模板', 'x-ui-widget': 'textarea' } }, required: ['template'] }, inputs: [], outputs: [{ key: 'text', title: '文本', type: 'string' as const, required: false, cardinality: 'one' as const }] },
  {
    type: 'extension.retriever', version: '1.0.0', title: 'Retriever', description: '使用本地 Jaccard 相似度检索配置文档', category: '扩展',
    configSchema: {
      type: 'object', required: ['documents', 'topK'],
      properties: {
        documents: {
          type: 'array', title: '文档', minItems: 1, maxItems: 1000,
          items: {
            type: 'object', required: ['id', 'text'],
            properties: {
              id: { type: 'string', title: '文档标识', minLength: 1, maxLength: 128 },
              text: { type: 'string', title: '文档内容', minLength: 1, maxLength: 65536, 'x-ui-widget': 'textarea' },
            },
          },
        },
        topK: { type: 'integer', title: '返回数量', minimum: 1, maximum: 100, default: 3 },
      },
    },
    inputs: [{ key: 'query', title: '查询', type: 'string' as const, required: true, cardinality: 'one' as const }],
    outputs: [{ key: 'matches', title: '匹配结果', type: 'json' as const, required: false, cardinality: 'one' as const }],
  },
  {
    type: 'extension.webhook', version: '1.0.0', title: 'Webhook', description: '向运维配置的基地址发送受约束的 JSON POST 请求', category: '扩展',
    configSchema: {
      type: 'object', required: ['path'],
      properties: {
        path: { type: 'string', title: '相对路径', minLength: 1 },
        timeoutMs: { type: 'integer', title: '超时毫秒', minimum: 1, maximum: 30000, default: 5000 },
      },
    },
    inputs: [{ key: 'body', title: '请求体', type: 'json' as const, required: true, cardinality: 'one' as const }],
    outputs: [
      { key: 'status', title: '状态码', type: 'number' as const, required: false, cardinality: 'one' as const },
      { key: 'body', title: '响应体', type: 'json' as const, required: false, cardinality: 'one' as const },
    ],
    capabilities: ['network' as const, 'secrets' as const],
  },
]

const definitions = rawDefinitions.map((definition) => ({
  ...definition,
  capabilities: 'capabilities' in definition ? (definition.capabilities ?? []) : [],
  package: definition.type.startsWith('extension.')
    ? { name: 'github.com/yyl1212/agent-studio', displayName: 'Agent Studio 官方扩展节点', license: 'Apache-2.0', repository: 'https://github.com/yyl1212/agent-studio', source: 'development' as const }
    : { name: 'agent-studio.dev/core', displayName: 'Agent Studio Core', version: 'v0.3.0', license: 'Apache-2.0', repository: 'https://github.com/yyl1212/agent-studio', source: 'builtin' as const },
})) satisfies NodeDefinition[]

describe('StudioPage', () => {
  afterEach(() => vi.restoreAllMocks())

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
    await userEvent.click(screen.getByRole('button', { name: /^提示词模板/ }))
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
    await userEvent.click(screen.getByRole('button', { name: /^提示词模板/ }))
    await userEvent.click(screen.getByRole('button', { name: '发布' }))
    await userEvent.click(screen.getByRole('button', { name: '确认发布' }))
    await vi.waitFor(() => expect(api.saveWorkflow).toHaveBeenCalled())
    expect(api.validateWorkflow).not.toHaveBeenCalled()
    resolveSave({ ...workflow, draftRevision: 2 })
    await vi.waitFor(() => expect(api.publishWorkflow).toHaveBeenCalledWith('w1', 2))
  })

  it('通过通用配置表单保存 Retriever 和 Webhook', async () => {
    render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
    await screen.findByText('演示助手')

    await userEvent.click(screen.getByRole('button', { name: '添加节点' }))
    await userEvent.click(screen.getByRole('button', { name: /^Retriever/ }))
    await userEvent.click(screen.getByRole('button', { name: '添加一项' }))
    await userEvent.type(screen.getByLabelText('文档标识'), 'doc-1')
    await userEvent.type(screen.getByLabelText('文档内容'), 'Agent Studio')
    await userEvent.clear(screen.getByLabelText('返回数量'))
    await userEvent.type(screen.getByLabelText('返回数量'), '1')
    await userEvent.click(screen.getByRole('button', { name: '关闭节点配置' }))

    await userEvent.click(screen.getByRole('button', { name: '添加节点' }))
    await userEvent.click(screen.getByRole('button', { name: /^Webhook/ }))
    await userEvent.type(screen.getByLabelText('相对路径'), 'hooks/run')
    await userEvent.clear(screen.getByLabelText('超时毫秒'))
    await userEvent.type(screen.getByLabelText('超时毫秒'), '2500')
    await userEvent.click(screen.getByRole('button', { name: '关闭节点配置' }))

    let request: Parameters<typeof api.saveWorkflow>[1] | undefined
    await vi.waitFor(() => {
      request = vi.mocked(api.saveWorkflow).mock.calls.at(-1)?.[1]
      expect(request?.graph.nodes.some((node) => node.type === 'extension.retriever')).toBe(true)
      expect(request?.graph.nodes.some((node) => node.type === 'extension.webhook')).toBe(true)
    }, { timeout: 2000 })
    const retriever = request?.graph.nodes.find((node) => node.type === 'extension.retriever')
    const webhook = request?.graph.nodes.find((node) => node.type === 'extension.webhook')
    expect(retriever?.config).toEqual({ documents: [{ id: 'doc-1', text: 'Agent Studio' }], topK: 1 })
    expect(webhook?.config).toEqual({ path: 'hooks/run', timeoutMs: 2500 })
  })

  it('重新加载后恢复官方节点配置', async () => {
    const persistedWorkflow = {
      ...workflow,
      draftGraph: {
        ...workflow.draftGraph,
        nodes: [
          workflow.draftGraph.nodes[0],
          { id: 'extension.retriever-1', type: 'extension.retriever', typeVersion: '1.0.0', position: { x: 300, y: 100 }, config: { documents: [{ id: 'doc-1', text: 'Agent Studio' }], topK: 1 } },
          { id: 'extension.webhook-1', type: 'extension.webhook', typeVersion: '1.0.0', position: { x: 500, y: 100 }, config: { path: 'hooks/run', timeoutMs: 2500 } },
          workflow.draftGraph.nodes[1],
        ],
      },
    }
    vi.mocked(api.getWorkflow).mockResolvedValue(persistedWorkflow)
    render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)

    fireEvent.click(await screen.findByTestId('node-extension.retriever'))
    expect(screen.getByLabelText('文档标识')).toHaveValue('doc-1')
    expect(screen.getByLabelText('文档内容')).toHaveValue('Agent Studio')
    expect(screen.getByLabelText('返回数量')).toHaveValue(1)

    fireEvent.click(screen.getByTestId('node-extension.webhook'))
    expect(screen.getByLabelText('相对路径')).toHaveValue('hooks/run')
    expect(screen.getByLabelText('超时毫秒')).toHaveValue(2500)
  })

  it('导出前等待保存并使用最新 revision', async () => {
    let resolveSave!: (value: typeof workflow) => void
    vi.mocked(api.saveWorkflow).mockReturnValueOnce(new Promise((resolve) => { resolveSave = resolve }))
    vi.spyOn(api, 'exportWorkflowTemplate').mockResolvedValue(new Blob(['template']))
    const createObjectURL = installURLMethod('createObjectURL', vi.fn().mockReturnValue('blob:template'))
    const revokeObjectURL = installURLMethod('revokeObjectURL', vi.fn())
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined)
    render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
    await screen.findByText('演示助手')
    await userEvent.click(screen.getByRole('button', { name: '添加节点' }))
    await userEvent.click(screen.getByRole('button', { name: /^提示词模板/ }))
    await userEvent.click(screen.getByRole('button', { name: '导出模板' }))
    await vi.waitFor(() => expect(api.saveWorkflow).toHaveBeenCalled())
    expect(api.exportWorkflowTemplate).not.toHaveBeenCalled()
    resolveSave({ ...workflow, draftRevision: 2 })
    await vi.waitFor(() => expect(api.exportWorkflowTemplate).toHaveBeenCalledWith('w1', 2, expect.any(AbortSignal)))
    expect(createObjectURL).toHaveBeenCalled()
    expect(click).toHaveBeenCalled()
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:template')
  })

  it.each([
    ['revision 冲突', new APIError(409, 'WORKFLOW_REVISION_CONFLICT', '草稿版本已变化，请刷新后重试'), '草稿版本已变化，请刷新后重试'],
    ['网络失败', new TypeError('network failed'), '操作失败，请稍后重试'],
  ])('导出%s时显示错误且不创建下载', async (_name, failure, message) => {
    vi.spyOn(api, 'exportWorkflowTemplate').mockRejectedValue(failure)
    const createObjectURL = installURLMethod('createObjectURL', vi.fn())
    installURLMethod('revokeObjectURL', vi.fn())
    render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
    await screen.findByText('演示助手')
    await userEvent.click(screen.getByRole('button', { name: '导出模板' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(message)
    expect(createObjectURL).not.toHaveBeenCalled()
  })
})

function installURLMethod(name: 'createObjectURL' | 'revokeObjectURL', implementation: ReturnType<typeof vi.fn>) {
  Object.defineProperty(URL, name, { configurable: true, writable: true, value: implementation })
  return implementation
}
