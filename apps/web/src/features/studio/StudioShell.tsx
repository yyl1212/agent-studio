import { useEffect, useRef, type ReactNode, type RefObject } from 'react'

import { useStudioShortcuts } from './useStudioShortcuts'

export type StudioLayer = 'none' | 'commands' | 'shortcuts' | 'library' | 'workbench'

export interface StudioShellProps {
  layer: StudioLayer
  commandBar: ReactNode
  quickTools: ReactNode
  canvas: ReactNode
  nodeLibrary?: ReactNode
  workbench?: ReactNode
  onOpenNodeLibrary: () => void
  onRequestCloseTopLayer: () => void
  returnFocusRef?: RefObject<HTMLElement | null>
}

export function StudioShell(props: StudioShellProps) {
  const previousLayer = useRef(props.layer)
  useStudioShortcuts({
    onOpenNodeLibrary: props.onOpenNodeLibrary,
    onEscape: () => {
      if (props.layer !== 'none') props.onRequestCloseTopLayer()
    },
  })
  useEffect(() => {
    if (previousLayer.current !== 'none' && props.layer === 'none') props.returnFocusRef?.current?.focus()
    previousLayer.current = props.layer
  }, [props.layer, props.returnFocusRef])

  return <section className="studio-shell">
    <div className="studio-shell-canvas">{props.canvas}</div>
    <div className="studio-shell-command">{props.commandBar}</div>
    <div className="studio-shell-tools">{props.quickTools}</div>
    {props.nodeLibrary && <div className="studio-shell-library">{props.nodeLibrary}</div>}
    {props.workbench && <div className="studio-shell-workbench">{props.workbench}</div>}
  </section>
}
