import type { XYPosition } from '@xyflow/react'

import type { StudioNode } from './types'

export const NODE_PLACEMENT_GRID = 20
const COLLISION_X = 280
const COLLISION_Y = 170

export function availableNodePosition(
  center: XYPosition,
  nodes: StudioNode[],
): XYPosition {
  for (let step = 0; step <= nodes.length; step += 1) {
    const candidate = { x: center.x, y: center.y + step * 190 }
    if (!isOccupied(candidate, nodes)) return candidate
  }
  return { x: center.x, y: center.y + (nodes.length + 1) * 190 }
}

export function snapNodePosition(position: XYPosition): XYPosition {
  return {
    x: Math.round(position.x / NODE_PLACEMENT_GRID) * NODE_PLACEMENT_GRID,
    y: Math.round(position.y / NODE_PLACEMENT_GRID) * NODE_PLACEMENT_GRID,
  }
}

export function dropNodePosition(
  position: XYPosition,
  nodes: StudioNode[],
): XYPosition {
  const origin = snapNodePosition(position)
  if (!isOccupied(origin, nodes)) return origin

  const stepX =
    Math.ceil(COLLISION_X / NODE_PLACEMENT_GRID) * NODE_PLACEMENT_GRID
  const stepY =
    Math.ceil(COLLISION_Y / NODE_PLACEMENT_GRID) * NODE_PLACEMENT_GRID
  for (let ring = 1; ring <= nodes.length + 1; ring += 1) {
    const candidates = [
      { x: origin.x, y: origin.y + stepY * ring },
      { x: origin.x, y: origin.y - stepY * ring },
      { x: origin.x + stepX * ring, y: origin.y },
      { x: origin.x - stepX * ring, y: origin.y },
      { x: origin.x + stepX * ring, y: origin.y + stepY * ring },
      { x: origin.x - stepX * ring, y: origin.y + stepY * ring },
      { x: origin.x + stepX * ring, y: origin.y - stepY * ring },
      { x: origin.x - stepX * ring, y: origin.y - stepY * ring },
    ]
    const available = candidates.find(
      (candidate) => !isOccupied(candidate, nodes),
    )
    if (available) return available
  }
  return origin
}

export function safeBoundaryPosition(
  kind: 'start' | 'end',
  nodes: StudioNode[],
): XYPosition {
  if (nodes.length === 0) {
    return kind === 'start' ? { x: 120, y: 180 } : { x: 520, y: 180 }
  }
  const xValues = nodes.map((node) => node.position.x)
  const yValues = nodes.map((node) => node.position.y)
  const centerY = (Math.min(...yValues) + Math.max(...yValues)) / 2
  const position = {
    x:
      kind === 'start'
        ? Math.min(...xValues) - 320
        : Math.max(...xValues) + 320,
    y: centerY,
  }
  return dropNodePosition(position, nodes)
}

export function boundaryMidpoint(nodes: StudioNode[]): XYPosition | undefined {
  if (nodes.length !== 2) return undefined
  const startNodes = nodes.filter((node) => node.data.nodeType === 'start')
  const endNodes = nodes.filter((node) => node.data.nodeType === 'end')
  if (startNodes.length !== 1 || endNodes.length !== 1) return undefined
  return {
    x: (startNodes[0].position.x + endNodes[0].position.x) / 2,
    y: (startNodes[0].position.y + endNodes[0].position.y) / 2,
  }
}

function isOccupied(candidate: XYPosition, nodes: StudioNode[]) {
  return nodes.some(
    (node) =>
      Math.abs(node.position.x - candidate.x) < COLLISION_X &&
      Math.abs(node.position.y - candidate.y) < COLLISION_Y,
  )
}
