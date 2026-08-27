import { useEffect } from 'react'

interface StudioShortcutOptions {
  onOpenNodeLibrary: () => void
  onEscape: () => void
}

export function isEditableTarget(target: EventTarget | null) {
  return target instanceof HTMLElement && Boolean(target.closest('input, textarea, select, [contenteditable="true"]'))
}

export function useStudioShortcuts({ onOpenNodeLibrary, onEscape }: StudioShortcutOptions) {
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.isComposing || isEditableTarget(event.target)) return
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        onOpenNodeLibrary()
      } else if (event.key === 'Escape') {
        onEscape()
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [onEscape, onOpenNodeLibrary])
}
