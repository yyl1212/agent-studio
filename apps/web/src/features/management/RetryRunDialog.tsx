import { useEffect, useMemo, useRef, useState } from 'react'

import { SchemaForm } from '../../components/schema-form/SchemaForm'
import type { FormValue, JSONSchema } from '../../components/schema-form/types'
import { APIError, api, type RunRetryPreview } from '../../lib/api/client'
import { readNDJSON } from '../../lib/api/ndjson'
import { WorkbenchPanel } from '../studio/WorkbenchPanel'

interface RetryRunDialogProps {
  sourceRunID: string
  onRequestClose: () => void
  onRetryCreated: (runID: string) => void
}

export function RetryRunDialog({ sourceRunID, onRequestClose, onRetryCreated }: RetryRunDialogProps) {
  const [preview, setPreview] = useState<RunRetryPreview>()
  const [secretValues, setSecretValues] = useState<Record<string, unknown>>({})
  const [loading, setLoading] = useState(true)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const idempotencyKey = useRef<string | undefined>(undefined)
  const controller = useRef<AbortController | undefined>(undefined)

  useEffect(() => {
    const loadController = new AbortController()
    setLoading(true)
    setError('')
    api.previewRunRetry(sourceRunID, loadController.signal).then((loaded) => {
      if (!loadController.signal.aborted) setPreview(loaded)
    }).catch((cause: unknown) => {
      if (!loadController.signal.aborted) setError(publicError(cause, '加载重试预览失败'))
    }).finally(() => { if (!loadController.signal.aborted) setLoading(false) })
    return () => {
      loadController.abort()
      controller.current?.abort()
      idempotencyKey.current = undefined
    }
  }, [sourceRunID])

  const paths = useMemo(() => new Set(preview?.inputRedactedPaths ?? []), [preview])
  const schema = useMemo(() => preview ? stripDefaults(preview.inputSchema as JSONSchema) : undefined, [preview])
  const value = useMemo(() => preview ? retryDisplayValue(preview.input as FormValue, preview.inputRedactedPaths, secretValues) : {}, [preview, secretValues])
  const close = () => {
    if (pending) return
    setSecretValues({})
    idempotencyKey.current = undefined
    onRequestClose()
  }
  const submit = async (normalized: FormValue) => {
    if (!preview || pending) return
    const selected = pickPointerValues(normalized, preview.inputRedactedPaths)
    idempotencyKey.current ??= crypto.randomUUID()
    const runController = new AbortController()
    controller.current = runController
    setPending(true)
    setError('')
    try {
      const response = await api.retryRun(sourceRunID, idempotencyKey.current, { secretValues: selected }, runController.signal)
      let startedRunID = ''
      await readNDJSON(response, (event) => {
        if (!startedRunID && event.type === 'run.started') {
          startedRunID = event.runId
          setSecretValues({})
          idempotencyKey.current = undefined
          onRequestClose()
          onRetryCreated(event.runId)
        }
      }, runController.signal)
    } catch (cause) {
      if (cause instanceof APIError && cause.code === 'RUN_RETRY_ALREADY_CREATED' && cause.details?.runId) {
        setSecretValues({})
        idempotencyKey.current = undefined
        onRequestClose()
        onRetryCreated(cause.details.runId)
      } else if (!isAbort(cause)) {
		setError(publicError(cause, '重新运行失败'))
		if (cause instanceof APIError && cause.code === 'RUN_RETRY_SECRET_REQUIRED') queueMicrotask(() => focusPointer(preview.inputRedactedPaths[0]))
	  }
    } finally {
      if (controller.current === runController) controller.current = undefined
      if (!runController.signal.aborted) setPending(false)
    }
  }

  return <WorkbenchPanel titleId="retry-run-title" onRequestClose={close}>
    <h2 id="retry-run-title" className="workbench-title">重新运行</h2>
    <p>普通输入来自历史运行且只读；出于安全原因，请重新填写秘密字段。</p>
    {loading && <p aria-live="polite">正在加载重试预览…</p>}
    {error && <div role="alert">{error}</div>}
    {preview && schema && <SchemaForm schema={schema} value={value} onChange={(next) => setSecretValues(pickPointerValues(next, preview.inputRedactedPaths))}
      onSubmit={submit} submitLabel={pending ? '正在重新运行…' : '重新运行'} disabled={pending}
      editablePaths={paths} requiredPaths={paths} />}
  </WorkbenchPanel>
}

function stripDefaults(schema: JSONSchema): JSONSchema {
  const cloned = structuredClone(schema)
  const visit = (current: JSONSchema) => {
    delete current.default
    for (const child of Object.values(current.properties ?? {})) visit(child)
    if (current.items) visit(current.items)
    if (typeof current.additionalProperties === 'object') visit(current.additionalProperties)
  }
  visit(cloned)
  return cloned
}

function retryDisplayValue(input: FormValue, paths: string[], secrets: Record<string, unknown>): FormValue {
  const value = structuredClone(input)
  for (const path of paths) setPointerValue(value, path, secrets[path] ?? '')
  return value
}

export function pickPointerValues(value: FormValue, paths: readonly string[]): Record<string, unknown> {
  const selected: Record<string, unknown> = {}
  for (const path of paths) selected[path] = pointerValue(value, path)
  return selected
}

function pointerValue(value: unknown, path: string): unknown {
  let current = value
  for (const token of pointerTokens(path)) {
    if (Array.isArray(current)) current = current[Number(token)]
    else if (current && typeof current === 'object') current = (current as Record<string, unknown>)[token]
    else return undefined
  }
  return current
}

function setPointerValue(value: FormValue, path: string, replacement: unknown) {
  const tokens = pointerTokens(path)
  let current: unknown = value
  for (let index = 0; index < tokens.length - 1; index++) current = Array.isArray(current) ? current[Number(tokens[index])] : (current as Record<string, unknown>)[tokens[index]]
  const last = tokens.at(-1)
  if (last === undefined) return
  if (Array.isArray(current)) current[Number(last)] = replacement
  else if (current && typeof current === 'object') (current as Record<string, unknown>)[last] = replacement
}

function pointerTokens(path: string): string[] {
  if (!path.startsWith('/')) return []
  return path.slice(1).split('/').map((token) => token.replace(/~1/g, '/').replace(/~0/g, '~'))
}

function focusPointer(path?: string) {
  if (!path) return
  document.getElementById(`field-${path.slice(1).replace(/[^a-zA-Z0-9_-]/g, '-')}`)?.focus()
}

function isAbort(error: unknown) { return error instanceof DOMException && error.name === 'AbortError' }
function publicError(error: unknown, fallback: string) { return error instanceof APIError ? `${error.message}${error.requestId ? ` · Request ID: ${error.requestId}` : ''}` : fallback }
