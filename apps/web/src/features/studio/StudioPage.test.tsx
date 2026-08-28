import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { StrictMode } from 'react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { APIError, api, type NodeDefinition, type Workflow } from '../../lib/api/client'
import type { RunEvent } from '../../lib/api/ndjson'
import { activeRunNodeID, connectionIssue, decorateRunNodes, graphAfterDelete, isPersistentEdgeChange, isPersistentNodeChange, markInvalidEdges, StudioPage } from './StudioPage'
import type { StudioEdge, StudioNode } from './types'

vi.mock('../../lib/api/client', async (importOriginal) => {
  const original = await importOriginal<typeof import('../../lib/api/client')>()
  return { ...original, api: { ...original.api } }
})

const workflow = {
  id: 'w1', name: '演示助手', slug: 'demo', description: '', draftRevision: 1,
  agentPresentation: { title: '演示助手', description: '', accent: 'indigo' as const, submitLabel: '运行 Agent', resultMode: 'auto' as const },
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

const workflowWithoutStart = {
  ...workflow,
  draftGraph: {
    ...workflow.draftGraph,
    nodes: workflow.draftGraph.nodes.filter((node) => node.type !== 'start'),
  },
}

const workflowWithDuplicateEnd = {
  ...workflow,
  draftGraph: {
    ...workflow.draftGraph,
    nodes: [
      ...workflow.draftGraph.nodes,
      {
        ...workflow.draftGraph.nodes.find((node) => node.type === 'end')!,
        id: 'end-duplicate',
      },
    ],
  },
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

const renderStudio = () =>
  render(
    <MemoryRouter initialEntries={['/workflows/w1']}>
      <Routes>
        <Route path="/workflows/:id" element={<StudioPage />} />
      </Routes>
    </MemoryRouter>,
  )

function confirmPendingPlacement() {
  const pane = document.querySelector<HTMLElement>('.react-flow__pane')
  if (!pane) throw new Error('找不到画布放置区域')
  fireEvent.click(pane)
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
    window.localStorage.clear()
    vi.spyOn(api, 'getWorkflow').mockResolvedValue(workflow)
    vi.spyOn(api, 'listNodeTypes').mockResolvedValue(definitions)
    vi.spyOn(api, 'resolveNodeType').mockResolvedValue({ inputs: [], outputs: [] })
    vi.spyOn(api, 'saveWorkflow').mockResolvedValue({ ...workflow, draftRevision: 2 })
  })

  it('使用全画布外壳突出试运行和发布，并收纳低频操作', async () => {
    const { container } = render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
    expect(await screen.findByText('演示助手')).toBeInTheDocument()
    expect(container.querySelector('.studio-shell')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '测试运行' })).toBeVisible()
    expect(screen.getByRole('button', { name: '发布' })).toBeVisible()
    await userEvent.click(screen.getByText('更多操作'))
    expect(screen.getByRole('link', { name: '运行记录' })).toHaveAttribute('href', '/runs?workflowId=w1')
  })

  it('节点库与两个 disclosure 互斥，并由 Escape 关闭视觉最上层', async () => {
    render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
    await screen.findByText('演示助手')
    const moreSummary = screen.getByText('更多操作')
    await userEvent.click(moreSummary)
    expect(moreSummary.closest('details')).toHaveAttribute('open')

    await userEvent.keyboard('{Control>}k{/Control}')
    expect(await screen.findByRole('dialog', { name: '节点库' })).toBeInTheDocument()
    expect(moreSummary.closest('details')).not.toHaveAttribute('open')
    await userEvent.keyboard('{Escape}')
    expect(screen.queryByRole('dialog', { name: '节点库' })).not.toBeInTheDocument()

    const helpSummary = screen.getByText('快捷键帮助')
    await userEvent.click(helpSummary)
    expect(helpSummary.closest('details')).toHaveAttribute('open')
    await userEvent.keyboard('{Escape}')
    expect(helpSummary.closest('details')).not.toHaveAttribute('open')
    expect(helpSummary).toHaveFocus()
  })

  it('工作台上方的菜单由 Escape 关闭并把焦点恢复到可见摘要', async () => {
    render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
    await screen.findByText('演示助手')
    await userEvent.click(screen.getByRole('button', { name: '测试运行' }))
    expect(screen.getByRole('dialog', { name: '测试运行' })).toBeInTheDocument()

    const moreSummary = screen.getByText('更多操作')
    await userEvent.click(moreSummary)
    screen.getByRole('button', { name: 'Agent 页面设置' }).focus()
    await userEvent.keyboard('{Escape}')

    expect(moreSummary.closest('details')).not.toHaveAttribute('open')
    expect(screen.getByRole('dialog', { name: '测试运行' })).toBeInTheDocument()
    await vi.waitFor(() => expect(moreSummary).toHaveFocus())
  })

  it('主操作和节点操作会关闭 disclosure，快捷键节点库恢复到稳定摘要', async () => {
    render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
    await screen.findByText('演示助手')
    const moreSummary = screen.getByText('更多操作')

    await userEvent.click(moreSummary)
    await userEvent.click(screen.getByRole('button', { name: '测试运行' }))
    expect(moreSummary.closest('details')).not.toHaveAttribute('open')

    await userEvent.click(moreSummary)
    screen.getByRole('button', { name: 'Agent 页面设置' }).focus()
    await userEvent.keyboard('{Control>}k{/Control}')
    expect(await screen.findByRole('dialog', { name: '节点库' })).toBeInTheDocument()
    await userEvent.keyboard('{Escape}')
    expect(moreSummary).toHaveFocus()

    const helpSummary = screen.getByText('快捷键帮助')
    await userEvent.click(helpSummary)
    fireEvent.click(screen.getByTestId('node-start'))
    expect(helpSummary.closest('details')).not.toHaveAttribute('open')
    expect(screen.getByRole('dialog', { name: '开始' })).toBeInTheDocument()
  })

  it('应用并试运行的直接切换路径也会关闭 disclosure', async () => {
    render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
    await screen.findByText('演示助手')
    await userEvent.click(screen.getByRole('button', { name: '添加节点' }))
    await userEvent.click(screen.getByRole('button', { name: /^提示词模板/ }))
    confirmPendingPlacement()
    fireEvent.change(screen.getByLabelText('模板'), { target: { value: '回答：{{topic}}' } })
    await vi.waitFor(() => expect(screen.getByRole('button', { name: '应用并试运行' })).toBeEnabled())

    const moreSummary = screen.getByText('更多操作')
    await userEvent.click(moreSummary)
    await userEvent.click(screen.getByRole('button', { name: '应用并试运行' }))

    await vi.waitFor(() => expect(screen.getByRole('dialog', { name: '测试运行' })).toBeInTheDocument())
    expect(moreSummary.closest('details')).not.toHaveAttribute('open')
  })

  it('只把图结构变化写入草稿，不持久化 React Flow 尺寸和选中态', () => {
	 expect(isPersistentNodeChange({ type: 'dimensions', id: 'a', dimensions: { width: 100, height: 60 } })).toBe(false)
	 expect(isPersistentNodeChange({ type: 'select', id: 'a', selected: true })).toBe(false)
	 expect(isPersistentNodeChange({ type: 'position', id: 'a', position: { x: 8, y: 9 }, dragging: true })).toBe(false)
	 expect(isPersistentNodeChange({ type: 'position', id: 'a', position: { x: 10, y: 20 }, dragging: false })).toBe(true)
	 expect(isPersistentEdgeChange({ type: 'select', id: 'edge', selected: true })).toBe(false)
	 expect(isPersistentEdgeChange({ type: 'remove', id: 'edge' })).toBe(true)
  })

  it('说明无效连线的具体原因', () => {
    const source = studioNode('source', 'template', { inputs: [], outputs: [{ key: 'text', title: '文本', type: 'string', required: false, cardinality: 'one' }] })
    const target = studioNode('target', 'end', { inputs: [{ key: 'value', title: '数值', type: 'number', required: false, cardinality: 'one' }], outputs: [] })
    const mismatch = { source: 'source', sourceHandle: 'text', target: 'target', targetHandle: 'value' }
    expect(connectionIssue(mismatch, [source, target], [])).toBe('端口类型不兼容：string 不能连接到 number')
    const anyTarget = { ...target, data: { ...target.data, ports: { inputs: [{ ...target.data.ports.inputs[0], type: 'any' as const }], outputs: [] } } }
    const occupied: StudioEdge = { id: 'edge-1', ...mismatch, targetHandle: 'value' }
    expect(connectionIssue(mismatch, [source, anyTarget], [occupied])).toBe('目标端口只允许一条输入连线')
    expect(connectionIssue(mismatch, [source, anyTarget], [])).toBeUndefined()
  })

  it('删除已连线节点时生成不含悬空边的原子保存快照', () => {
    const source = studioNode('source', 'template', { inputs: [], outputs: [{ key: 'text', title: '文本', type: 'string', required: false, cardinality: 'one' }] })
    const target = studioNode('target', 'end', { inputs: [{ key: 'result', title: '结果', type: 'any', required: false, cardinality: 'one' }], outputs: [] })
    const edge: StudioEdge = { id: 'edge-1', source: 'source', sourceHandle: 'text', target: 'target', targetHandle: 'result' }

    expect(graphAfterDelete([source, target], [edge], [source], [])).toEqual({ nodes: [target], edges: [] })
  })

  it('从页面删除已连线节点时只保存一次完整图快照', async () => {
    vi.mocked(api.getWorkflow).mockResolvedValue({
      ...workflow,
      draftGraph: {
        ...workflow.draftGraph,
        nodes: [
          workflow.draftGraph.nodes[0],
          {
            id: 'template',
            type: 'template',
            typeVersion: '1',
            position: { x: 300, y: 100 },
            config: { template: '' },
          },
          workflow.draftGraph.nodes[1],
        ],
        edges: [
          { id: 'start-template', source: 'start', sourcePort: 'value', target: 'template', targetPort: 'input' },
          { id: 'template-end', source: 'template', sourcePort: 'text', target: 'end', targetPort: 'result' },
        ],
      },
    })
    renderStudio()
    await screen.findByText('演示助手')
    const flowNode = screen.getByTestId('node-template').closest<HTMLElement>('.react-flow__node')
    if (!flowNode) throw new Error('找不到模板节点容器')

    fireEvent.click(flowNode)
    fireEvent.keyDown(document, { key: 'Delete', code: 'Delete' })

    await vi.waitFor(() => expect(api.saveWorkflow).toHaveBeenCalledOnce(), { timeout: 2000 })
    expect(vi.mocked(api.saveWorkflow).mock.calls[0][1].graph).toEqual(expect.objectContaining({
      nodes: [
        expect.objectContaining({ id: 'start' }),
        expect.objectContaining({ id: 'end' }),
      ],
      edges: [],
    }))
  })

  it('删除开始节点不会改图或保存', async () => {
    renderStudio()
    const start = await screen.findByTestId('node-start')
    fireEvent.click(start.closest('.react-flow__node')!)
    fireEvent.keyDown(document, { key: 'Delete', code: 'Delete' })

    await vi.waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(
        '开始和结束节点不可删除',
      ),
    )
    expect(api.saveWorkflow).not.toHaveBeenCalled()
    expect(screen.getByTestId('node-start')).toBeInTheDocument()
  })

  it('复制或剪切边界节点不会创建副本或进入保存队列', async () => {
    renderStudio()
    const start = await screen.findByTestId('node-start')
    fireEvent.click(start.closest('.react-flow__node')!)
    await userEvent.keyboard('{Control>}c{/Control}{Control>}x{/Control}')

    expect(screen.getAllByTestId('node-start')).toHaveLength(1)
    expect(api.saveWorkflow).not.toHaveBeenCalled()
  })

  it('边界修复保存成功后才替换页面图', async () => {
    const pending = deferred<Workflow>()
    vi.mocked(api.getWorkflow).mockResolvedValue(workflowWithoutStart)
    vi.mocked(api.saveWorkflow).mockReturnValue(pending.promise)
    renderStudio()

    await userEvent.click(
      await screen.findByRole('button', { name: '修复工作流边界' }),
    )
    await userEvent.click(screen.getByRole('button', { name: '确认修复' }))
    expect(screen.queryByTestId('node-start')).not.toBeInTheDocument()

    pending.resolve({ ...workflow, draftRevision: 2 })
    expect(await screen.findByTestId('node-start')).toBeInTheDocument()
  })

  it.each([
    ['缺少开始节点', workflowWithoutStart],
    ['重复结束节点', workflowWithDuplicateEnd],
  ] as const)('%s 时阻塞普通写操作', async (_name, loaded) => {
    vi.mocked(api.getWorkflow).mockResolvedValue(loaded)
    renderStudio()

    await screen.findByRole('button', { name: '修复工作流边界' })
    expect(screen.getByRole('button', { name: '添加节点' })).toBeDisabled()
    expect(screen.getByRole('button', { name: '测试运行' })).toBeDisabled()
    expect(screen.getByRole('button', { name: '发布' })).toBeDisabled()
  })

  it.each([
    [new APIError(503, 'TEMPORARY_UNAVAILABLE', '保存失败'), '保存失败'],
    [new APIError(409, 'REVISION_CONFLICT', '草稿冲突'), '草稿冲突'],
  ] as const)('修复保存失败或冲突都保留原图和修复对话框', async (failure, message) => {
    vi.mocked(api.getWorkflow).mockResolvedValue(workflowWithoutStart)
    vi.mocked(api.saveWorkflow).mockRejectedValueOnce(failure)
    renderStudio()

    await userEvent.click(
      await screen.findByRole('button', { name: '修复工作流边界' }),
    )
    await userEvent.click(screen.getByRole('button', { name: '确认修复' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(message)
    expect(screen.queryByTestId('node-start')).not.toBeInTheDocument()
    expect(
      screen.getByRole('dialog', { name: '修复工作流边界' }),
    ).toBeVisible()
    expect(
      screen.queryByRole('button', { name: '重试保存' }),
    ).not.toBeInTheDocument()
    if (failure.status === 409) {
      expect(
        screen.getByRole('button', { name: '刷新工作流' }),
      ).toBeVisible()
    }
  })

  it('只有开始和结束节点时显示首节点引导并打开节点库', async () => {
    renderStudio()
    await screen.findByText('演示助手')

    await userEvent.click(
      screen.getByRole('button', { name: '在这里添加第一个节点' }),
    )
    expect(screen.getByRole('dialog', { name: '节点库' })).toBeVisible()
    expect(api.saveWorkflow).not.toHaveBeenCalled()
  })

  it('已有普通节点时不显示首节点引导', async () => {
    vi.mocked(api.getWorkflow).mockResolvedValue({
      ...workflow,
      draftGraph: {
        ...workflow.draftGraph,
        nodes: [
          ...workflow.draftGraph.nodes,
          {
            id: 'template',
            type: 'template',
            typeVersion: '1',
            position: { x: 300, y: 100 },
            config: { template: '' },
          },
        ],
      },
    })
    renderStudio()
    await screen.findByTestId('node-template')

    expect(
      screen.queryByRole('button', { name: '在这里添加第一个节点' }),
    ).not.toBeInTheDocument()
  })

  it('只高亮尚未结束的最新运行节点并保留中文状态所需数据', () => {
    expect(activeRunNodeID([runEvent(1, 'node.started', 'a')])).toBe('a')
    expect(activeRunNodeID([runEvent(1, 'node.started', 'a'), runEvent(2, 'node.completed', 'a')])).toBeUndefined()
    expect(activeRunNodeID([runEvent(1, 'node.started', 'a'), runEvent(2, 'run.failed')])).toBeUndefined()
    const nodes = [studioNode('a', 'template', { inputs: [], outputs: [] }), studioNode('b', 'end', { inputs: [], outputs: [] })]
    const decorated = decorateRunNodes(nodes, [runEvent(1, 'node.completed', 'a'), runEvent(2, 'node.started', 'b')])
    expect(decorated[0].data.debugStatus).toBe('completed')
    expect(decorated[1].data).toEqual(expect.objectContaining({ debugStatus: 'running', debugCurrent: true }))
  })

  it('打开节点库、添加节点并在右侧配置', async () => {
    render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
    expect(await screen.findByText('演示助手')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '添加节点' }))
    await userEvent.click(screen.getByRole('button', { name: /^提示词模板/ }))
    confirmPendingPlacement()
    expect(screen.getByRole('dialog', { name: '提示词模板' })).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('模板'), { target: { value: '回答：{{topic}}' } })
    await vi.waitFor(() => expect(api.resolveNodeType).toHaveBeenCalledWith('template', '1', expect.objectContaining({ template: '回答：{{topic}}' }), expect.any(AbortSignal)))
  })

  it('把新节点放到当前画布视口中心', async () => {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
      if (this.classList.contains('workflow-canvas')) {
        return { x: 0, y: 0, width: 1280, height: 720, top: 0, right: 1280, bottom: 720, left: 0, toJSON: () => ({}) }
      }
      return { x: 0, y: 0, width: 0, height: 0, top: 0, right: 0, bottom: 0, left: 0, toJSON: () => ({}) }
    })
    render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
    await screen.findByText('演示助手')
    await userEvent.click(screen.getByRole('button', { name: '添加节点' }))
    await userEvent.click(screen.getByRole('button', { name: /^提示词模板/ }))
    confirmPendingPlacement()

    await vi.waitFor(() => {
      const saved = vi.mocked(api.saveWorkflow).mock.calls.at(-1)?.[1]
      expect(saved?.graph.nodes.find((node) => node.type === 'template')?.position).toEqual({ x: 640, y: 360 })
    }, { timeout: 2000 })
  })

  it('点击添加只保存一次、立即配置并记录最近使用', async () => {
    window.localStorage.setItem('agent-studio.node-library.recent.v1', JSON.stringify(['removed@1']))
    const setItem = vi.spyOn(Storage.prototype, 'setItem')
    render(<StrictMode><MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter></StrictMode>)
    await screen.findByText('演示助手')
    await userEvent.click(screen.getByRole('button', { name: '添加节点' }))
    await userEvent.click(screen.getByRole('button', { name: /^提示词模板/ }))
    confirmPendingPlacement()

    expect(screen.getByRole('dialog', { name: '提示词模板' })).toBeInTheDocument()
    await vi.waitFor(() => expect(api.saveWorkflow).toHaveBeenCalledOnce(), { timeout: 2000 })
    expect(setItem).toHaveBeenCalledOnce()
    expect(JSON.parse(window.localStorage.getItem('agent-studio.node-library.recent.v1') ?? '[]')).toEqual(['template@1'])

    await userEvent.click(screen.getByRole('button', { name: '关闭工作台' }))
    await userEvent.click(screen.getByRole('button', { name: '添加节点' }))
    expect(screen.getByRole('heading', { name: '最近使用' })).toBeInTheDocument()
    expect(screen.getAllByRole('button', { name: /^提示词模板/ })).toHaveLength(1)
  })

  it('关闭节点库后把焦点恢复到添加节点按钮', async () => {
    render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
    await screen.findByText('演示助手')
    const add = screen.getByRole('button', { name: '添加节点' })
    await userEvent.click(add)
    await userEvent.keyboard('{Escape}')
    await vi.waitFor(() => expect(add).toHaveFocus())
  })

  it('拖放通过统一入口创建一次并立即打开配置', async () => {
    const setItem = vi.spyOn(Storage.prototype, 'setItem')
    render(<StrictMode><MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter></StrictMode>)
    await screen.findByText('演示助手')
    await userEvent.click(screen.getByRole('button', { name: '添加节点' }))
    const transfer = nodeTransfer('template@1')
    const canvas = screen.getByLabelText('工作流画布')
    fireEvent.dragOver(canvas, { dataTransfer: transfer })
    fireEvent.drop(canvas, { dataTransfer: transfer, clientX: 640, clientY: 360 })

    expect(await screen.findByRole('dialog', { name: '提示词模板' })).toBeInTheDocument()
    await vi.waitFor(() => expect(api.saveWorkflow).toHaveBeenCalledOnce(), { timeout: 2000 })
    expect(setItem).toHaveBeenCalledOnce()
    expect(JSON.parse(window.localStorage.getItem('agent-studio.node-library.recent.v1') ?? '[]')).toEqual(['template@1'])
  })

  it('最近记录写入失败时仍创建、保存并打开配置', async () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('storage unavailable')
    })
    render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
    await screen.findByText('演示助手')
    await userEvent.click(screen.getByRole('button', { name: '添加节点' }))
    await userEvent.click(screen.getByRole('button', { name: /^提示词模板/ }))
    confirmPendingPlacement()

    expect(screen.getByRole('dialog', { name: '提示词模板' })).toBeInTheDocument()
    await vi.waitFor(() => expect(api.saveWorkflow).toHaveBeenCalledOnce(), { timeout: 2000 })
  })

  it('拖放失效定义时保持节点库并提示，不创建也不保存', async () => {
    render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
    await screen.findByText('演示助手')
    await userEvent.click(screen.getByRole('button', { name: '添加节点' }))
    const transfer = nodeTransfer('removed@1')
    const canvas = screen.getByLabelText('工作流画布')
    fireEvent.dragOver(canvas, { dataTransfer: transfer })
    fireEvent.drop(canvas, { dataTransfer: transfer, clientX: 640, clientY: 360 })

    expect(screen.getByRole('dialog', { name: '节点库' })).toBeInTheDocument()
    expect(screen.getByRole('alert')).toHaveTextContent('节点定义已更新，请重新选择')
    expect(api.saveWorkflow).not.toHaveBeenCalled()
  })

  it('只开始拖拽但未释放时不创建、不保存也不记录最近使用', async () => {
    render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
    await screen.findByText('演示助手')
    await userEvent.click(screen.getByRole('button', { name: '添加节点' }))
    const transfer = nodeTransfer('template@1')
    const card = screen.getByRole('button', { name: /^提示词模板/ })
    fireEvent.dragStart(card, { dataTransfer: transfer })
    fireEvent.dragEnd(card, { dataTransfer: transfer })
    expect(screen.getByRole('dialog', { name: '节点库' })).toBeInTheDocument()
    expect(api.saveWorkflow).not.toHaveBeenCalled()
    expect(window.localStorage.getItem('agent-studio.node-library.recent.v1')).toBeNull()
  })

  it('归档工作流以只读模式查看且导出不触发保存', async () => {
    vi.mocked(api.getWorkflow).mockResolvedValue({ ...workflow, archivedAt: '2026-08-25T04:00:00Z' })
	vi.spyOn(api, 'listWorkflowVersions').mockResolvedValue({ items: [], nextCursor: null, rollbackCheckpoint: null })
    vi.spyOn(api, 'exportWorkflowTemplate').mockResolvedValue(new Blob(['template']))
    installURLMethod('createObjectURL', vi.fn().mockReturnValue('blob:archived-template'))
    installURLMethod('revokeObjectURL', vi.fn())
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined)
    render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
    expect(await screen.findByRole('status')).toHaveTextContent('已归档，只读模式')
    expect(screen.getByRole('button', { name: '添加节点' })).toBeDisabled()
    expect(screen.getByRole('button', { name: '测试运行' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Agent 页面设置' })).toBeDisabled()
    expect(screen.getByRole('button', { name: '发布' })).toBeDisabled()
	expect(screen.getByRole('button', { name: '版本历史' })).toBeEnabled()
    expect(screen.getByRole('link', { name: '运行记录' })).toHaveAttribute('href', '/runs?workflowId=w1')
    fireEvent.click(await screen.findByTestId('node-start'))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '导出模板' }))
    await vi.waitFor(() => expect(api.exportWorkflowTemplate).toHaveBeenCalledWith('w1', 1, expect.any(AbortSignal)))
    expect(api.saveWorkflow).not.toHaveBeenCalled()
	await userEvent.click(screen.getByRole('button', { name: '版本历史' }))
	expect(await screen.findByRole('heading', { name: '版本历史' })).toBeInTheDocument()
	expect(screen.queryByRole('button', { name: /恢复 v/ })).not.toBeInTheDocument()
  })

  it('保存失败后保留草稿、阻断提交动作并允许原位重试', async () => {
    vi.mocked(api.saveWorkflow)
      .mockRejectedValueOnce(new TypeError('network failed'))
      .mockResolvedValueOnce({ ...workflow, draftRevision: 2 })
    render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
    await screen.findByText('演示助手')
    await userEvent.click(screen.getByRole('button', { name: '添加节点' }))
    await userEvent.click(screen.getByRole('button', { name: /^提示词模板/ }))
    confirmPendingPlacement()
    const retry = await screen.findByRole('button', { name: '重试保存' }, { timeout: 2500 })
    expect(screen.getByRole('button', { name: '测试运行' })).toBeDisabled()
    expect(screen.getByRole('button', { name: '发布' })).toBeDisabled()
    expect(screen.getByTestId('node-template')).toBeInTheDocument()
    await userEvent.click(retry)
    await vi.waitFor(() => expect(api.saveWorkflow).toHaveBeenCalledTimes(2), { timeout: 2500 })
    expect(await screen.findByText('已保存', {}, { timeout: 2500 })).toBeInTheDocument()
    expect(screen.getByTestId('node-template')).toBeInTheDocument()
  })

  it('配置输入只更新草稿，端口就绪并显式应用后才保存一次', async () => {
    render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
    await screen.findByText('演示助手')
    await userEvent.click(screen.getByRole('button', { name: '添加节点' }))
    await userEvent.click(screen.getByRole('button', { name: /^提示词模板/ }))
    confirmPendingPlacement()
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

  it('保存 Agent 页面设置后接纳服务端 revision 并用于发布', async () => {
    const nextPresentation = { ...workflow.agentPresentation, title: '研究助手', accent: 'teal' as const }
    vi.spyOn(api, 'saveAgentPresentation').mockResolvedValue({ ...workflow, draftRevision: 2, agentPresentation: nextPresentation })
    vi.spyOn(api, 'validateWorkflow').mockResolvedValue({ valid: true, issues: [] })
    vi.spyOn(api, 'publishWorkflow').mockResolvedValue({
      id: 'v1', workflowId: 'w1', version: 1, graph: workflow.draftGraph, inputSchema: {}, agentPresentation: nextPresentation, createdAt: '2026-08-17T00:00:00Z',
    })
    render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
    await screen.findByText('演示助手')
    await userEvent.click(screen.getByRole('button', { name: 'Agent 页面设置' }))
    await userEvent.clear(screen.getByLabelText('页面标题'))
    await userEvent.type(screen.getByLabelText('页面标题'), '研究助手')
    await userEvent.selectOptions(screen.getByLabelText('强调色'), 'teal')
    await userEvent.click(screen.getByRole('button', { name: '保存设置' }))
    await vi.waitFor(() => expect(api.saveAgentPresentation).toHaveBeenCalledWith('w1', {
      draftRevision: 1, presentation: nextPresentation,
    }))
    await vi.waitFor(() => expect(screen.queryByRole('dialog', { name: '页面设置' })).not.toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: '发布' }))
    await userEvent.click(screen.getByRole('button', { name: '确认发布' }))
    await vi.waitFor(() => expect(api.publishWorkflow).toHaveBeenCalledWith('w1', 2))
  })

  it('脏节点配置会延迟打开 Agent 页面设置', async () => {
    render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
    await screen.findByText('演示助手')
    await userEvent.click(screen.getByRole('button', { name: '添加节点' }))
    await userEvent.click(screen.getByRole('button', { name: /^提示词模板/ }))
    confirmPendingPlacement()
    fireEvent.change(screen.getByLabelText('模板'), { target: { value: '未应用配置' } })
    await vi.waitFor(() => expect(screen.getByRole('button', { name: '应用配置' })).toBeEnabled())
    await userEvent.click(screen.getByRole('button', { name: 'Agent 页面设置' }))
    expect(screen.getByRole('dialog', { name: '保存节点配置更改？' })).toBeInTheDocument()
    expect(screen.queryByRole('dialog', { name: '页面设置' })).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '放弃更改' }))
    expect(await screen.findByRole('dialog', { name: '页面设置' })).toBeInTheDocument()
  })

  it('打开版本历史前处理脏配置并等待保存队列', async () => {
    let resolveSave!: (value: typeof workflow) => void
    vi.mocked(api.saveWorkflow).mockReturnValueOnce(new Promise((resolve) => { resolveSave = resolve }))
    const listVersions = vi.spyOn(api, 'listWorkflowVersions').mockResolvedValue({ items: [], nextCursor: null, rollbackCheckpoint: null })
    render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
    await screen.findByText('演示助手')
    await userEvent.click(screen.getByRole('button', { name: '添加节点' }))
    await userEvent.click(screen.getByRole('button', { name: /^提示词模板/ }))
    confirmPendingPlacement()
    fireEvent.change(screen.getByLabelText('模板'), { target: { value: '等待保存' } })
    await vi.waitFor(() => expect(screen.getByRole('button', { name: '应用配置' })).toBeEnabled())
    await userEvent.click(screen.getByRole('button', { name: '版本历史' }))
    expect(screen.getByRole('dialog', { name: '保存节点配置更改？' })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '应用并继续' }))
    await vi.waitFor(() => expect(api.saveWorkflow).toHaveBeenCalled())
    expect(listVersions).not.toHaveBeenCalled()
    resolveSave({ ...workflow, draftRevision: 2 })
    expect(await screen.findByRole('heading', { name: '版本历史' })).toBeInTheDocument()
    expect(listVersions).toHaveBeenCalledWith('w1', { limit: 20 }, expect.any(AbortSignal))
  })

  it('回滚期间锁定画布和工作台，并接纳服务端 revision', async () => {
    const publishedWorkflow = { ...workflow, publishedVersion: 1 }
    vi.mocked(api.getWorkflow).mockResolvedValue(publishedWorkflow)
    vi.spyOn(api, 'listWorkflowVersions').mockResolvedValue({ items: [{ id: 'v1', version: 1, current: true, createdAt: '2026-08-27T00:00:00Z' }], nextCursor: null, rollbackCheckpoint: null })
    vi.spyOn(api, 'diffWorkflowVersions').mockResolvedValue({
      base: { kind: 'version', version: 1, versionId: 'v1', createdAt: '2026-08-27T00:00:00Z' }, compare: { kind: 'draft', draftRevision: 1 },
      summary: { total: 1, nodes: 1, startParameters: 0, connections: 0, agentPresentation: 0, layout: 0 }, truncated: false,
      groups: { nodes: [], startParameters: [], connections: [], agentPresentation: [], layout: [] },
    })
    let resolveRollback!: (value: Awaited<ReturnType<typeof api.rollbackWorkflow>>) => void
    vi.spyOn(api, 'rollbackWorkflow').mockReturnValue(new Promise((resolve) => { resolveRollback = resolve }))
    const restored = { ...publishedWorkflow, draftRevision: 2, draftGraph: { ...workflow.draftGraph, nodes: workflow.draftGraph.nodes.map((node) => ({ ...node, position: { x: node.position.x + 40, y: node.position.y } })) } }
    render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
    await screen.findByText('演示助手')
    await userEvent.click(screen.getByRole('button', { name: '版本历史' }))
    await userEvent.click(await screen.findByRole('button', { name: '恢复 v1 为草稿' }))
	await userEvent.keyboard('{Escape}')
	expect(screen.queryByRole('dialog', { name: '恢复 v1 为草稿？' })).not.toBeInTheDocument()
	expect(screen.getByRole('dialog', { name: '版本历史' })).toBeInTheDocument()
	await userEvent.click(screen.getByRole('button', { name: '恢复 v1 为草稿' }))
    await userEvent.click(screen.getByRole('button', { name: '确认恢复' }))
    expect(screen.getByRole('button', { name: '关闭工作台' })).toBeDisabled()
    fireEvent.click(screen.getByTestId('node-start'))
    expect(screen.queryByRole('heading', { name: '开始' })).not.toBeInTheDocument()
    resolveRollback({ workflow: restored, rollbackCheckpoint: { sourceRevision: 1, restoredRevision: 2, restoredFromVersion: 1, createdAt: '2026-08-27T01:00:00Z' } })
    expect(await screen.findByText('已回滚到版本 1')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '关闭工作台' })).toBeEnabled()
    await userEvent.click(screen.getByRole('button', { name: '关闭工作台' }))
	await vi.waitFor(() => expect(screen.getByText('更多操作')).toHaveFocus())
    await userEvent.click(screen.getByRole('button', { name: '添加节点' }))
    await userEvent.click(screen.getByRole('button', { name: /^提示词模板/ }))
    confirmPendingPlacement()
    await vi.waitFor(() => expect(api.saveWorkflow).toHaveBeenCalledWith('w1', expect.objectContaining({ draftRevision: 2 })), { timeout: 2000 })
  })

  it('页面设置 revision 冲突时保留输入和对话框', async () => {
    vi.spyOn(api, 'saveAgentPresentation').mockRejectedValue(new APIError(409, 'WORKFLOW_REVISION_CONFLICT', '内部冲突'))
    render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
    await screen.findByText('演示助手')
    await userEvent.click(screen.getByRole('button', { name: 'Agent 页面设置' }))
    await userEvent.clear(screen.getByLabelText('页面标题'))
    await userEvent.type(screen.getByLabelText('页面标题'), '保留的标题')
    await userEvent.click(screen.getByRole('button', { name: '保存设置' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('草稿已在其他页面更新，请刷新后重试')
    expect(screen.getByLabelText('页面标题')).toHaveValue('保留的标题')
    expect(screen.getByRole('dialog', { name: '页面设置' })).toBeInTheDocument()
  })

  it('应用配置后直接打开测试工作台并运行最新草稿', async () => {
    vi.spyOn(api, 'runDraft').mockResolvedValue(new Response('{"type":"run.completed","sequence":1,"output":{}}\n', { headers: { 'content-type': 'application/x-ndjson' } }))
    render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
    await screen.findByText('演示助手')
    await userEvent.click(screen.getByRole('button', { name: '添加节点' }))
    await userEvent.click(screen.getByRole('button', { name: /^提示词模板/ }))
    confirmPendingPlacement()
    fireEvent.change(screen.getByLabelText('模板'), { target: { value: '回答：{{topic}}' } })
    await vi.waitFor(() => expect(screen.getByRole('button', { name: '应用配置' })).toBeEnabled())

    await userEvent.click(screen.getByRole('button', { name: '应用并试运行' }))
    expect(screen.queryByRole('dialog', { name: '保存节点配置更改？' })).not.toBeInTheDocument()
    expect(await screen.findByRole('dialog', { name: '测试运行' })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '运行' }))
    await vi.waitFor(() => expect(api.runDraft).toHaveBeenCalledWith('w1', { draftRevision: 2, input: {} }, expect.any(AbortSignal)), { timeout: 2500 })
  })

  it('离开测试工作台会取消仍在进行的浏览器请求', async () => {
    let signal: AbortSignal | undefined
    vi.spyOn(api, 'runDraft').mockImplementation((_workflowID, _request, requestSignal) => {
      signal = requestSignal
      return new Promise((_resolve, reject) => requestSignal?.addEventListener('abort', () => reject(new DOMException('操作已取消', 'AbortError')), { once: true }))
    })
    render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
    await screen.findByText('演示助手')
    await userEvent.click(screen.getByRole('button', { name: '测试运行' }))
    await userEvent.click(screen.getByRole('button', { name: '运行' }))
    await vi.waitFor(() => expect(signal).toBeDefined())
    await userEvent.click(screen.getByRole('button', { name: '关闭工作台' }))
    expect(signal?.aborted).toBe(true)
  })

  it('发布前等待保存队列完成并使用新 revision', async () => {
    let resolveSave!: (value: typeof workflow) => void
    vi.mocked(api.saveWorkflow).mockReturnValueOnce(new Promise((resolve) => { resolveSave = resolve }))
    vi.spyOn(api, 'validateWorkflow').mockResolvedValue({ valid: true, issues: [] })
    vi.spyOn(api, 'publishWorkflow').mockResolvedValue({
      id: 'v1', workflowId: 'w1', version: 1, graph: workflow.draftGraph, inputSchema: {}, agentPresentation: workflow.agentPresentation, createdAt: '2026-08-17T00:00:00Z',
    })
    render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
    await screen.findByText('演示助手')
    await userEvent.click(screen.getByRole('button', { name: '添加节点' }))
    await userEvent.click(screen.getByRole('button', { name: /^提示词模板/ }))
    confirmPendingPlacement()
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
    confirmPendingPlacement()
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
    confirmPendingPlacement()
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
    confirmPendingPlacement()
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
    confirmPendingPlacement()
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

function nodeTransfer(nodeKey: string) {
  return {
    types: ['application/x-agent-studio-node'],
    getData: (type: string) => type === 'application/x-agent-studio-node' ? nodeKey : '',
    setData: vi.fn(),
    dropEffect: 'none',
    effectAllowed: 'copy',
  } as unknown as DataTransfer
}

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

function runEvent(sequence: number, type: RunEvent['type'], nodeId?: string): RunEvent {
  return { sequence, type, runId: 'r1', ...(nodeId ? { nodeId } : {}), activePorts: [], inputRedactedPaths: [], outputRedactedPaths: [], timestamp: '2026-08-27T00:00:00Z' }
}
