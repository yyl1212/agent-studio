import type { XYPosition } from '@xyflow/react'

import type { NodeDefinition } from '../../lib/api/client'
import { NodeIcon } from './NodeIcon'

export interface NodePlacementState {
  definition: NodeDefinition
  position: XYPosition
}

interface NodePlacementPreviewProps {
  state: NodePlacementState
}

export function NodePlacementPreview({ state }: NodePlacementPreviewProps) {
  return (
    <div
      className="node-placement-preview"
      style={{ transform: `translate(${state.position.x}px, ${state.position.y}px)` }}
      role="status"
      aria-live="polite"
    >
      <span className="node-placement-preview-heading">
        <NodeIcon category={state.definition.category} decorative />
        <strong>{state.definition.title}</strong>
      </span>
      <small>{state.definition.type}@{state.definition.version}</small>
      <span>点击画布放置，Esc 取消</span>
    </div>
  )
}
