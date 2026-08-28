import { describe, expect, it } from 'vitest'

import {
  availableNodePosition,
  boundaryMidpoint,
  dropNodePosition,
  previewNodePosition,
  safeBoundaryPosition,
  snapNodePosition,
} from './nodePlacement'
import type { StudioNode } from './types'

const nodeAt = (x: number, y: number): StudioNode => ({
  id: `${x}-${y}`,
  type: 'studio',
  position: { x, y },
  data: {
    nodeType: 'template',
    typeVersion: '1',
    config: {},
    ports: { inputs: [], outputs: [] },
    issues: [],
  },
})

describe('nodePlacement', () => {
  it('保持点击添加按 190px 向下寻找空位的现有行为', () => {
    expect(
      availableNodePosition({ x: 320, y: 260 }, [nodeAt(320, 260)]),
    ).toEqual({ x: 320, y: 450 })
  })

  it('把自由拖放位置吸附到 20px 网格且单轴误差不超过 10px', () => {
    expect(snapNodePosition({ x: 111, y: 249 })).toEqual({ x: 120, y: 240 })
    expect(dropNodePosition({ x: 111, y: 249 }, [])).toEqual({ x: 120, y: 240 })
  })

  it('完全重叠时按确定性顺序选择最近的非重叠网格', () => {
    expect(dropNodePosition({ x: 120, y: 240 }, [nodeAt(120, 240)])).toEqual({
      x: 120,
      y: 420,
    })
    expect(
      dropNodePosition(
        { x: 120, y: 240 },
        [nodeAt(120, 240), nodeAt(120, 420)],
      ),
    ).toEqual({ x: 120, y: 60 })
  })

  it('预览移动吸附网格且避开完全覆盖', () => {
    const occupied = nodeAt(100, 100)

    expect(previewNodePosition({ x: 109, y: 94 }, [occupied])).toEqual(
      dropNodePosition({ x: 109, y: 94 }, [occupied]),
    )
  })

  it('边界安全位置位于图包围盒外并避开现有节点', () => {
    const nodes = [nodeAt(120, 180), nodeAt(520, 180)]
    const start = safeBoundaryPosition('start', nodes)
    const end = safeBoundaryPosition('end', nodes)
    expect(start.x).toBeLessThan(120)
    expect(end.x).toBeGreaterThan(520)
    expect(start).not.toEqual(nodes[0].position)
    expect(end).not.toEqual(nodes[1].position)
  })

  it('只有唯一开始和结束节点时返回空图引导中点', () => {
    const start = {
      ...nodeAt(100, 100),
      id: 'start',
      data: { ...nodeAt(100, 100).data, nodeType: 'start' },
    }
    const end = {
      ...nodeAt(500, 100),
      id: 'end',
      data: { ...nodeAt(500, 100).data, nodeType: 'end' },
    }
    expect(boundaryMidpoint([start, end])).toEqual({ x: 300, y: 100 })
    expect(boundaryMidpoint([start, nodeAt(300, 100), end])).toBeUndefined()
    expect(boundaryMidpoint([start])).toBeUndefined()
  })
})
