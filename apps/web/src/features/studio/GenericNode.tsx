import { Handle, Position, useUpdateNodeInternals, type NodeProps } from '@xyflow/react'
import { useEffect } from 'react'

import { NodeIcon } from './NodeIcon'
import type { StudioNode } from './types'

export function GenericNode({ data, selected, id }: NodeProps<StudioNode>) {
  const updateNodeInternals = useUpdateNodeInternals()
  const portKey = JSON.stringify([
    ...(data.ports.inputs ?? []).map((port) => ['target', port.key]),
    ...(data.ports.outputs ?? []).map((port) => ['source', port.key]),
  ])
  useEffect(() => updateNodeInternals(id), [id, portKey, updateNodeInternals])
  const category = data.definition?.category ?? data.nodeType
  const title = data.definition?.title ?? data.nodeType
  const statusLabels = [
    selected ? '已选中' : undefined,
    data.readOnly ? '只读' : undefined,
    data.issues.length ? `${data.issues.length} 个问题` : undefined,
    data.debugStatus ? debugStatusLabel(data.debugStatus) : undefined,
  ].filter((label): label is string => Boolean(label))
  return (
    <div
      className={`generic-node${selected ? ' selected' : ''}${data.issues.length ? ' invalid' : ''}${data.debugCurrent ? ' debug-current' : ''}${data.debugStatus ? ` debug-${data.debugStatus}` : ''}${data.readOnly ? ' read-only' : ''}${data.boundary ? ' boundary' : ''}`}
      data-testid={`node-${data.nodeType}`}
      data-node-id={id}
      data-selected={selected || undefined}
      data-invalid={data.issues.length > 0 || undefined}
      data-debug-status={data.debugStatus}
      data-read-only={data.readOnly || undefined}
      data-boundary={data.boundary || undefined}
    >
      <PortList id={id} direction="input" ports={data.ports.inputs ?? []} />
      <header className="node-card-header">
        <span className="node-card-icon"><NodeIcon category={category} decorative /></span>
        <span className="node-card-heading">
          <strong>{title}</strong>
          <small>{data.nodeType}@{data.typeVersion}</small>
        </span>
        {data.boundary && (
          <span className="node-boundary-lock" aria-label={`${data.nodeType === 'start' ? '开始' : '结束'}节点，不可删除`}>
            <span aria-hidden="true">🔒</span> 工作流唯一
          </span>
        )}
      </header>
      <div className="node-card-body">
        <span className="node-category">{category}</span>
        <p>{data.definition?.description || configSummary(data.config)}</p>
        <span className="node-id" title={id}>{id}</span>
      </div>
      <footer className="node-card-footer">
        <span>{(data.ports.inputs?.length ?? 0)} 入 · {(data.ports.outputs?.length ?? 0)} 出</span>
        {statusLabels.map((label) => (
          <span className="node-state-label" key={label}>{label}</span>
        ))}
      </footer>
      <PortList id={id} direction="output" ports={data.ports.outputs ?? []} />
    </div>
  )
}

function debugStatusLabel(status: string) {
	return { pending: '待执行', running: '运行中', completed: '已完成', failed: '失败', skipped: '已跳过', cancelled: '已取消' }[status] ?? status
}

interface PortLike {
  key: string
  title: string
  type: string
}

function PortList({ id, direction, ports }: { id: string; direction: 'input' | 'output'; ports: PortLike[] }) {
  const input = direction === 'input'
  return ports.map((port, index) => (
    <span
      className={`node-port-hit-area node-port-${direction}`}
      key={`${direction}-${port.key}`}
      style={{ top: `${((index + 1) / (ports.length + 1)) * 100}%` }}
    >
      <span className="node-port-label">{port.title} · {port.type}</span>
      <Handle
        id={port.key}
        type={input ? 'target' : 'source'}
        position={input ? Position.Left : Position.Right}
        data-port={`${id}:${port.key}`}
        title={`${port.title} · ${port.type}`}
        aria-label={`${input ? '输入' : '输出'}端口 ${port.title}，类型 ${port.type}`}
      />
    </span>
  ))
}

function configSummary(config: Record<string, unknown>) {
  const keys = Object.keys(config)
  return keys.length ? `已配置 ${keys.length} 项参数` : '尚未配置参数'
}
