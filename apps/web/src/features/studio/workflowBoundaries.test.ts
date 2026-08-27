import type { XYPosition } from '@xyflow/react'
import { describe, expect, it } from 'vitest'

import type { StudioEdge, StudioNode } from './types'
import {
  BoundaryRepairSelectionError,
  buildBoundaryRepairPlan,
  diagnoseWorkflowBoundaries,
  isBoundaryNode,
  protectGraphDelete,
  protectNodeChanges,
  type BoundaryKind,
} from './workflowBoundaries'

const studioNode = (
  id: string,
  nodeType: string,
  position = { x: 0, y: 0 },
): StudioNode => ({
  id,
  type: 'studio',
  position,
  data: {
    nodeType,
    typeVersion: '1',
    config: {},
    ports: { inputs: [], outputs: [] },
    issues: [],
  },
})

const studioEdge = (
  id: string,
  source: string,
  target: string,
): StudioEdge => ({
  id,
  source,
  target,
  sourceHandle: 'out',
  targetHandle: 'in',
  data: {},
})

const createBoundary = (kind: BoundaryKind, position: XYPosition) =>
  studioNode(`${kind}-new`, kind, position)

describe('workflow boundaries', () => {
  it('只按领域类型识别开始和结束节点', () => {
    expect(isBoundaryNode(studioNode('renamed', 'start'))).toBe(true)
    expect(isBoundaryNode(studioNode('start', 'template'))).toBe(false)
  })

  it.each([
    [['start', 'end'], []],
    [['end'], [['start', 'missing']]],
    [['start'], [['end', 'missing']]],
    [[], [['start', 'missing'], ['end', 'missing']]],
    [['start', 'start', 'end'], [['start', 'duplicate']]],
    [['start', 'end', 'end'], [['end', 'duplicate']]],
  ] as const)('诊断 %j', (types, expected) => {
    const diagnosis = diagnoseWorkflowBoundaries(
      types.map((type, index) => studioNode(`${type}-${index}`, type)),
    )
    expect(
      diagnosis.problems.map((problem) => [problem.kind, problem.issue]),
    ).toEqual(expected)
    expect(diagnosis.healthy).toBe(expected.length === 0)
  })

  it('过滤 React Flow remove change，避免 onDelete 前先移除边界', () => {
    const start = studioNode('start', 'start')
    const task = studioNode('task', 'template')
    const result = protectNodeChanges(
      [
        { type: 'remove', id: 'start' },
        { type: 'remove', id: 'task' },
      ],
      [start, task],
    )
    expect(result.changes).toEqual([{ type: 'remove', id: 'task' }])
    expect(result.skippedBoundaryNodeIds).toEqual(['start'])
  })

  it('混合删除跳过边界并删除普通节点及其关联边', () => {
    const start = studioNode('start', 'start')
    const task = studioNode('task', 'template')
    const end = studioNode('end', 'end')
    const startTask = studioEdge('start-task', 'start', 'task')
    const taskEnd = studioEdge('task-end', 'task', 'end')
    const nodes = [start, task, end]
    const edges = [startTask, taskEnd]
    const before = structuredClone({ nodes, edges })

    const result = protectGraphDelete(
      nodes,
      edges,
      [start, task],
      [startTask, taskEnd],
    )

    expect(result.nodes.map((node) => node.id)).toEqual(['start', 'end'])
    expect(result.edges).toEqual([])
    expect(result.skippedBoundaryNodeIds).toEqual(['start'])
    expect(result.changed).toBe(true)
    expect({ nodes, edges }).toEqual(before)
  })

  it('只删除边界时保留边界和自动关联边', () => {
    const start = studioNode('start', 'start')
    const task = studioNode('task', 'template')
    const edge = studioEdge('start-task', 'start', 'task')
    expect(
      protectGraphDelete(
        [start, task],
        [edge],
        [start],
        [{ ...edge, selected: false }],
      ),
    ).toEqual({
      nodes: [start, task],
      edges: [edge],
      skippedBoundaryNodeIds: ['start'],
      changed: false,
    })
  })

  it('允许同时显式选中的边从受保护边界上删除', () => {
    const start = studioNode('start', 'start')
    const task = studioNode('task', 'template')
    const edge = { ...studioEdge('start-task', 'start', 'task'), selected: true }
    const result = protectGraphDelete(
      [start, task],
      [edge],
      [start],
      [edge],
    )
    expect(result.nodes).toEqual([start, task])
    expect(result.edges).toEqual([])
    expect(result.changed).toBe(true)
  })

  it('重复修复要求有效 keeper，并只移除其他节点及其关联边', () => {
    const nodes = [
      studioNode('start-a', 'start'),
      studioNode('start-b', 'start'),
      studioNode('end', 'end'),
    ]
    const edges = [
      studioEdge('a-end', 'start-a', 'end'),
      studioEdge('b-end', 'start-b', 'end'),
    ]
    expect(() =>
      buildBoundaryRepairPlan(nodes, edges, {}, createBoundary),
    ).toThrow(BoundaryRepairSelectionError)
    expect(() =>
      buildBoundaryRepairPlan(
        nodes,
        edges,
        { keepStartId: 'missing' },
        createBoundary,
      ),
    ).toThrow(BoundaryRepairSelectionError)

    const before = structuredClone({ nodes, edges })
    const plan = buildBoundaryRepairPlan(
      nodes,
      edges,
      { keepStartId: 'start-a' },
      createBoundary,
    )
    expect(plan.removedNodeIds).toEqual(['start-b'])
    expect(plan.removedEdgeIds).toEqual(['b-end'])
    expect(plan.edges.map((edge) => edge.id)).toEqual(['a-end'])
    expect({ nodes, edges }).toEqual(before)
  })

  it('缺失边界生成不连线节点并报告稳定影响列表', () => {
    const task = studioNode('task', 'template', { x: 120, y: 180 })
    const plan = buildBoundaryRepairPlan([task], [], {}, createBoundary)
    expect(plan.nodes.map((node) => node.data.nodeType)).toEqual([
      'template',
      'start',
      'end',
    ])
    expect(plan.addedNodeIds).toEqual(['end-new', 'start-new'])
    expect(plan.removedNodeIds).toEqual([])
    expect(plan.removedEdgeIds).toEqual([])
    expect(plan.edges).toEqual([])
    expect(plan.nodes[1].position).not.toEqual(task.position)
    expect(plan.nodes[2].position).not.toEqual(task.position)
  })
})
