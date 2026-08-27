import type { NodeChange, XYPosition } from '@xyflow/react'

import { safeBoundaryPosition } from './nodePlacement'
import type { FlowGraph, StudioEdge, StudioNode } from './types'

export type BoundaryKind = 'start' | 'end'

export interface BoundaryProblem {
  kind: BoundaryKind
  issue: 'missing' | 'duplicate'
  nodeIds: string[]
}

export interface BoundaryDiagnosis {
  healthy: boolean
  problems: BoundaryProblem[]
}

export interface ProtectedDeletion extends FlowGraph {
  skippedBoundaryNodeIds: string[]
  changed: boolean
}

export interface BoundaryRepairSelection {
  keepStartId?: string
  keepEndId?: string
}

export interface BoundaryRepairPlan extends FlowGraph {
  addedNodeIds: string[]
  removedNodeIds: string[]
  removedEdgeIds: string[]
}

export class BoundaryRepairSelectionError extends Error {
  constructor(message = '请选择要保留的工作流边界节点') {
    super(message)
    this.name = 'BoundaryRepairSelectionError'
  }
}

export function isBoundaryNode(node: Pick<StudioNode, 'data'>) {
  return node.data.nodeType === 'start' || node.data.nodeType === 'end'
}

export function diagnoseWorkflowBoundaries(
  nodes: StudioNode[],
): BoundaryDiagnosis {
  const problems: BoundaryProblem[] = []
  for (const kind of ['start', 'end'] as const) {
    const nodeIds = nodes
      .filter((node) => node.data.nodeType === kind)
      .map((node) => node.id)
    if (nodeIds.length === 0) {
      problems.push({ kind, issue: 'missing', nodeIds })
    } else if (nodeIds.length > 1) {
      problems.push({ kind, issue: 'duplicate', nodeIds })
    }
  }
  return { healthy: problems.length === 0, problems }
}

export function protectNodeChanges(
  changes: NodeChange<StudioNode>[],
  nodes: StudioNode[],
) {
  const boundaryIds = new Set(
    nodes.filter(isBoundaryNode).map((node) => node.id),
  )
  const skippedBoundaryNodeIds: string[] = []
  const protectedChanges = changes.filter((change) => {
    if (change.type !== 'remove' || !boundaryIds.has(change.id)) return true
    if (!skippedBoundaryNodeIds.includes(change.id)) {
      skippedBoundaryNodeIds.push(change.id)
    }
    return false
  })
  return { changes: protectedChanges, skippedBoundaryNodeIds }
}

export function protectGraphDelete(
  nodes: StudioNode[],
  edges: StudioEdge[],
  deletedNodes: StudioNode[],
  deletedEdges: StudioEdge[],
): ProtectedDeletion {
  const skippedBoundaryNodeIds = deletedNodes
    .filter(isBoundaryNode)
    .map((node) => node.id)
  const skippedBoundaryIds = new Set(skippedBoundaryNodeIds)
  const removedNodeIds = new Set(
    deletedNodes.filter((node) => !isBoundaryNode(node)).map((node) => node.id),
  )
  const requestedEdgeIds = new Set(deletedEdges.map((edge) => edge.id))

  const nextNodes = nodes.filter((node) => !removedNodeIds.has(node.id))
  const nextEdges = edges.filter((edge) => {
    if (removedNodeIds.has(edge.source) || removedNodeIds.has(edge.target)) {
      return false
    }
    if (!requestedEdgeIds.has(edge.id)) return true
    const touchesSkippedBoundary =
      skippedBoundaryIds.has(edge.source) || skippedBoundaryIds.has(edge.target)
    return touchesSkippedBoundary && edge.selected !== true
  })

  return {
    nodes: nextNodes,
    edges: nextEdges,
    skippedBoundaryNodeIds,
    changed:
      nextNodes.length !== nodes.length || nextEdges.length !== edges.length,
  }
}

export function buildBoundaryRepairPlan(
  nodes: StudioNode[],
  edges: StudioEdge[],
  selection: BoundaryRepairSelection,
  createBoundary: (
    kind: BoundaryKind,
    position: XYPosition,
  ) => StudioNode,
): BoundaryRepairPlan {
  const diagnosis = diagnoseWorkflowBoundaries(nodes)
  for (const problem of diagnosis.problems) {
    if (problem.issue !== 'duplicate') continue
    const keeper = keeperFor(problem.kind, selection)
    if (!keeper || !problem.nodeIds.includes(keeper)) {
      throw new BoundaryRepairSelectionError()
    }
  }

  const nextNodes = structuredClone(nodes)
  let nextEdges = structuredClone(edges)
  const addedNodeIds: string[] = []
  const removedNodeIds: string[] = []
  const removedEdgeIds: string[] = []

  for (const problem of diagnosis.problems) {
    if (problem.issue === 'missing') {
      const boundary = createBoundary(
        problem.kind,
        safeBoundaryPosition(problem.kind, nextNodes),
      )
      nextNodes.push(structuredClone(boundary))
      addedNodeIds.push(boundary.id)
      continue
    }

    const keeper = keeperFor(problem.kind, selection)
    const discarded = new Set(
      problem.nodeIds.filter((nodeId) => nodeId !== keeper),
    )
    removedNodeIds.push(...discarded)
    for (let index = nextNodes.length - 1; index >= 0; index -= 1) {
      if (discarded.has(nextNodes[index].id)) nextNodes.splice(index, 1)
    }
    nextEdges = nextEdges.filter((edge) => {
      if (!discarded.has(edge.source) && !discarded.has(edge.target)) {
        return true
      }
      removedEdgeIds.push(edge.id)
      return false
    })
  }

  return {
    nodes: nextNodes,
    edges: nextEdges,
    addedNodeIds: addedNodeIds.sort(),
    removedNodeIds: removedNodeIds.sort(),
    removedEdgeIds: removedEdgeIds.sort(),
  }
}

function keeperFor(
  kind: BoundaryKind,
  selection: BoundaryRepairSelection,
) {
  return kind === 'start' ? selection.keepStartId : selection.keepEndId
}
