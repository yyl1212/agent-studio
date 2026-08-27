import { render } from '@testing-library/react'
import { useEffect } from 'react'
import { expect, it, vi } from 'vitest'

import { WorkflowCanvas } from './WorkflowCanvas'
import type { StudioNode } from './types'

const fitView = vi.hoisted(() => vi.fn())

vi.mock('@xyflow/react', async () => {
  const React = await import('react')
  return {
    Background: () => null,
    Controls: () => null,
    MiniMap: () => null,
    ReactFlowProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
    ReactFlow: (props: { onInit?: (instance: { fitView: typeof fitView }) => void; children: React.ReactNode }) => {
      useEffect(() => { props.onInit?.({ fitView }) }, [props.onInit])
      return <div>{props.children}</div>
    },
    Handle: () => null,
    Position: { Left: 'left', Right: 'right' },
    useUpdateNodeInternals: () => vi.fn(),
  }
})

it('节点或工作台状态变化时不会重复重置画布视口', () => {
  const props = { edges: [], onNodesChange: vi.fn(), onEdgesChange: vi.fn(), onConnect: vi.fn(), isValidConnection: vi.fn(), onNodeClick: vi.fn() }
  const { rerender } = render(<WorkflowCanvas {...props} nodes={[node('a')]} />)
  expect(fitView).toHaveBeenCalledTimes(1)
  rerender(<WorkflowCanvas {...props} nodes={[node('a'), node('b')]} />)
  expect(fitView).toHaveBeenCalledTimes(1)
})

it('fitRequest 推进时主动重新适配服务端替换后的画布', () => {
	fitView.mockClear()
	const props = { edges: [], nodes: [node('a')], onNodesChange: vi.fn(), onEdgesChange: vi.fn(), onConnect: vi.fn(), isValidConnection: vi.fn(), onNodeClick: vi.fn() }
	const { rerender } = render(<WorkflowCanvas {...props} fitRequest={0} />)
	expect(fitView).toHaveBeenCalledTimes(1)
	rerender(<WorkflowCanvas {...props} fitRequest={1} />)
	expect(fitView).toHaveBeenCalledTimes(2)
})

function node(id: string): StudioNode {
  return { id, type: 'studio', position: { x: 0, y: 0 }, data: { nodeType: 'template', typeVersion: '1', config: {}, ports: { inputs: [], outputs: [] }, issues: [] } }
}
