import { describe, expect, it } from 'vitest'

import type { ResolvedPorts } from '../../lib/api/client'
import type { StudioEdge, StudioNode } from './types'
import { applyNodeConfig, previewPorts } from './configDraft'

const port = (key: string) => ({ key, title: key, type: 'string' as const, required: false, cardinality: 'one' as const })
const node = (ports: ResolvedPorts): StudioNode => ({ id: 'node-a', type: 'studio', position: { x: 0, y: 0 }, data: { nodeType: 'dynamic', typeVersion: '1', config: { mode: 'old' }, ports, issues: [] } })
const edge: StudioEdge = { id: 'edge-old', source: 'node-a', sourceHandle: 'old', target: 'node-b', targetHandle: 'input', data: {} }

describe('节点配置端口变更', () => {
  it('预览新增删除端口和将失效的现有连线', () => {
    const current = node({ inputs: [], outputs: [port('old')] })
    const next = { inputs: [], outputs: [port('new')] }
    expect(previewPorts(current, [edge], next)).toEqual({ ports: next, added: ['output:new'], removed: ['output:old'], invalidEdgeIds: ['edge-old'] })
  })

  it('原子应用配置和端口且不修改任何输入容器', () => {
    const current = node({ inputs: [], outputs: [port('old')] })
    const nodes = [current]
    const edges = [edge]
    const config = { mode: 'new' }
    const ports = { inputs: [], outputs: [port('new')] }
    const before = structuredClone({ nodes, edges, config, ports })
    const applied = applyNodeConfig(nodes, edges, 'node-a', config, ports)
    expect(applied.nodes[0].data.config).toEqual({ mode: 'new' })
    expect(applied.nodes[0].data.ports.outputs[0].key).toBe('new')
    expect(applied.edges[0].data?.invalid).toBe(true)
    expect({ nodes, edges, config, ports }).toEqual(before)
  })
})
