import type { ResolvedPorts } from '../../lib/api/client'
import type { StudioEdge, StudioNode } from './types'

export interface PortPreview { ports: ResolvedPorts; added: string[]; removed: string[]; invalidEdgeIds: string[] }

export function previewPorts(node: StudioNode, edges: StudioEdge[], ports: ResolvedPorts): PortPreview {
  const nextPorts = clonePorts(ports)
  const before = portKeys(node.data.ports)
  const after = portKeys(nextPorts)
  const updatedNode = { ...node, data: { ...node.data, ports: nextPorts } }
  const relatedNodes = [updatedNode]
  const invalidEdgeIds = edges.filter((edge) => (edge.source === node.id || edge.target === node.id) && !edgeHasPorts(edge, relatedNodes)).map((edge) => edge.id)
  return {
    ports: nextPorts,
    added: [...after].filter((key) => !before.has(key)),
    removed: [...before].filter((key) => !after.has(key)),
    invalidEdgeIds,
  }
}

export function applyNodeConfig(nodes: StudioNode[], edges: StudioEdge[], nodeId: string, config: Record<string, unknown>, ports: ResolvedPorts) {
  const nextNodes = nodes.map((item) => item.id === nodeId ? { ...item, data: { ...item.data, config: structuredClone(config), ports: clonePorts(ports) } } : item)
  const nextEdges = edges.map((edge) => {
    const invalid = !edgeIsValid(edge, nextNodes, edges)
    return { ...edge, data: { ...edge.data, invalid }, style: invalid ? { stroke: '#b42318', strokeDasharray: '5 4' } : undefined }
  })
  return { nodes: nextNodes, edges: nextEdges }
}

function portKeys(ports: ResolvedPorts) {
  return new Set([...ports.inputs.map((port) => `input:${port.key}`), ...ports.outputs.map((port) => `output:${port.key}`)])
}

function clonePorts(ports: ResolvedPorts): ResolvedPorts { return { inputs: structuredClone(ports.inputs), outputs: structuredClone(ports.outputs) } }

function edgeHasPorts(edge: StudioEdge, nodes: StudioNode[]) {
  const source = nodes.find((item) => item.id === edge.source)
  const target = nodes.find((item) => item.id === edge.target)
  if (source && !source.data.ports.outputs.some((port) => port.key === edge.sourceHandle)) return false
  if (target && !target.data.ports.inputs.some((port) => port.key === edge.targetHandle)) return false
  return true
}

function edgeIsValid(edge: StudioEdge, nodes: StudioNode[], edges: StudioEdge[]) {
  if (!edge.sourceHandle || !edge.targetHandle || edge.source === edge.target) return false
  const source = nodes.find((item) => item.id === edge.source)
  const target = nodes.find((item) => item.id === edge.target)
  const output = source?.data.ports.outputs.find((port) => port.key === edge.sourceHandle)
  const input = target?.data.ports.inputs.find((port) => port.key === edge.targetHandle)
  if (!output || !input || (output.type !== 'any' && input.type !== 'any' && output.type !== input.type)) return false
  return input.cardinality !== 'one' || !edges.some((item) => item.id !== edge.id && item.target === edge.target && item.targetHandle === edge.targetHandle)
}
