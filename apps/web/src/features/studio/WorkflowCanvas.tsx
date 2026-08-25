import {
  Background,
  Controls,
  MiniMap,
  ReactFlow,
  ReactFlowProvider,
  type Connection,
  type EdgeChange,
  type NodeChange,
} from '@xyflow/react'

import { GenericNode } from './GenericNode'
import type { StudioEdge, StudioNode } from './types'

const nodeTypes = { studio: GenericNode }

interface WorkflowCanvasProps {
  nodes: StudioNode[]
  edges: StudioEdge[]
  onNodesChange: (changes: NodeChange<StudioNode>[]) => void
  onEdgesChange: (changes: EdgeChange<StudioEdge>[]) => void
  onConnect: (connection: Connection) => void
  isValidConnection: (connection: Connection | StudioEdge) => boolean
  onNodeClick: (node: StudioNode) => void
  readOnly?: boolean
  currentNodeID?: string
}

export function WorkflowCanvas(props: WorkflowCanvasProps) {
	const canvasNodes = props.currentNodeID === undefined
		? props.nodes
		: props.nodes.map((node) => ({ ...node, selected: props.currentNodeID === node.id }))
  return (
    <div className="workflow-canvas" aria-label="工作流画布">
      <ReactFlowProvider>
        <ReactFlow
		  nodes={canvasNodes}
          edges={props.edges}
          nodeTypes={nodeTypes}
          onNodesChange={(changes) => { if (!props.readOnly) props.onNodesChange(changes) }}
          onEdgesChange={(changes) => { if (!props.readOnly) props.onEdgesChange(changes) }}
          onConnect={(connection) => { if (!props.readOnly) props.onConnect(connection) }}
          isValidConnection={props.isValidConnection}
          onNodeClick={(event, node) => {
            if ((event.target as HTMLElement).closest('.react-flow__handle')) return
            props.onNodeClick(node)
          }}
          fitView
          fitViewOptions={{ padding: 0.2, maxZoom: 1.2 }}
		  nodesDraggable={!props.readOnly}
		  nodesConnectable={!props.readOnly}
		  deleteKeyCode={props.readOnly ? null : ['Backspace', 'Delete']}
        >
          <Background gap={22} size={1.2} />
          <Controls />
          <MiniMap pannable zoomable />
        </ReactFlow>
      </ReactFlowProvider>
    </div>
  )
}
