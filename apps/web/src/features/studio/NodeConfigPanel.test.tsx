import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import type { NodeDefinition } from '../../lib/api/client'
import type { InvalidEdgeImpact } from './configDraft'
import { NodeConfigPanel } from './NodeConfigPanel'
import type { StudioNode } from './types'
import type { NodeConfigStatus, UseNodeConfigDraftResult } from './useNodeConfigDraft'

const definition: NodeDefinition = { type: 'dynamic', version: '1', title: '动态节点', description: '', category: 'logic', configSchema: { type: 'object', required: ['mode'], properties: { mode: { type: 'string', title: '模式' }, note: { type: 'string', title: '备注' } } }, inputs: [], outputs: [], capabilities: [], executionSafety: 'pure', package: { name: 'builtin', displayName: '内置', license: 'Apache-2.0', repository: 'https://example.com', source: 'builtin' } }
const node: StudioNode = { id: 'node-a', type: 'studio', position: { x: 0, y: 0 }, data: { nodeType: 'dynamic', typeVersion: '1', config: { mode: 'old' }, definition, ports: { inputs: [], outputs: [] }, issues: [] } }
const readyDraft: UseNodeConfigDraftResult = {
  nodeId: 'node-a', draft: { mode: 'new' }, normalized: { mode: 'new' }, dirty: true, status: 'ready',
  validation: { normalized: { mode: 'new' }, errors: {}, valid: true },
  preview: { ports: { inputs: [], outputs: [] }, added: [], removed: [], invalidEdges: [] }, error: '',
  setDraft: vi.fn(), reset: vi.fn(), retry: vi.fn(), markApplied: vi.fn(),
}
const draftFor = (status: NodeConfigStatus): UseNodeConfigDraftResult => ({
  ...readyDraft,
  status,
  dirty: status !== 'idle',
  preview: status === 'ready' ? readyDraft.preview : undefined,
})
const idleDraft = draftFor('idle')
const emptyNode: StudioNode = {
  ...node,
  data: { ...node.data, definition: { ...definition, configSchema: { type: 'object', properties: {} } }, config: {} },
}
const impact: InvalidEdgeImpact = {
  edgeId: 'edge-old', sourceNodeId: 'node-a', sourcePort: 'old', targetNodeId: 'node-b', targetPort: 'input',
}

it('无配置字段显示明确空状态并聚焦标题', async () => {
  render(<NodeConfigPanel titleId="config-title" node={emptyNode} draft={idleDraft} onApply={vi.fn()} onApplyAndTest={vi.fn()} />)
  expect(screen.getByText('此节点无需配置')).toBeVisible()
  expect(screen.queryByRole('button', { name: '应用配置' })).not.toBeInTheDocument()
  await vi.waitFor(() => expect(screen.getByRole('heading', { name: '动态节点' })).toHaveFocus())
})

it.each([
  ['idle', '已应用'],
  ['dirty', '有未应用更改'],
  ['resolving', '正在解析端口'],
  ['ready', '可以应用'],
  ['error', '需要处理'],
] as const)('%s 状态展示中文文字语义', (status, label) => {
  render(<NodeConfigPanel titleId="config-title" node={node} draft={draftFor(status)} onApply={vi.fn()} onApplyAndTest={vi.fn()} />)
  expect(screen.getByRole('status')).toHaveTextContent(label)
})

it('边界锁、重置和解析重试均有独立操作', async () => {
  const boundaryNode = { ...node, data: { ...node.data, nodeType: 'start' } }
  const draft = { ...draftFor('error'), errorKind: 'resolve' as const, error: '解析失败' }
  render(<NodeConfigPanel titleId="config-title" node={boundaryNode} draft={draft} onApply={vi.fn()} onApplyAndTest={vi.fn()} />)
  expect(screen.getByText('工作流唯一节点，不可删除')).toBeVisible()
  await userEvent.click(screen.getByRole('button', { name: '重置' }))
  await userEvent.click(screen.getByRole('button', { name: '重试端口解析' }))
  expect(draft.reset).toHaveBeenCalledOnce()
  expect(draft.retry).toHaveBeenCalledOnce()
})

it('就绪草稿提供应用并试运行动作', async () => {
  const onApplyAndTest = vi.fn()
  render(<NodeConfigPanel titleId="config-title" node={node} draft={readyDraft} onApply={vi.fn()} onApplyAndTest={onApplyAndTest} />)
  await userEvent.click(screen.getByRole('button', { name: '应用并试运行' }))
  expect(onApplyAndTest).toHaveBeenCalledWith({ mode: 'new' }, readyDraft.preview?.ports)
})

it('节点配置打开后聚焦首个可编辑字段', async () => {
  render(<NodeConfigPanel titleId="config-title" node={node} draft={readyDraft} onApply={vi.fn()} onApplyAndTest={vi.fn()} />)
  await vi.waitFor(() => expect(screen.getByLabelText('模式')).toHaveFocus())
})

it('展示端口变化并用快捷键应用已就绪草稿', async () => {
  const onApply = vi.fn()
  const draft: UseNodeConfigDraftResult = { ...readyDraft, preview: { ports: { inputs: [], outputs: [] }, added: ['output:new'], removed: ['output:old'], invalidEdges: [impact] } }
  render(<NodeConfigPanel titleId="config-title" node={node} draft={draft} onApply={onApply} onApplyAndTest={vi.fn()} />)
  expect(screen.getByText('新增 output:new')).toBeInTheDocument()
  expect(screen.getByText('移除 output:old')).toBeInTheDocument()
  expect(screen.getByText('1 条连线将失效')).toBeInTheDocument()
  await userEvent.keyboard('{Meta>}{Enter}{/Meta}')
  expect(onApply).toHaveBeenCalledWith({ mode: 'new' }, { inputs: [], outputs: [] })
})
