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
    <div className={`generic-node${selected ? ' selected' : ''}${data.issues.length ? ' invalid' : ''}${data.debugCurrent ? ' debug-current' : ''}${data.debugStatus ? ` debug-${data.debugStatus}` : ''}`} data-testid={`node-${data.nodeType}`} data-node-id={id}>
      {(data.ports.inputs ?? []).map((port, index) => (
        <Handle
          key={`input-${port.key}`}
          id={port.key}
          type="target"
          position={Position.Left}
          style={{ top: `${((index + 1) / ((data.ports.inputs?.length ?? 0) + 1)) * 100}%` }}
          data-port={`${id}:${port.key}`}
          title={port.title}
          aria-label={`输入端口 ${port.title}`}
        />
      ))}
      <span className="node-category">{data.definition?.category ?? data.nodeType}</span>
      <strong>{data.definition?.title ?? data.nodeType}</strong>
      <small>{data.nodeType}@{data.typeVersion}</small>
      <span className="node-id">{id}</span>
      {data.issues.length > 0 && <span className="node-error">{data.issues.length} 个问题</span>}
	  {data.debugStatus && <span className="node-debug-status">{debugStatusLabel(data.debugStatus)}</span>}
      {(data.ports.outputs ?? []).map((port, index) => (
        <Handle
          key={`output-${port.key}`}
          id={port.key}
          type="source"
          position={Position.Right}
          style={{ top: `${((index + 1) / ((data.ports.outputs?.length ?? 0) + 1)) * 100}%` }}
          data-port={`${id}:${port.key}`}
          title={port.title}
          aria-label={`输出端口 ${port.title}`}
        />
      ))}
    </div>
  )
}

function debugStatusLabel(status: string) {
	return { pending: '○ 待执行', running: '◌ 运行中', completed: '✓ 已完成', failed: '× 失败', skipped: '↷ 已跳过', cancelled: '— 已取消' }[status] ?? status
}
