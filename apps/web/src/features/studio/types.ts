import type { Edge, Node } from '@xyflow/react'

import type { Graph, NodeDefinition, ResolvedPorts, ValidationIssue } from '../../lib/api/client'

export interface StudioNodeData extends Record<string, unknown> {
  nodeType: string
  typeVersion: string
  config: Record<string, unknown>
  definition?: NodeDefinition
  ports: ResolvedPorts
  issues: ValidationIssue[]
  debugStatus?: string
  debugCurrent?: boolean
  readOnly?: boolean
  boundary?: boolean
  invalidPortAnchors?: Array<{ direction: 'input' | 'output'; key: string }>
}

export type StudioNode = Node<StudioNodeData, 'studio'>
export type StudioEdge = Edge<{ invalid?: boolean }>

export interface FlowGraph {
  nodes: StudioNode[]
  edges: StudioEdge[]
}

export type { Graph }
