import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { APIError, api, type NodeDefinition } from '../../lib/api/client'
import { markInvalidEdges, StudioPage } from './StudioPage'
import type { StudioEdge, StudioNode } from './types'

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
    type: 'llm', version: '2', title: 'LLM · 结构化输出', description: '调用已配置的模型服务生成文本或严格结构化结果', category: 'AI',
    configSchema: {
      type: 'object', additionalProperties: false,
      properties: {
        model: { type: 'string', title: '模型' },
        systemPrompt: { type: 'string', title: '系统提示词', 'x-ui-widget': 'textarea' },
        temperature: { type: 'number', minimum: 0, maximum: 2, default: 0.7, title: '温度' },
        maxTokens: { type: 'integer', minimum: 1, maximum: 32768, default: 1024, title: '最大 Token' },
        outputMode: { type: 'string', enum: ['text', 'structured'], default: 'text', title: '输出模式' },
        fields: {
          type: 'array', title: '输出字段', default: [], maxItems: 32,
          items: {
            type: 'object', additionalProperties: false, required: ['key', 'label', 'type'],
            properties: {
              key: { type: 'string', title: '字段 Key', minLength: 1, maxLength: 64, pattern: '^[A-Za-z][A-Za-z0-9_]{0,63}$' },
              label: { type: 'string', title: '字段名称', minLength: 1, maxLength: 80 },
              description: { type: 'string', title: '字段说明', maxLength: 500, 'x-ui-widget': 'textarea' },
              type: { type: 'string', title: '字段类型', enum: ['string', 'number', 'integer', 'boolean', 'string_array'] },
              required: { type: 'boolean', title: '必填', default: true },
            },
          },
        },
      },
    },
    inputs: [{ key: 'prompt', title: '提示词', type: 'string' as const, required: true, cardinality: 'one' as const }],
    outputs: [
      { key: 'text', title: '文本', type: 'string' as const, required: false, cardinality: 'one' as const },
      { key: 'usage', title: '用量', type: 'json' as const, required: false, cardinality: 'one' as const },
    ],
    capabilities: ['network' as const, 'secrets' as const],
  },
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
  executionSafety: definition.type === 'extension.webhook' ? 'side_effect' as const : 'pure' as const,
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
    expect(screen.getByRole('dialog', { name: '提示词模板' })).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('模板'), { target: { value: '回答：{{topic}}' } })
    await vi.waitFor(() => expect(api.resolveNodeType).toHaveBeenCalledWith('template', '1', expect.objectContaining({ template: '回答：{{topic}}' }), expect.any(AbortSignal)))
  })

  it('配置输入只更新草稿，端口就绪并显式应用后才保存一次', async () => {
    render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
    await screen.findByText('演示助手')
    await userEvent.click(screen.getByRole('button', { name: '添加节点' }))
    await userEvent.click(screen.getByRole('button', { name: /^提示词模板/ }))
    await vi.waitFor(() => expect(api.saveWorkflow).toHaveBeenCalled(), { timeout: 2000 })
    await new Promise((resolve) => setTimeout(resolve, 900))
    vi.mocked(api.saveWorkflow).mockClear()
    fireEvent.change(screen.getByLabelText('模板'), { target: { value: '回答：{{topic}}' } })
    await vi.waitFor(() => expect(api.resolveNodeType).toHaveBeenCalledWith('template', '1', expect.objectContaining({ template: '回答：{{topic}}' }), expect.any(AbortSignal)))
    await new Promise((resolve) => setTimeout(resolve, 900))
    expect(api.saveWorkflow).not.toHaveBeenCalled()
    await userEvent.click(screen.getByRole('button', { name: '应用配置' }))
    await vi.waitFor(() => expect(api.saveWorkflow).toHaveBeenCalledTimes(1), { timeout: 2000 })
    expect(vi.mocked(api.saveWorkflow).mock.calls[0][1].graph.nodes.find((node) => node.type === 'template')?.config).toEqual({ template: '回答：{{topic}}' })
  })

  it('脏配置进入测试前要求确认并在应用后运行最新草稿', async () => {
    vi.spyOn(api, 'runDraft').mockResolvedValue(new Response('{"type":"run.completed","sequence":1,"output":{}}\n', { headers: { 'content-type': 'application/x-ndjson' } }))
    render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
    await screen.findByText('演示助手')
    await userEvent.click(screen.getByRole('button', { name: '添加节点' }))
    await userEvent.click(screen.getByRole('button', { name: /^提示词模板/ }))
    fireEvent.change(screen.getByLabelText('模板'), { target: { value: '回答：{{topic}}' } })
    await vi.waitFor(() => expect(screen.getByRole('button', { name: '应用配置' })).toBeEnabled())

    await userEvent.click(screen.getByRole('button', { name: '测试运行' }))
    expect(screen.getByRole('dialog', { name: '保存节点配置更改？' })).toBeInTheDocument()
    expect(screen.queryByRole('dialog', { name: '测试运行' })).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '应用并继续' }))
    expect(await screen.findByRole('dialog', { name: '测试运行' })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '运行' }))
    await vi.waitFor(() => expect(api.runDraft).toHaveBeenCalledWith('w1', { draftRevision: 2, input: {} }, expect.any(AbortSignal)), { timeout: 2500 })
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
    await vi.waitFor(() => expect(screen.getByRole('button', { name: '应用配置' })).toBeEnabled())
    await userEvent.click(screen.getByRole('button', { name: '应用配置' }))
    await userEvent.click(screen.getByRole('button', { name: '关闭工作台' }))

    await userEvent.click(screen.getByRole('button', { name: '添加节点' }))
    await userEvent.click(screen.getByRole('button', { name: /^Webhook/ }))
    await userEvent.type(screen.getByLabelText('相对路径'), 'hooks/run')
    await userEvent.click(screen.getByText('可选配置'))
    await userEvent.clear(screen.getByLabelText('超时毫秒'))
    await userEvent.type(screen.getByLabelText('超时毫秒'), '2500')
    await vi.waitFor(() => expect(screen.getByRole('button', { name: '应用配置' })).toBeEnabled())
    await userEvent.click(screen.getByRole('button', { name: '应用配置' }))
    await userEvent.click(screen.getByRole('button', { name: '关闭工作台' }))

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

  it('通过通用配置表单保存 LLM v2 结构化字段并解析动态端口', async () => {
    vi.mocked(api.resolveNodeType).mockImplementation(async (type, version, config) => {
      if (type === 'llm' && version === '2' && config.outputMode === 'structured') {
        return {
          inputs: [{ key: 'prompt', title: '提示词', type: 'string', required: true, cardinality: 'one' }],
          outputs: [
            { key: 'json', title: '结构化结果', type: 'json', required: false, cardinality: 'one' },
            ...((config.fields as Array<{ key: string; label: string; required: boolean }> | undefined) ?? []).map((field) => ({
              key: field.key, title: field.label, type: field.required ? 'string' as const : 'any' as const, required: false, cardinality: 'one' as const,
            })),
            { key: 'usage', title: '用量', type: 'json', required: false, cardinality: 'one' },
          ],
        }
      }
      return { inputs: [], outputs: [] }
    })
    render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
    await screen.findByText('演示助手')

    await userEvent.click(screen.getByRole('button', { name: '添加节点' }))
    await userEvent.click(screen.getByRole('button', { name: /^LLM · 结构化输出/ }))
    await userEvent.click(screen.getByText('可选配置'))
    await userEvent.selectOptions(screen.getByLabelText('输出模式'), 'structured')
    await userEvent.click(screen.getByRole('button', { name: '添加一项' }))
    await userEvent.click(screen.getByRole('button', { name: '添加一项' }))

    const keys = screen.getAllByLabelText('字段 Key')
    const labels = screen.getAllByLabelText('字段名称')
    const types = screen.getAllByLabelText('字段类型')
    const required = screen.getAllByRole('checkbox', { name: '必填' })
    await userEvent.type(keys[0], 'answer')
    await userEvent.type(labels[0], '回答')
    await userEvent.selectOptions(types[0], 'string')
    await userEvent.type(keys[1], 'score')
    await userEvent.type(labels[1], '分数')
    await userEvent.selectOptions(types[1], 'number')
    await userEvent.click(required[1])

    await vi.waitFor(() => expect(screen.getByRole('button', { name: '应用配置' })).toBeEnabled())
    await userEvent.click(screen.getByRole('button', { name: '应用配置' }))

    let request: Parameters<typeof api.saveWorkflow>[1] | undefined
    await vi.waitFor(() => {
      request = vi.mocked(api.saveWorkflow).mock.calls.at(-1)?.[1]
      const llm = request?.graph.nodes.find((node) => node.type === 'llm')
      expect(llm?.typeVersion).toBe('2')
      expect(llm?.config).toEqual({
        model: '',
        systemPrompt: '',
        temperature: 0.7,
        maxTokens: 1024,
        outputMode: 'structured',
        fields: [
          { key: 'answer', label: '回答', description: '', type: 'string', required: true },
          { key: 'score', label: '分数', description: '', type: 'number', required: false },
        ],
      })
    }, { timeout: 3000 })
    expect(api.resolveNodeType).toHaveBeenCalledWith('llm', '2', expect.objectContaining({ outputMode: 'structured' }), expect.any(AbortSignal))
    expect(await screen.findByTitle('结构化结果')).toBeInTheDocument()
    expect(screen.getByTitle('回答')).toBeInTheDocument()
    expect(screen.getByTitle('分数')).toBeInTheDocument()
  })

  it('结构化字段删除后标记旧连线无效，服务端校验失败时阻止发布', async () => {
    const llm = studioNode('llm-2', 'llm', {
      inputs: [],
      outputs: [{ key: 'answer', title: '回答', type: 'string', required: false, cardinality: 'one' }],
    })
    const end = studioNode('end', 'end', {
      inputs: [{ key: 'result', title: '结果', type: 'any', required: false, cardinality: 'one' }],
      outputs: [],
    })
    const edge: StudioEdge = { id: 'answer-edge', source: llm.id, sourceHandle: 'answer', target: end.id, targetHandle: 'result' }
    expect(markInvalidEdges([llm, end], [edge])[0].data?.invalid).toBe(false)
    const withoutAnswer = { ...llm, data: { ...llm.data, ports: { inputs: [], outputs: [] } } }
    const invalidEdge = markInvalidEdges([withoutAnswer, end], [edge])[0]
    expect(invalidEdge.data?.invalid).toBe(true)
    expect(invalidEdge.style).toEqual({ stroke: '#d92d20', strokeDasharray: '5 4' })

    vi.spyOn(api, 'validateWorkflow').mockResolvedValue({
      valid: false,
      issues: [{ code: 'EDGE_SOURCE_PORT_NOT_FOUND', message: '输出端口 answer 不存在', path: '/edges/0', nodeId: 'llm-2' }],
    })
    vi.spyOn(api, 'publishWorkflow').mockRejectedValue(new Error('不得调用'))
    render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
    await screen.findByText('演示助手')
    await userEvent.click(screen.getByRole('button', { name: '发布' }))
    await userEvent.click(screen.getByRole('button', { name: '确认发布' }))
    expect(await screen.findByText('输出端口 answer 不存在')).toBeInTheDocument()
    expect(api.publishWorkflow).not.toHaveBeenCalled()
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
    await userEvent.click(screen.getByText('可选配置'))
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

function studioNode(id: string, nodeType: string, ports: StudioNode['data']['ports']): StudioNode {
  return {
    id, type: 'studio', position: { x: 0, y: 0 },
    data: { nodeType, typeVersion: '2', config: {}, ports, issues: [] },
  }
}
