import { useCallback, useState } from 'react'

export type WorkbenchMode = { kind: 'closed' } | { kind: 'config'; nodeId: string } | { kind: 'test' }
export type WorkbenchIntent = { kind: 'close' } | { kind: 'config'; nodeId: string } | { kind: 'test' } | { kind: 'open-library' } | { kind: 'publish' } | { kind: 'export' }
export interface StudioWorkbench { mode: WorkbenchMode; pendingIntent?: WorkbenchIntent; request: (intent: WorkbenchIntent, dirty: boolean) => void; resolveDirty: (choice: 'apply' | 'discard' | 'cancel') => WorkbenchIntent | undefined }

export function useStudioWorkbench(): StudioWorkbench {
  const [mode, setMode] = useState<WorkbenchMode>({ kind: 'closed' })
  const [pendingIntent, setPendingIntent] = useState<WorkbenchIntent>()
  const request = useCallback((intent: WorkbenchIntent, dirty: boolean) => {
    if (dirty && mode.kind === 'config') { setPendingIntent(intent); return }
    setPendingIntent(undefined)
    setMode(modeForIntent(intent, mode))
  }, [mode])
  const resolveDirty = useCallback((choice: 'apply' | 'discard' | 'cancel') => {
    const intent = pendingIntent
    if (choice === 'cancel') { setPendingIntent(undefined); return undefined }
    if (choice === 'discard' && intent) { setMode((current) => modeForIntent(intent, current)); setPendingIntent(undefined) }
    if (choice === 'apply') setPendingIntent(undefined)
    return intent
  }, [pendingIntent])
  return { mode, pendingIntent, request, resolveDirty }
}

function modeForIntent(intent: WorkbenchIntent, current: WorkbenchMode): WorkbenchMode {
  if (intent.kind === 'close') return { kind: 'closed' }
  if (intent.kind === 'config' || intent.kind === 'test') return intent
  return current
}
