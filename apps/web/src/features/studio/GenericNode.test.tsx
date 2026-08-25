import { render, screen } from '@testing-library/react'
import { expect, it, vi } from 'vitest'

import { GenericNode } from './GenericNode'

vi.mock('@xyflow/react', () => ({
  Handle: ({ title, type }: { title: string; type: string }) => <span role="img" aria-label={`${type === 'target' ? '输入' : '输出'}端口 ${title}`}>{title}</span>,
  Position: { Left: 'left', Right: 'right' },
  useUpdateNodeInternals: () => vi.fn(),
}))

it('节点卡片同时显示定义标题、版本、节点 ID、端口和文字状态', () => {
  const props = {
    id: 'llm-main', selected: false,
    data: {
      nodeType: 'llm', typeVersion: '2', config: {}, definition: { title: '结构化生成', category: 'AI' }, issues: [], debugStatus: 'failed',
      ports: {
        inputs: [{ key: 'prompt', title: '提示词', type: 'string', cardinality: 'one' }],
        outputs: [{ key: 'answer', title: '回答', type: 'string', cardinality: 'one' }],
      },
    },
  } as unknown as Parameters<typeof GenericNode>[0]
  render(<GenericNode {...props} />)
  expect(screen.getByText('结构化生成')).toBeInTheDocument()
  expect(screen.getByText('llm@2')).toBeInTheDocument()
  expect(screen.getByText('llm-main')).toBeInTheDocument()
  expect(screen.getByRole('img', { name: '输入端口 提示词' })).toBeInTheDocument()
  expect(screen.getByRole('img', { name: '输出端口 回答' })).toBeInTheDocument()
  expect(screen.getByText('× 失败')).toBeInTheDocument()
})
