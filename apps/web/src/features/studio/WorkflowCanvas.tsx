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
}

export function WorkflowCanvas(props: WorkflowCanvasProps) {
  return (
    <div className="workflow-canvas" aria-label="工作流画布">
      <ReactFlowProvider>
        <ReactFlow
          nodes={props.nodes}
          edges={props.edges}
          nodeTypes={nodeTypes}
          onNodesChange={props.onNodesChange}
          onEdgesChange={props.onEdgesChange}
          onConnect={props.onConnect}
          isValidConnection={props.isValidConnection}
          onNodeClick={(event, node) => {
            if ((event.target as HTMLElement).closest('.react-flow__handle')) return
            props.onNodeClick(node)
          }}
          fitView
          fitViewOptions={{ padding: 0.2, maxZoom: 1.2 }}
          deleteKeyCode={['Backspace', 'Delete']}
        >
          <Background gap={22} size={1.2} />
          <Controls />
          <MiniMap pannable zoomable />
        </ReactFlow>
      </ReactFlowProvider>
    </div>
  )
}
