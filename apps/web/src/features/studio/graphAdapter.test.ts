import { describe, expect, it } from 'vitest'

import { fromFlowGraph, toFlowGraph } from './graphAdapter'

describe('graphAdapter', () => {
  it('往返保留节点版本、配置、位置和端口连线', () => {
    const graph = {
      schemaVersion: 1 as const,
      nodes: [
        { id: 'a', type: 'template', typeVersion: '1', position: { x: 10, y: 20 }, config: { template: '{{topic}}' } },
        { id: 'b', type: 'end', typeVersion: '1', position: { x: 30, y: 40 }, config: {} },
      ],
      edges: [{ id: 'e1', source: 'a', sourcePort: 'text', target: 'b', targetPort: 'result' }],
    }
    const flow = toFlowGraph(graph)
    expect(flow.edges[0]).toMatchObject({ sourceHandle: 'text', targetHandle: 'result' })
    expect(fromFlowGraph(flow.nodes, flow.edges)).toEqual(graph)
  })
})
