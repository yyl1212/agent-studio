import { useCallback, useEffect, useRef, useState, type CSSProperties, type PointerEvent as ReactPointerEvent, type ReactNode } from 'react'

import { Button } from '../../components/ui/Button'

const storageKey = 'agent-studio.workbench-width'
const minimumWidth = 320
const maximumWidth = 480
const defaultWidth = 400

interface WorkbenchPanelProps { titleId: string; onRequestClose: () => void; children: ReactNode }

export function WorkbenchPanel({ titleId, onRequestClose, children }: WorkbenchPanelProps) {
  const [width, setWidth] = useState(readWidth)
  const drag = useRef<{ startX: number; startWidth: number } | undefined>(undefined)
  const updateWidth = useCallback((value: number) => {
    const next = clampWidth(value)
    setWidth(next)
    try { window.localStorage.setItem(storageKey, String(next)) } catch { /* preference storage is optional */ }
  }, [])
  useEffect(() => {
    const move = (event: PointerEvent) => { if (drag.current) updateWidth(drag.current.startWidth + drag.current.startX - event.clientX) }
    const stop = () => { drag.current = undefined }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', stop)
    return () => { window.removeEventListener('pointermove', move); window.removeEventListener('pointerup', stop) }
  }, [updateWidth])
  const style = { '--as-workbench-width': `${width}px` } as CSSProperties
  return <aside className="workbench-panel" aria-labelledby={titleId} style={style}>
    <div className="workbench-resizer" role="separator" aria-label="调整工作台宽度" aria-orientation="vertical" aria-valuemin={minimumWidth} aria-valuemax={maximumWidth} aria-valuenow={width} tabIndex={0} onPointerDown={(event: ReactPointerEvent) => { drag.current = { startX: event.clientX, startWidth: width } }} onKeyDown={(event) => {
      if (event.key === 'ArrowLeft') { event.preventDefault(); updateWidth(width - 16) }
      if (event.key === 'ArrowRight') { event.preventDefault(); updateWidth(width + 16) }
    }} />
    <Button className="workbench-close" variant="ghost" aria-label="关闭工作台" onClick={onRequestClose}>×</Button>
    {children}
  </aside>
}

function readWidth() { try { return clampWidth(Number(window.localStorage.getItem(storageKey) || defaultWidth)) } catch { return defaultWidth } }
function clampWidth(value: number) { return Number.isFinite(value) ? Math.min(maximumWidth, Math.max(minimumWidth, Math.round(value))) : defaultWidth }
