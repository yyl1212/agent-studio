import { describe, expect, it } from 'vitest'

import { attachInvalidPortAnchors, connectionIssue, hasInvalidEdges, markInvalidEdges, validateConnection } from './connections'
import type { StudioEdge, StudioNode } from './types'

function node(id: string, outputType: 'string' | 'number' | 'any', inputType: 'string' | 'number' | 'any'): StudioNode {
  return {
    id, type: 'studio', position: { x: 0, y: 0 },
    data: {
      nodeType: id, typeVersion: '1', config: {}, issues: [],
      ports: {
        inputs: [{ key: 'in', title: '输入', type: inputType, required: true, cardinality: 'one' }],
        outputs: [{ key: 'out', title: '输出', type: outputType, required: false, cardinality: 'one' }],
      },
    },
  }
}

describe('画布连线校验', () => {
  it('拒绝类型不兼容和普通端口的第二条入边', () => {
    const nodes = [node('string', 'string', 'any'), node('number', 'number', 'number')]
    expect(validateConnection({ source: 'string', sourceHandle: 'out', target: 'number', targetHandle: 'in' }, nodes, [])).toBe(false)
    const existing: StudioEdge[] = [{ id: 'e1', source: 'number', sourceHandle: 'out', target: 'string', targetHandle: 'in' }]
    expect(validateConnection({ source: 'number', sourceHandle: 'out', target: 'string', targetHandle: 'in' }, nodes, existing)).toBe(false)
  })

  it('动态 handle 消失后保留边并标红', () => {
    const nodes = [node('source', 'string', 'any'), node('target', 'any', 'string')]
    const edge: StudioEdge = { id: 'e1', source: 'source', sourceHandle: 'missing', target: 'target', targetHandle: 'in' }
    const [marked] = markInvalidEdges(nodes, [edge])
    expect(marked.id).toBe('e1')
    expect(marked.data?.invalid).toBe(true)
    expect(marked.className).toBe('invalid')
    expect(marked.style).toMatchObject({ stroke: '#d92d20' })
    expect(hasInvalidEdges([marked])).toBe(true)
    const anchored = attachInvalidPortAnchors(nodes, [marked])
    expect(anchored.find((item) => item.id === 'source')?.data.invalidPortAnchors).toEqual([{ direction: 'output', key: 'missing' }])
    expect(anchored.find((item) => item.id === 'target')?.data.invalidPortAnchors).toEqual([])
  })

  it('恢复有效端口时清除失效标记、类名和样式', () => {
    const nodes = [node('source', 'string', 'any'), node('target', 'any', 'string')]
    const edge: StudioEdge = {
      id: 'e1', source: 'source', sourceHandle: 'out', target: 'target', targetHandle: 'in',
      data: { invalid: true }, className: 'invalid', style: { stroke: '#d92d20', strokeDasharray: '5 4' },
    }
    const [marked] = markInvalidEdges(nodes, [edge])
    expect(connectionIssue(marked, nodes, [marked])).toBeUndefined()
    expect(marked.data?.invalid).toBe(false)
    expect(marked.className).toBeUndefined()
    expect(marked.style).toBeUndefined()
    expect(hasInvalidEdges([marked])).toBe(false)
  })
})
