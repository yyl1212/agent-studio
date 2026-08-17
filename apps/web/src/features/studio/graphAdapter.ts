import type { Graph, NodeDefinition, ResolvedPorts } from '../../lib/api/client'
import type { FlowGraph, StudioEdge, StudioNode } from './types'

const emptyPorts: ResolvedPorts = { inputs: [], outputs: [] }

export function toFlowGraph(
  graph: Graph,
  definitions: NodeDefinition[] = [],
  resolved: Record<string, ResolvedPorts> = {},
): FlowGraph {
  const definitionMap = new Map(definitions.map((definition) => [`${definition.type}@${definition.version}`, definition]))
  return {
    nodes: graph.nodes.map((node): StudioNode => ({
      id: node.id,
      type: 'studio',
      position: { ...node.position },
      data: {
        nodeType: node.type,
        typeVersion: node.typeVersion,
        config: structuredClone(node.config),
        definition: definitionMap.get(`${node.type}@${node.typeVersion}`),
        ports: resolved[node.id] ?? portsFromDefinition(definitionMap.get(`${node.type}@${node.typeVersion}`)),
        issues: [],
      },
    })),
    edges: graph.edges.map((edge): StudioEdge => ({
      id: edge.id,
      source: edge.source,
      sourceHandle: edge.sourcePort,
      target: edge.target,
      targetHandle: edge.targetPort,
      data: {},
    })),
  }
}

export function fromFlowGraph(nodes: StudioNode[], edges: StudioEdge[]): Graph {
  return {
    schemaVersion: 1,
    nodes: nodes.map((node) => ({
      id: node.id,
      type: node.data.nodeType,
      typeVersion: node.data.typeVersion,
      position: { x: node.position.x, y: node.position.y },
      config: structuredClone(node.data.config),
    })),
    edges: edges.map((edge) => ({
      id: edge.id,
      source: edge.source,
      sourcePort: edge.sourceHandle ?? '',
      target: edge.target,
      targetPort: edge.targetHandle ?? '',
    })),
  }
}

export function portsFromDefinition(definition?: NodeDefinition): ResolvedPorts {
  if (!definition) return emptyPorts
  return { inputs: definition.inputs, outputs: definition.outputs }
}
