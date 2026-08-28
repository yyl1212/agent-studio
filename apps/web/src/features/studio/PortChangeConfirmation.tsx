import { useEffect, useRef } from 'react'

import { Button } from '../../components/ui/Button'
import type { InvalidEdgeImpact } from './configDraft'

export interface PortChangeConfirmationProps {
  open: boolean
  nodeTitle: string
  removedPorts: string[]
  invalidEdges: InvalidEdgeImpact[]
  busy: boolean
  onConfirm: () => void | Promise<void>
  onCancel: () => void
}

export function PortChangeConfirmation({ open, nodeTitle, removedPorts, invalidEdges, busy, onConfirm, onCancel }: PortChangeConfirmationProps) {
  const confirmRef = useRef<HTMLButtonElement>(null)
  const returnFocus = useRef<HTMLElement | null>(null)

  useEffect(() => {
    if (open) {
      returnFocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
      confirmRef.current?.focus()
    } else if (returnFocus.current) {
      returnFocus.current.focus()
      returnFocus.current = null
    }
  }, [open])

  useEffect(() => {
    if (!open) return
    const handleEscape = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      event.preventDefault()
      event.stopPropagation()
      if (!busy) onCancel()
    }
    document.addEventListener('keydown', handleEscape, true)
    return () => document.removeEventListener('keydown', handleEscape, true)
  }, [busy, onCancel, open])

  if (!open) return null
  return (
    <div className="dialog-backdrop">
      <dialog open className="port-change-dialog" aria-labelledby="port-change-title" aria-describedby="port-change-description">
        <h3 id="port-change-title">确认端口变化</h3>
        <p id="port-change-description">“{nodeTitle}”的配置会移除端口并使现有连线失效。失效连线将保留为红色虚线，不会自动重连。</p>
        {removedPorts.length > 0 && <section><h4>将移除的端口</h4><ul>{removedPorts.map((port) => <li key={port}><code>{port}</code></li>)}</ul></section>}
        {invalidEdges.length > 0 && <section><h4>受影响的连线</h4><ul>{invalidEdges.map((edge) => <li key={edge.edgeId}><code>{edge.sourceNodeId}.{edge.sourcePort} → {edge.targetNodeId}.{edge.targetPort}</code></li>)}</ul></section>}
        <div className="dialog-actions">
          <Button ref={confirmRef} variant="primary" disabled={busy} aria-busy={busy} onClick={() => { void onConfirm() }}>确认应用</Button>
          <Button variant="ghost" disabled={busy} onClick={onCancel}>取消</Button>
        </div>
      </dialog>
    </div>
  )
}
