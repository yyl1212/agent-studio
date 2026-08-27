import { fireEvent, render, screen } from '@testing-library/react'
import { createRef, useEffect, type MouseEvent as ReactMouseEvent } from 'react'
import { beforeEach, expect, it, vi } from 'vitest'

import { WorkflowCanvas, type InvalidConnectionAttempt, type WorkflowCanvasHandle } from './WorkflowCanvas'
import type { StudioNode } from './types'

const fitView = vi.hoisted(() => vi.fn())
const screenToFlowPosition = vi.hoisted(() => vi.fn(() => ({ x: 420, y: 260 })))

vi.mock('@xyflow/react', async () => {
  const React = await import('react')
  return {
    Background: () => null,
    Controls: () => null,
    MiniMap: () => null,
    ReactFlowProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
    ReactFlow: (props: {
      onInit?: (instance: { fitView: typeof fitView; screenToFlowPosition: typeof screenToFlowPosition }) => void
      onNodeClick?: (event: ReactMouseEvent<HTMLDivElement>, node: StudioNode) => void
      onConnectEnd?: (event: MouseEvent, state: unknown) => void
      nodes: StudioNode[]
      children: React.ReactNode
    }) => {
      useEffect(() => { props.onInit?.({ fitView, screenToFlowPosition }) }, [props.onInit])
      return <div>
        {props.nodes.map((flowNode) => <div className="react-flow__node" data-selected={String(Boolean(flowNode.selected))} data-testid={`flow-node-${flowNode.id}`} key={flowNode.id} onClick={(event) => props.onNodeClick?.(event, flowNode)}>{flowNode.id}</div>)}
        <button type="button" onClick={(event) => props.onConnectEnd?.(event.nativeEvent, {
          isValid: false,
          fromHandle: { type: 'source', nodeId: 'source', id: 'text' },
          toHandle: { type: 'target', nodeId: 'target', id: 'value' },
        })}>结束无效连线</button>
        {props.children}
      </div>
    },
    Handle: () => null,
    Position: { Left: 'left', Right: 'right' },
    useUpdateNodeInternals: () => vi.fn(),
  }
})

const baseProps = { edges: [], onNodesChange: vi.fn(), onEdgesChange: vi.fn(), onConnect: vi.fn(), isValidConnection: vi.fn(), onNodeClick: vi.fn() }

beforeEach(() => {
  fitView.mockClear()
  screenToFlowPosition.mockClear()
})

it('通过窄接口返回当前视口中心并允许手动适配', async () => {
  const ref = createRef<WorkflowCanvasHandle>()
  render(<WorkflowCanvas ref={ref} {...baseProps} nodes={[node('a')]} />)

  expect(ref.current?.getViewportCenter()).toEqual({ x: 420, y: 260 })
  await ref.current?.fitView()
  expect(screenToFlowPosition).toHaveBeenCalledOnce()
  expect(fitView).toHaveBeenCalledTimes(2)
})

it('把被点击的画布节点作为焦点回退目标上报', () => {
  const onNodeClick = vi.fn()
  render(<WorkflowCanvas {...baseProps} nodes={[node('a')]} onNodeClick={onNodeClick} />)

  const trigger = screen.getByTestId('flow-node-a')
  fireEvent.click(trigger)
  expect(onNodeClick).toHaveBeenCalledWith(expect.objectContaining({ id: 'a' }), trigger)
})

it('节点或工作台状态变化时不会重复重置画布视口', () => {
  const { rerender } = render(<WorkflowCanvas {...baseProps} nodes={[node('a')]} />)
  expect(fitView).toHaveBeenCalledTimes(1)
  rerender(<WorkflowCanvas {...baseProps} nodes={[node('a'), node('b')]} />)
  expect(fitView).toHaveBeenCalledTimes(1)
})

it('只把当前运行节点标记为选中', () => {
  render(<WorkflowCanvas {...baseProps} nodes={[node('a'), node('b')]} currentNodeID="b" />)
  expect(screen.getByTestId('flow-node-a')).toHaveAttribute('data-selected', 'false')
  expect(screen.getByTestId('flow-node-b')).toHaveAttribute('data-selected', 'true')
})

it('在连线结束时上报无效候选和指针位置', () => {
  const onInvalidConnection = vi.fn<(attempt: InvalidConnectionAttempt) => void>()
  render(<WorkflowCanvas {...baseProps} nodes={[node('a')]} onInvalidConnection={onInvalidConnection} />)

  fireEvent.click(screen.getByRole('button', { name: '结束无效连线' }), { clientX: 120, clientY: 80 })
  expect(onInvalidConnection).toHaveBeenCalledWith({
    connection: { source: 'source', sourceHandle: 'text', target: 'target', targetHandle: 'value' },
    clientX: 120,
    clientY: 80,
  })
})

it('fitRequest 推进时主动重新适配服务端替换后的画布', () => {
	const { rerender } = render(<WorkflowCanvas {...baseProps} nodes={[node('a')]} fitRequest={0} />)
	expect(fitView).toHaveBeenCalledTimes(1)
	rerender(<WorkflowCanvas {...baseProps} nodes={[node('a')]} fitRequest={1} />)
	expect(fitView).toHaveBeenCalledTimes(2)
})

function node(id: string): StudioNode {
  return { id, type: 'studio', position: { x: 0, y: 0 }, data: { nodeType: 'template', typeVersion: '1', config: {}, ports: { inputs: [], outputs: [] }, issues: [] } }
}
