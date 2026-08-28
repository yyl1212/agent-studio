import { describe, expect, it } from 'vitest'

import type { ResolvedPorts } from '../../lib/api/client'
import type { StudioEdge, StudioNode } from './types'
import { applyNodeConfig, previewPorts } from './configDraft'
import { hasInvalidEdges } from './connections'

const port = (key: string) => ({ key, title: key, type: 'string' as const, required: false, cardinality: 'one' as const })
const node = (id: string, ports: ResolvedPorts): StudioNode => ({ id, type: 'studio', position: { x: 0, y: 0 }, data: { nodeType: 'dynamic', typeVersion: '1', config: { mode: 'old' }, ports, issues: [] } })
const outgoing: StudioEdge = { id: 'edge-old', source: 'node-a', sourceHandle: 'old', target: 'node-b', targetHandle: 'input', data: {} }
const secondOutgoing: StudioEdge = { id: 'edge-second', source: 'node-a', sourceHandle: 'old', target: 'node-c', targetHandle: 'input', data: {} }
const incoming: StudioEdge = { id: 'edge-in', source: 'node-b', sourceHandle: 'output', target: 'node-a', targetHandle: 'old', data: {} }

describe('节点配置端口变更', () => {
  it('预览返回完整失效边影响而不修改输入', () => {
    const current = node('node-a', { inputs: [], outputs: [port('old')] })
    const nextPorts: ResolvedPorts = { inputs: [], outputs: [] }
    const before = structuredClone({ node: current, edges: [outgoing] })
    expect(previewPorts(current, [outgoing], nextPorts).invalidEdges).toEqual([{
      edgeId: 'edge-old', sourceNodeId: 'node-a', sourcePort: 'old', targetNodeId: 'node-b', targetPort: 'input',
    }])
    expect({ node: current, edges: [outgoing] }).toEqual(before)
  })

  it('原子应用配置和端口且不修改任何输入容器', () => {
    const current = node('node-a', { inputs: [], outputs: [port('old')] })
    const target = node('node-b', { inputs: [port('input')], outputs: [] })
    const nodes = [current, target]
    const edges = [outgoing]
    const config = { mode: 'new' }
    const ports = { inputs: [], outputs: [port('new')] }
    const before = structuredClone({ nodes, edges, config, ports })
    const applied = applyNodeConfig(nodes, edges, 'node-a', config, ports)
    expect(applied.nodes[0].data.config).toEqual({ mode: 'new' })
    expect(applied.nodes[0].data.ports.outputs[0].key).toBe('new')
    expect(applied.edges[0].data?.invalid).toBe(true)
    expect({ nodes, edges, config, ports }).toEqual(before)
  })

  it('恢复端口后清除失效标记和内联样式', () => {
    const oldPorts: ResolvedPorts = { inputs: [], outputs: [port('old')] }
    const nodes = [node('node-a', oldPorts), node('node-b', { inputs: [port('input')], outputs: [] })]
    const invalid: StudioEdge = { ...outgoing, data: { invalid: true }, className: 'invalid', style: { stroke: '#d92d20', strokeDasharray: '5 4' } }
    const result = applyNodeConfig(nodes, [invalid], 'node-a', { mode: 'restored' }, oldPorts)
    expect(result.edges[0].data?.invalid).toBe(false)
    expect(result.edges[0].className).toBeUndefined()
    expect(result.edges[0].style).toBeUndefined()
    expect(hasInvalidEdges(result.edges)).toBe(false)
  })

  it.each([
    { name: '新增端口', current: { inputs: [], outputs: [port('old')] }, edges: [outgoing], after: { inputs: [], outputs: [port('old'), port('new')] }, impactCount: 0 },
    { name: '移除输入端口', current: { inputs: [port('old')], outputs: [] }, edges: [incoming], after: { inputs: [], outputs: [] }, impactCount: 1 },
    { name: '移除输出端口', current: { inputs: [], outputs: [port('old')] }, edges: [outgoing], after: { inputs: [], outputs: [] }, impactCount: 1 },
    { name: '同时影响多条边', current: { inputs: [], outputs: [port('old')] }, edges: [outgoing, secondOutgoing], after: { inputs: [], outputs: [] }, impactCount: 2 },
  ])('$name', ({ current: currentPorts, edges: relatedEdges, after, impactCount }) => {
    const current = node('node-a', currentPorts)
    const snapshot = structuredClone({ current, relatedEdges, after })
    expect(previewPorts(current, relatedEdges, after).invalidEdges).toHaveLength(impactCount)
    expect({ current, relatedEdges, after }).toEqual(snapshot)
  })
})
