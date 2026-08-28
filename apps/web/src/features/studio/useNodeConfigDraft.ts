import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { validateFormValue, type FormValidation } from '../../components/schema-form/SchemaForm'
import type { JSONSchema } from '../../components/schema-form/types'
import type { ResolvedPorts } from '../../lib/api/client'
import { previewPorts, type PortPreview } from './configDraft'
import type { StudioEdge, StudioNode } from './types'

export type ResolveNodePorts = (type: string, version: string, config: Record<string, unknown>, signal: AbortSignal) => Promise<ResolvedPorts>
export type NodeConfigStatus = 'idle' | 'dirty' | 'resolving' | 'ready' | 'error'
export interface UseNodeConfigDraftOptions { node?: StudioNode; edges: StudioEdge[]; resolve: ResolveNodePorts; debounceMs?: number }
export interface UseNodeConfigDraftResult {
  nodeId?: string
  draft: Record<string, unknown>
  normalized?: Record<string, unknown>
  validation: FormValidation
  dirty: boolean
  status: NodeConfigStatus
  errorKind?: 'validation' | 'resolve'
  preview?: PortPreview
  error: string
  setDraft: (config: Record<string, unknown>) => void
  reset: () => void
  retry: () => void
  markApplied: (config: Record<string, unknown>, ports: ResolvedPorts) => void
}

interface DraftValue { nodeId?: string; draft: Record<string, unknown>; base: Record<string, unknown> }

export function useNodeConfigDraft({ node, edges, resolve, debounceMs = 300 }: UseNodeConfigDraftOptions): UseNodeConfigDraftResult {
  const initial = () => valueForNode(node)
  const [value, setValue] = useState<DraftValue>(initial)
  const [resolution, setResolution] = useState<{ status: 'idle'|'resolving'|'ready'|'error'; preview?: PortPreview; error: string }>({ status: 'idle', error: '' })
  const [retrySerial, setRetrySerial] = useState(0)
  const generation = useRef(0)
  useEffect(() => { generation.current += 1; setValue(valueForNode(node)); setResolution({ status: 'idle', error: '' }) }, [node?.id])
  const schema = node?.data.definition?.configSchema as JSONSchema | undefined
  const validation = useMemo<FormValidation>(() => schema && value.nodeId === node?.id
    ? validateFormValue(schema, value.draft)
    : { normalized: {}, errors: {}, valid: true }, [node?.id, schema, value])
  const dirty = JSON.stringify(value.draft) !== JSON.stringify(value.base)
  const edgeKey = JSON.stringify(edges.map((edge) => [edge.id, edge.source, edge.sourceHandle, edge.target, edge.targetHandle]))
  const portKey = JSON.stringify(node ? [node.id, node.data.ports] : null)

  useEffect(() => {
    if (!node || value.nodeId !== node.id || !dirty || !validation.valid) {
      setResolution((current) => current.status === 'idle' && !current.preview && current.error === '' ? current : { status: 'idle', error: '' })
      return
    }
    const controller = new AbortController()
    const currentGeneration = ++generation.current
    const timer = window.setTimeout(() => {
      setResolution({ status: 'resolving', error: '' })
      resolve(node.data.nodeType, node.data.typeVersion, validation.normalized, controller.signal).then((ports) => {
        if (!controller.signal.aborted && generation.current === currentGeneration) setResolution({ status: 'ready', preview: previewPorts(node, edges, ports), error: '' })
      }).catch((error: unknown) => {
        if (!controller.signal.aborted && generation.current === currentGeneration) setResolution({ status: 'error', error: error instanceof Error ? error.message : '端口解析失败' })
      })
    }, debounceMs)
    return () => { window.clearTimeout(timer); controller.abort() }
  }, [debounceMs, dirty, edgeKey, node?.id, node?.data.nodeType, node?.data.typeVersion, portKey, resolve, retrySerial, validation, value.nodeId])

  const reset = useCallback(() => { generation.current += 1; setValue(valueForNode(node)); setResolution({ status: 'idle', error: '' }) }, [node])
  const retry = useCallback(() => setRetrySerial((value) => value + 1), [])
  const markApplied = useCallback((config: Record<string, unknown>, ports: ResolvedPorts) => {
    generation.current += 1
    const copy = structuredClone(config)
    setValue({ nodeId: node?.id, draft: copy, base: structuredClone(copy) })
    if (node) setResolution({ status: 'ready', preview: previewPorts(node, edges, ports), error: '' })
  }, [edges, node])
  const status: NodeConfigStatus = !dirty
    ? 'idle'
    : !validation.valid
      ? 'error'
      : resolution.status === 'idle'
        ? 'dirty'
        : resolution.status
  const errorKind = dirty && !validation.valid
    ? 'validation' as const
    : resolution.status === 'error' ? 'resolve' as const : undefined
  return {
    nodeId: value.nodeId,
    draft: value.draft,
    normalized: validation.valid ? validation.normalized : undefined,
    validation,
    dirty,
    status,
    errorKind,
    preview: resolution.preview,
    error: resolution.error,
    setDraft: (draft) => setValue((current) => ({ ...current, draft: structuredClone(draft) })),
    reset,
    retry,
    markApplied,
  }
}

function valueForNode(node?: StudioNode): DraftValue {
  const config = structuredClone(node?.data.config ?? {})
  return { nodeId: node?.id, draft: config, base: structuredClone(config) }
}
