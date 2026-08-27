import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import type { NodeDefinition } from '../../lib/api/client'
import { NodeConfigPanel } from './NodeConfigPanel'
import type { StudioNode } from './types'
import type { UseNodeConfigDraftResult } from './useNodeConfigDraft'

const definition: NodeDefinition = { type: 'dynamic', version: '1', title: '动态节点', description: '', category: 'logic', configSchema: { type: 'object', required: ['mode'], properties: { mode: { type: 'string', title: '模式' }, note: { type: 'string', title: '备注' } } }, inputs: [], outputs: [], capabilities: [], executionSafety: 'pure', package: { name: 'builtin', displayName: '内置', license: 'Apache-2.0', repository: 'https://example.com', source: 'builtin' } }
const node: StudioNode = { id: 'node-a', type: 'studio', position: { x: 0, y: 0 }, data: { nodeType: 'dynamic', typeVersion: '1', config: { mode: 'old' }, definition, ports: { inputs: [], outputs: [] }, issues: [] } }
const readyDraft: UseNodeConfigDraftResult = {
  nodeId: 'node-a', draft: { mode: 'new' }, normalized: { mode: 'new' }, dirty: true, status: 'ready',
  preview: { ports: { inputs: [], outputs: [] }, added: [], removed: [], invalidEdgeIds: [] }, error: '',
  setDraft: vi.fn(), reset: vi.fn(), retry: vi.fn(), markApplied: vi.fn(),
}

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
  const draft: UseNodeConfigDraftResult = { ...readyDraft, preview: { ports: { inputs: [], outputs: [] }, added: ['output:new'], removed: ['output:old'], invalidEdgeIds: ['edge-1'] } }
  render(<NodeConfigPanel titleId="config-title" node={node} draft={draft} onApply={onApply} onApplyAndTest={vi.fn()} />)
  expect(screen.getByText('新增 output:new')).toBeInTheDocument()
  expect(screen.getByText('移除 output:old')).toBeInTheDocument()
  expect(screen.getByText('1 条连线将失效')).toBeInTheDocument()
  await userEvent.keyboard('{Meta>}{Enter}{/Meta}')
  expect(onApply).toHaveBeenCalledWith({ mode: 'new' }, { inputs: [], outputs: [] })
})
