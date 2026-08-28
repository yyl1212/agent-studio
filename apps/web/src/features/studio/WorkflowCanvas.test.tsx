import { fireEvent, render, screen } from '@testing-library/react'
import { createRef, useEffect, type MouseEvent as ReactMouseEvent } from 'react'
import { beforeEach, expect, it, vi } from 'vitest'

import type { NodeDefinition } from '../../lib/api/client'
import { WorkflowCanvas, type InvalidConnectionAttempt, type WorkflowCanvasHandle } from './WorkflowCanvas'
import type { StudioEdge, StudioNode } from './types'

const fitView = vi.hoisted(() => vi.fn())
const screenToFlowPosition = vi.hoisted(() => vi.fn(() => ({ x: 420, y: 260 })))

vi.mock('@xyflow/react', async () => {
  const React = await import('react')
  return {
    Background: () => null,
    Controls: () => null,
    MiniMap: () => null,
    ReactFlowProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
    ViewportPortal: ({ children }: { children: React.ReactNode }) => <>{children}</>,
    ReactFlow: (props: {
      onInit?: (instance: { fitView: typeof fitView; screenToFlowPosition: typeof screenToFlowPosition }) => void
      onNodeClick?: (event: ReactMouseEvent<HTMLDivElement>, node: StudioNode) => void
      onConnectEnd?: (event: MouseEvent, state: unknown) => void
      onPaneClick?: () => void
      onDelete?: (elements: { nodes: StudioNode[]; edges: StudioEdge[] }) => void
      nodes: StudioNode[]
      edges: StudioEdge[]
      children: React.ReactNode
    }) => {
      useEffect(() => { props.onInit?.({ fitView, screenToFlowPosition }) }, [props.onInit])
      return <div>
        {props.nodes.map((flowNode) => <div className="react-flow__node" data-selected={String(Boolean(flowNode.selected))} data-read-only={String(Boolean(flowNode.data.readOnly))} data-boundary={String(Boolean(flowNode.data.boundary))} data-testid={`flow-node-${flowNode.id}`} key={flowNode.id} onClick={(event) => props.onNodeClick?.(event, flowNode)}>{flowNode.id}</div>)}
        <button type="button" onClick={(event) => props.onConnectEnd?.(event.nativeEvent, {
          isValid: false,
          fromHandle: { type: 'source', nodeId: 'source', id: 'text' },
          toHandle: { type: 'target', nodeId: 'target', id: 'value' },
        })}>结束无效连线</button>
        <button type="button" onClick={(event) => props.onConnectEnd?.(event.nativeEvent, {
          isValid: false,
          fromHandle: { type: 'source', nodeId: 'source', id: 'text' },
          toHandle: null,
        })}>释放输出端口到空白</button>
        <button type="button" onClick={(event) => props.onConnectEnd?.(event.nativeEvent, {
          isValid: false,
          fromHandle: { type: 'target', nodeId: 'target', id: 'value' },
          toHandle: null,
        })}>从输入端口释放到空白</button>
        <button type="button" onClick={() => props.onPaneClick?.()}>点击画布空白</button>
        <button type="button" onClick={() => props.onDelete?.({ nodes: props.nodes.slice(0, 1), edges: props.edges.slice(0, 1) })}>删除选中元素</button>
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

it('通过窄接口转换任意屏幕坐标', () => {
  const ref = createRef<WorkflowCanvasHandle>()
  render(<WorkflowCanvas ref={ref} {...baseProps} nodes={[node('a')]} />)
  expect(ref.current?.screenToFlowPosition({ x: 900, y: 500 })).toEqual({
    x: 420,
    y: 260,
  })
  expect(screenToFlowPosition).toHaveBeenCalledWith({ x: 900, y: 500 })
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

it('为画布节点注入只读和边界展示状态且不修改原节点', () => {
  const start = node('start')
  start.data.nodeType = 'start'
  render(<WorkflowCanvas {...baseProps} nodes={[start, node('task')]} readOnly />)

  expect(screen.getByTestId('flow-node-start')).toHaveAttribute('data-read-only', 'true')
  expect(screen.getByTestId('flow-node-start')).toHaveAttribute('data-boundary', 'true')
  expect(screen.getByTestId('flow-node-task')).toHaveAttribute('data-boundary', 'false')
  expect(start.data.readOnly).toBeUndefined()
  expect(start.data.boundary).toBeUndefined()
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

it('从输出端口释放到空白处报告连接意图', () => {
  const onConnectionEnd = vi.fn()
  render(
    <WorkflowCanvas
      {...baseProps}
      nodes={[node('source')]}
      onConnectionEnd={onConnectionEnd}
    />,
  )

  fireEvent.click(screen.getByRole('button', { name: '释放输出端口到空白' }), {
    clientX: 440,
    clientY: 260,
  })

  expect(onConnectionEnd).toHaveBeenCalledWith({
    sourceNodeId: 'source',
    sourceHandleId: 'text',
    clientX: 440,
    clientY: 260,
  })
})

it('target 起点、只读画布和已有 target 的无效连接不触发添加', () => {
  const onConnectionEnd = vi.fn()
  const onInvalidConnection = vi.fn()
  const { rerender } = render(
    <WorkflowCanvas
      {...baseProps}
      nodes={[node('source')]}
      onConnectionEnd={onConnectionEnd}
      onInvalidConnection={onInvalidConnection}
    />,
  )

  fireEvent.click(screen.getByRole('button', { name: '从输入端口释放到空白' }))
  expect(onConnectionEnd).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: '结束无效连线' }))
  expect(onInvalidConnection).toHaveBeenCalledOnce()

  rerender(
    <WorkflowCanvas
      {...baseProps}
      readOnly
      nodes={[node('source')]}
      onConnectionEnd={onConnectionEnd}
    />,
  )
  fireEvent.click(screen.getByRole('button', { name: '释放输出端口到空白' }))
  expect(onConnectionEnd).not.toHaveBeenCalled()
})

it('placement 指针移动转换坐标，画布点击确认且预览不进入节点数组', () => {
  const onPlacementMove = vi.fn()
  const onPlacementConfirm = vi.fn()
  render(
    <WorkflowCanvas
      {...baseProps}
      nodes={[node('source')]}
      placement={{ definition: templateDefinition, position: { x: 320, y: 240 } }}
      onPlacementMove={onPlacementMove}
      onPlacementConfirm={onPlacementConfirm}
    />,
  )

  fireEvent.pointerMove(screen.getByLabelText('工作流画布'), { clientX: 440, clientY: 260 })
  expect(onPlacementMove).toHaveBeenCalledWith({ x: 420, y: 260 })
  expect(screen.getAllByTestId(/flow-node-/)).toHaveLength(1)
  expect(screen.getByText('点击画布放置，Esc 取消')).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: '点击画布空白' }))
  expect(onPlacementConfirm).toHaveBeenCalledOnce()
})

it('把节点和边的删除作为一次原子事件上报，并在只读时阻止删除', () => {
  const onDelete = vi.fn()
  const edge = { id: 'a-b', source: 'a', target: 'b' } as StudioEdge
  const { rerender } = render(<WorkflowCanvas {...baseProps} nodes={[node('a'), node('b')]} edges={[edge]} onDelete={onDelete} />)

  fireEvent.click(screen.getByRole('button', { name: '删除选中元素' }))
  expect(onDelete).toHaveBeenCalledWith({ nodes: [expect.objectContaining({ id: 'a' })], edges: [edge] })

  onDelete.mockClear()
  rerender(<WorkflowCanvas {...baseProps} nodes={[node('a'), node('b')]} edges={[edge]} onDelete={onDelete} readOnly />)
  fireEvent.click(screen.getByRole('button', { name: '删除选中元素' }))
  expect(onDelete).not.toHaveBeenCalled()
})

it('画布空图引导只上报添加意图，不产生节点或边变化', () => {
  const onAdd = vi.fn()
  const onNodesChange = vi.fn()
  const onEdgesChange = vi.fn()
  render(
    <WorkflowCanvas
      {...baseProps}
      nodes={[]}
      onNodesChange={onNodesChange}
      onEdgesChange={onEdgesChange}
      emptyGuide={{ position: { x: 300, y: 100 }, onAdd }}
    />,
  )

  fireEvent.click(
    screen.getByRole('button', { name: '在这里添加第一个节点' }),
  )
  expect(onAdd).toHaveBeenCalledOnce()
  expect(onNodesChange).not.toHaveBeenCalled()
  expect(onEdgesChange).not.toHaveBeenCalled()
})

it('fitRequest 推进时主动重新适配服务端替换后的画布', () => {
	const { rerender } = render(<WorkflowCanvas {...baseProps} nodes={[node('a')]} fitRequest={0} />)
	expect(fitView).toHaveBeenCalledTimes(1)
	rerender(<WorkflowCanvas {...baseProps} nodes={[node('a')]} fitRequest={1} />)
	expect(fitView).toHaveBeenCalledTimes(2)
})

const transfer = (
  key: string,
  types = ['application/x-agent-studio-node'],
) => ({
  types,
  getData: vi.fn((type: string) =>
    type === 'application/x-agent-studio-node' ? key : '',
  ),
  setData: vi.fn(),
  dropEffect: 'none',
  effectAllowed: 'copy',
}) as unknown as DataTransfer

it('只接收固定 MIME 并上报转换后的画布坐标', () => {
  const onNodeDefinitionDrop = vi.fn()
  render(
    <WorkflowCanvas
      {...baseProps}
      nodes={[node('a')]}
      onNodeDefinitionDrop={onNodeDefinitionDrop}
    />,
  )
  const canvas = screen.getByLabelText('工作流画布')
  const dataTransfer = transfer('template@1')
  fireEvent.dragOver(canvas, { dataTransfer })
  expect(canvas).toHaveAttribute('data-node-drop-active', 'true')
  fireEvent.drop(canvas, { dataTransfer, clientX: 900, clientY: 500 })
  expect(onNodeDefinitionDrop).toHaveBeenCalledWith('template@1', {
    x: 420,
    y: 260,
  })
  expect(canvas).toHaveAttribute('data-node-drop-active', 'false')
})

it('忽略错误 MIME、空载荷和只读画布', () => {
  const onNodeDefinitionDrop = vi.fn()
  const { rerender } = render(
    <WorkflowCanvas
      {...baseProps}
      nodes={[node('a')]}
      onNodeDefinitionDrop={onNodeDefinitionDrop}
    />,
  )
  const canvas = screen.getByLabelText('工作流画布')
  fireEvent.drop(canvas, {
    dataTransfer: transfer('template@1', ['text/plain']),
    clientX: 10,
    clientY: 10,
  })
  fireEvent.drop(canvas, {
    dataTransfer: transfer(''),
    clientX: 10,
    clientY: 10,
  })
  rerender(
    <WorkflowCanvas
      {...baseProps}
      nodes={[node('a')]}
      onNodeDefinitionDrop={onNodeDefinitionDrop}
      readOnly
    />,
  )
  fireEvent.drop(canvas, {
    dataTransfer: transfer('template@1'),
    clientX: 10,
    clientY: 10,
  })
  expect(onNodeDefinitionDrop).not.toHaveBeenCalled()
})

function node(id: string): StudioNode {
  return { id, type: 'studio', position: { x: 0, y: 0 }, data: { nodeType: 'template', typeVersion: '1', config: {}, ports: { inputs: [], outputs: [] }, issues: [] } }
}

const templateDefinition: NodeDefinition = {
  type: 'template', version: '1', title: '提示词模板', description: '生成提示词', category: '文本',
  configSchema: {}, inputs: [], outputs: [], capabilities: [], executionSafety: 'pure',
  package: { name: 'agent-studio.dev/core', displayName: 'Agent Studio Core', version: 'v0.5.0', license: 'Apache-2.0', repository: 'https://github.com/yyl1212/agent-studio', source: 'builtin' },
}
