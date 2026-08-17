import { Handle, Position, useUpdateNodeInternals, type NodeProps } from '@xyflow/react'
import { useEffect } from 'react'

import type { StudioNode } from './types'

export function GenericNode({ data, selected, id }: NodeProps<StudioNode>) {
  const updateNodeInternals = useUpdateNodeInternals()
  const portKey = JSON.stringify([
    ...(data.ports.inputs ?? []).map((port) => ['target', port.key]),
    ...(data.ports.outputs ?? []).map((port) => ['source', port.key]),
  ])
  useEffect(() => updateNodeInternals(id), [id, portKey, updateNodeInternals])
  return (
    <div className={`generic-node${selected ? ' selected' : ''}${data.issues.length ? ' invalid' : ''}`} data-testid={`node-${data.nodeType}`}>
      {(data.ports.inputs ?? []).map((port, index) => (
        <Handle
          key={`input-${port.key}`}
          id={port.key}
          type="target"
          position={Position.Left}
          style={{ top: `${((index + 1) / ((data.ports.inputs?.length ?? 0) + 1)) * 100}%` }}
          data-port={`${id}:${port.key}`}
          title={port.title}
        />
      ))}
      <span className="node-category">{data.definition?.category ?? data.nodeType}</span>
      <strong>{data.definition?.title ?? data.nodeType}</strong>
      <small>{data.nodeType}@{data.typeVersion}</small>
      {data.issues.length > 0 && <span className="node-error">{data.issues.length} 个问题</span>}
      {(data.ports.outputs ?? []).map((port, index) => (
        <Handle
          key={`output-${port.key}`}
          id={port.key}
          type="source"
          position={Position.Right}
          style={{ top: `${((index + 1) / ((data.ports.outputs?.length ?? 0) + 1)) * 100}%` }}
          data-port={`${id}:${port.key}`}
          title={port.title}
        />
      ))}
    </div>
  )
}
