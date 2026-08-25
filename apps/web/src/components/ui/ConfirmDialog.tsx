import { useEffect, useRef } from 'react'

import { Button } from './Button'

export interface ConfirmDialogProps {
  open: boolean
  title: string
  description: string
  confirmLabel: string
  discardLabel?: string
  cancelLabel: string
  confirmDisabled?: boolean
  onConfirm: () => void
  onDiscard?: () => void
  onCancel: () => void
}

export function ConfirmDialog(props: ConfirmDialogProps) {
  const confirmRef = useRef<HTMLButtonElement>(null)
  const returnFocus = useRef<HTMLElement | null>(null)

  useEffect(() => {
    if (props.open) {
      returnFocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
      confirmRef.current?.focus()
    } else if (returnFocus.current) {
      returnFocus.current.focus()
      returnFocus.current = null
    }
  }, [props.open])

  if (!props.open) return null
  return (
    <div className="dialog-backdrop" data-testid="dialog-backdrop">
      <dialog open aria-labelledby="confirm-dialog-title" aria-describedby="confirm-dialog-description">
        <h3 id="confirm-dialog-title">{props.title}</h3>
        <p id="confirm-dialog-description">{props.description}</p>
        <div className="dialog-actions">
          <Button ref={confirmRef} variant="primary" disabled={props.confirmDisabled} onClick={props.onConfirm}>{props.confirmLabel}</Button>
          {props.discardLabel && props.onDiscard && <Button variant="danger" onClick={props.onDiscard}>{props.discardLabel}</Button>}
          <Button variant="ghost" onClick={props.onCancel}>{props.cancelLabel}</Button>
        </div>
      </dialog>
    </div>
  )
}
