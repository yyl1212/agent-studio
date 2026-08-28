import { render, screen } from '@testing-library/react'
import type { NodeProps } from '@xyflow/react'
import { expect, it, vi } from 'vitest'

import type { NodeDefinition } from '../../lib/api/client'
import { GenericNode } from './GenericNode'
import type { StudioNode, StudioNodeData } from './types'

vi.mock('@xyflow/react', () => ({
  Handle: ({ 'aria-label': ariaLabel }: { 'aria-label': string }) => (
    <span role="img" aria-label={ariaLabel} />
  ),
  Position: { Left: 'left', Right: 'right' },
  useUpdateNodeInternals: () => vi.fn(),
}))

const templateDefinition: NodeDefinition = {
  type: 'template', version: '1', title: '提示词模板', description: '把变量渲染为可复用提示词', category: '文本',
  configSchema: {},
  inputs: [{ key: 'prompt', title: '提示词', type: 'string', required: true, cardinality: 'one' }],
  outputs: [{ key: 'text', title: '文本', type: 'string', required: false, cardinality: 'one' }],
  capabilities: [], executionSafety: 'pure',
  package: { name: 'agent-studio.dev/core', displayName: 'Agent Studio Core', version: 'v0.5.0', license: 'Apache-2.0', repository: 'https://github.com/yyl1212/agent-studio', source: 'builtin' },
}

type NodeOverrides = Partial<StudioNodeData> & { id?: string; selected?: boolean }

function nodeProps({ id = 'template-1', selected = false, ...dataOverrides }: NodeOverrides = {}): NodeProps<StudioNode> {
  return {
    id, type: 'studio',
    data: {
      nodeType: 'template', typeVersion: '1', config: {}, definition: templateDefinition,
      ports: {
        inputs: [{ key: 'prompt', title: '提示词', type: 'string', required: true, cardinality: 'one' }],
        outputs: [{ key: 'text', title: '文本', type: 'string', required: false, cardinality: 'one' }],
      },
      issues: [],
      ...dataOverrides,
    },
    dragging: false, zIndex: 0, selectable: true, deletable: true, selected,
    draggable: true, isConnectable: true, positionAbsoluteX: 0, positionAbsoluteY: 0,
  }
}

it('边界节点展示唯一锁标记且状态不只依赖颜色', () => {
  render(<GenericNode {...nodeProps({ nodeType: 'start', boundary: true, readOnly: false })} />)
  expect(screen.getByText('工作流唯一')).toBeVisible()
  expect(screen.getByLabelText('开始节点，不可删除')).toBeVisible()
})

it('端口提供名称、类型和 44px 交互包装', () => {
  const { container } = render(<GenericNode {...nodeProps()} />)
  expect(screen.getByText('提示词 · string')).toBeInTheDocument()
  expect(screen.getByLabelText('输入端口 提示词，类型 string')).toBeInTheDocument()
  expect(container.querySelector('.node-port-hit-area')).toBeInTheDocument()
})

it.each([
  ['选中', { selected: true }, '已选中'],
  ['错误', { issues: [{ code: 'INVALID', message: '配置错误', path: 'config' }] }, '1 个问题'],
  ['运行', { debugStatus: 'running' }, '运行中'],
  ['成功', { debugStatus: 'completed' }, '已完成'],
  ['失败', { debugStatus: 'failed' }, '失败'],
  ['只读', { readOnly: true }, '只读'],
] satisfies Array<[string, NodeOverrides, string]>)('%s 状态包含文字语义', (_name, overrides, label) => {
  render(<GenericNode {...nodeProps(overrides)} />)
  expect(screen.getByText(new RegExp(label))).toBeVisible()
})

it('长 ID、缺失 definition 和状态更新保持稳定', () => {
  const longID = 'node-with-a-very-long-identifier'
  const { rerender } = render(<GenericNode {...nodeProps({ definition: undefined, id: longID })} />)
  expect(screen.getByText(longID)).toHaveAttribute('title', longID)
  expect(screen.getAllByText('template')).toHaveLength(2)
  const before = screen.getAllByLabelText(/端口/).length
  rerender(<GenericNode {...nodeProps({ debugStatus: 'running' })} />)
  expect(screen.getAllByLabelText(/端口/)).toHaveLength(before)
})
