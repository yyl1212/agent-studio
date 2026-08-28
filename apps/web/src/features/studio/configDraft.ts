import type { ResolvedPorts } from '../../lib/api/client'
import { attachInvalidPortAnchors, markInvalidEdges } from './connections'
import type { StudioEdge, StudioNode } from './types'

export interface InvalidEdgeImpact {
  edgeId: string
  sourceNodeId: string
  sourcePort: string
  targetNodeId: string
  targetPort: string
}

export interface PortPreview { ports: ResolvedPorts; added: string[]; removed: string[]; invalidEdges: InvalidEdgeImpact[] }

export function previewPorts(node: StudioNode, edges: StudioEdge[], ports: ResolvedPorts): PortPreview {
  const nextPorts = clonePorts(ports)
  const before = portKeys(node.data.ports)
  const after = portKeys(nextPorts)
  const updatedNode = { ...node, data: { ...node.data, ports: nextPorts } }
  const relatedNodes = [updatedNode]
  const invalidEdges = edges
    .filter((edge) => (edge.source === node.id || edge.target === node.id) && !edgeHasPorts(edge, relatedNodes))
    .map((edge) => ({
      edgeId: edge.id,
      sourceNodeId: edge.source,
      sourcePort: edge.sourceHandle ?? '',
      targetNodeId: edge.target,
      targetPort: edge.targetHandle ?? '',
    }))
  return {
    ports: nextPorts,
    added: [...after].filter((key) => !before.has(key)),
    removed: [...before].filter((key) => !after.has(key)),
    invalidEdges,
  }
}

export function applyNodeConfig(nodes: StudioNode[], edges: StudioEdge[], nodeId: string, config: Record<string, unknown>, ports: ResolvedPorts) {
  const nextNodes = nodes.map((item) => item.id === nodeId ? { ...item, data: { ...item.data, config: structuredClone(config), ports: clonePorts(ports) } } : item)
  const nextEdges = markInvalidEdges(nextNodes, edges)
  return { nodes: attachInvalidPortAnchors(nextNodes, nextEdges), edges: nextEdges }
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
