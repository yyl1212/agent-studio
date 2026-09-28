import { useEffect, useRef } from 'react'

import { Button } from '../../components/ui/Button'

export interface NodePlacementActionsProps {
  nodeTitle: string
  onConfirm: () => void
  onCancel: () => void
}

export function NodePlacementActions({ nodeTitle, onConfirm, onCancel }: NodePlacementActionsProps) {
  const confirmRef = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    confirmRef.current?.focus()
  }, [])

  return <section className="studio-placement-actions" aria-label="节点放置操作">
    <div className="studio-placement-copy">
      <strong>{nodeTitle}</strong>
      <span>选择位置 → 确认放置</span>
    </div>
    <div className="studio-placement-buttons">
      <Button ref={confirmRef} variant="primary" onClick={onConfirm}>确认放置</Button>
      <Button onClick={onCancel}>取消</Button>
    </div>
  </section>
}
