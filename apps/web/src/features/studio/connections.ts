import type { Connection } from '@xyflow/react'

import type { StudioEdge, StudioNode } from './types'

const invalidEdgeStyle = { stroke: '#d92d20', strokeDasharray: '5 4' }

export function validateConnection(connection: Connection | StudioEdge, nodes: StudioNode[], edges: StudioEdge[]) {
  return connectionIssue(connection, nodes, edges) === undefined
}

export function connectionIssue(connection: Connection | StudioEdge, nodes: StudioNode[], edges: StudioEdge[]) {
  if (!connection.source || !connection.target || !connection.sourceHandle || !connection.targetHandle) return '请连接有效的输入和输出端口'
  if (connection.source === connection.target) return '节点不能连接到自身'
  const source = nodes.find((node) => node.id === connection.source)
  const target = nodes.find((node) => node.id === connection.target)
  const output = source?.data.ports.outputs.find((port) => port.key === connection.sourceHandle)
  const input = target?.data.ports.inputs.find((port) => port.key === connection.targetHandle)
  if (!output || !input) return '端口不存在或已变化'
  if (output.type !== 'any' && input.type !== 'any' && output.type !== input.type) return `端口类型不兼容：${output.type} 不能连接到 ${input.type}`
  const connectionID = 'id' in connection ? connection.id : undefined
  if (input.cardinality === 'one' && edges.some((edge) => edge.id !== connectionID && edge.target === connection.target && edge.targetHandle === connection.targetHandle)) return '目标端口只允许一条输入连线'
  return undefined
}

export function markInvalidEdges(nodes: StudioNode[], edges: StudioEdge[]): StudioEdge[] {
  return edges.map((edge) => {
    const invalid = !validateConnection(edge, nodes, edges)
    return {
      ...edge,
      data: { ...edge.data, invalid },
      className: invalid ? 'invalid' : undefined,
      style: invalid ? invalidEdgeStyle : undefined,
    }
  })
}

export function hasInvalidEdges(edges: StudioEdge[]) {
  return edges.some((edge) => edge.data?.invalid === true)
}
