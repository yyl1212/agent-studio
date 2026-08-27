import type { XYPosition } from '@xyflow/react'

import { Button } from '../../components/ui/Button'

export interface CanvasEmptyGuideProps {
  position: XYPosition
  onAdd: () => void
}

export function CanvasEmptyGuide(props: CanvasEmptyGuideProps) {
  return (
    <div
      className="canvas-empty-guide"
      style={{
        transform: `translate(${props.position.x}px, ${props.position.y}px)`,
      }}
    >
      <span aria-hidden="true">＋</span>
      <strong>构建你的第一步</strong>
      <small>在开始与结束之间添加一个处理节点</small>
      <Button
        className="nodrag nopan"
        variant="primary"
        onClick={props.onAdd}
      >
        在这里添加第一个节点
      </Button>
    </div>
  )
}
