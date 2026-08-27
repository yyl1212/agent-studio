import { describe, expect, it } from 'vitest'

import {
  availableNodePosition,
  dropNodePosition,
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
})
