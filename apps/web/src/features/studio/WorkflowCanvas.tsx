import {
  Background,
  Controls,
  MiniMap,
  ReactFlow,
  ReactFlowProvider,
  ViewportPortal,
  type Connection,
  type EdgeChange,
  type NodeChange,
  type ReactFlowInstance,
  type Viewport,
  type XYPosition,
} from '@xyflow/react'
import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
  type ComponentProps,
  type DragEvent,
} from 'react'

import { GenericNode } from './GenericNode'
import { CanvasEmptyGuide } from './CanvasEmptyGuide'
import { NODE_DEFINITION_MIME } from './nodeLibraryModel'
import type { StudioEdge, StudioNode } from './types'

const nodeTypes = { studio: GenericNode }

interface WorkflowCanvasProps {
  nodes: StudioNode[]
  edges: StudioEdge[]
  onNodesChange: (changes: NodeChange<StudioNode>[]) => void
  onEdgesChange: (changes: EdgeChange<StudioEdge>[]) => void
  onConnect: (connection: Connection) => void
  onDelete?: (elements: { nodes: StudioNode[]; edges: StudioEdge[] }) => void
  isValidConnection: (connection: Connection | StudioEdge) => boolean
  onNodeClick: (node: StudioNode, trigger: HTMLElement) => void
  readOnly?: boolean
	fitRequest?: number
  currentNodeID?: string
  onViewportChange?: (viewport: Viewport) => void
  onInvalidConnection?: (attempt: InvalidConnectionAttempt) => void
  onNodeDefinitionDrop?: (nodeKey: string, position: XYPosition) => void
  emptyGuide?: { position: XYPosition; onAdd: () => void }
}

export interface InvalidConnectionAttempt {
  connection: Connection
  clientX: number
  clientY: number
}

export interface WorkflowCanvasHandle {
  getViewportCenter: () => XYPosition
  screenToFlowPosition: (point: XYPosition) => XYPosition | undefined
  fitView: () => Promise<boolean>
}

export const WorkflowCanvas = forwardRef<WorkflowCanvasHandle, WorkflowCanvasProps>(function WorkflowCanvas(props, ref) {
	const containerRef = useRef<HTMLDivElement>(null)
	const fitted = useRef(false)
	const instanceRef = useRef<ReactFlowInstance<StudioNode, StudioEdge> | undefined>(undefined)
	const [nodeDropActive, setNodeDropActive] = useState(false)
	const lastFitRequest = useRef(props.fitRequest)
	const handleInit = useCallback((instance: ReactFlowInstance<StudioNode, StudioEdge>) => {
		instanceRef.current = instance
		if (fitted.current) return
		fitted.current = true
		void instance.fitView({ padding: 0.2, maxZoom: 1.2 })
	}, [])
	useEffect(() => {
		if (lastFitRequest.current === props.fitRequest) return
		lastFitRequest.current = props.fitRequest
		if (instanceRef.current) void instanceRef.current.fitView({ padding: 0.2, maxZoom: 1.2 })
	}, [props.fitRequest])
	useImperativeHandle(ref, () => ({
		getViewportCenter: () => {
			const instance = instanceRef.current
			const rect = containerRef.current?.getBoundingClientRect()
			if (!instance || !rect) return { x: 320, y: 260 }
			return instance.screenToFlowPosition({ x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 })
		},
		screenToFlowPosition: (point) => instanceRef.current?.screenToFlowPosition(point),
		fitView: () => instanceRef.current?.fitView({ padding: 0.2, maxZoom: 1.2 }) ?? Promise.resolve(false),
	}), [])
	const acceptsNodeDefinition = (event: DragEvent<HTMLDivElement>) =>
		!props.readOnly &&
		Boolean(props.onNodeDefinitionDrop) &&
		event.dataTransfer.types.includes(NODE_DEFINITION_MIME)
	const handleNodeDragOver = (event: DragEvent<HTMLDivElement>) => {
		if (!acceptsNodeDefinition(event)) return
		event.preventDefault()
		event.dataTransfer.dropEffect = 'copy'
		setNodeDropActive(true)
	}
	const handleNodeDrop = (event: DragEvent<HTMLDivElement>) => {
		setNodeDropActive(false)
		if (!acceptsNodeDefinition(event)) return
		const nodeKey = event.dataTransfer.getData(NODE_DEFINITION_MIME)
		const position = instanceRef.current?.screenToFlowPosition({
			x: event.clientX,
			y: event.clientY,
		})
		if (!nodeKey || !position) return
		event.preventDefault()
		props.onNodeDefinitionDrop?.(nodeKey, position)
	}
	const canvasNodes = props.currentNodeID === undefined
		? props.nodes
		: props.nodes.map((node) => ({ ...node, selected: props.currentNodeID === node.id }))
  const onConnectEnd: NonNullable<ComponentProps<typeof ReactFlow>['onConnectEnd']> = (event, state) => {
    if (state.isValid || !state.fromHandle || !state.toHandle) return
    const source = state.fromHandle.type === 'source' ? state.fromHandle : state.toHandle
    const target = state.fromHandle.type === 'target' ? state.fromHandle : state.toHandle
    const point = 'changedTouches' in event ? event.changedTouches[0] : event
    props.onInvalidConnection?.({
      connection: { source: source.nodeId, sourceHandle: source.id ?? null, target: target.nodeId, targetHandle: target.id ?? null },
      clientX: point?.clientX ?? 0,
      clientY: point?.clientY ?? 0,
    })
  }
  return (
    <div
      ref={containerRef}
      className="workflow-canvas"
      aria-label="工作流画布"
      data-node-drop-active={nodeDropActive}
      onDragOver={handleNodeDragOver}
      onDragLeave={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
          setNodeDropActive(false)
        }
      }}
      onDrop={handleNodeDrop}
    >
      <ReactFlowProvider>
        <ReactFlow
		  nodes={canvasNodes}
          edges={props.edges}
          nodeTypes={nodeTypes}
          onNodesChange={(changes) => { if (!props.readOnly) props.onNodesChange(changes) }}
          onEdgesChange={(changes) => { if (!props.readOnly) props.onEdgesChange(changes) }}
          onConnect={(connection) => { if (!props.readOnly) props.onConnect(connection) }}
          onDelete={(elements) => { if (!props.readOnly) props.onDelete?.(elements) }}
          onConnectEnd={onConnectEnd}
          isValidConnection={props.isValidConnection}
          onNodeClick={(event, node) => {
            if ((event.target as HTMLElement).closest('.react-flow__handle')) return
			const trigger = (event.target as HTMLElement).closest<HTMLElement>('.react-flow__node')
			if (trigger) props.onNodeClick(node, trigger)
          }}
		  onInit={handleInit}
		  onMoveEnd={(_event, viewport) => props.onViewportChange?.(viewport)}
		  nodesDraggable={!props.readOnly}
		  nodesConnectable={!props.readOnly}
		  deleteKeyCode={props.readOnly ? null : ['Backspace', 'Delete']}
        >
          <Background gap={22} size={1.2} />
          <Controls />
          <MiniMap pannable zoomable />
          {props.emptyGuide && (
            <ViewportPortal>
              <CanvasEmptyGuide
                position={props.emptyGuide.position}
                onAdd={props.emptyGuide.onAdd}
              />
            </ViewportPortal>
          )}
        </ReactFlow>
      </ReactFlowProvider>
    </div>
  )
})
