import { useEffect, useRef, useState } from 'react'
import { useParams } from 'react-router-dom'

import { SchemaForm } from '../../components/schema-form/SchemaForm'
import type { JSONSchema } from '../../components/schema-form/types'
import { APIError, api, type AgentManifest } from '../../lib/api/client'
import { readNDJSON, type RunEvent } from '../../lib/api/ndjson'
import { RunProgress } from '../runs/RunProgress'
import './agent.css'

export function AgentPage() {
  const { slug = '' } = useParams()
  const [manifest, setManifest] = useState<AgentManifest>()
  const [input, setInput] = useState<Record<string, unknown>>({})
  const [loadError, setLoadError] = useState('')
  const [events, setEvents] = useState<RunEvent[]>([])
  const [running, setRunning] = useState(false)
  const [error, setError] = useState('')
  const controller = useRef<AbortController | undefined>(undefined)

  useEffect(() => {
    const loadController = new AbortController()
    api.getAgentManifest(slug, loadController.signal).then(setManifest).catch((loadFailure: unknown) => {
      if (!(loadFailure instanceof DOMException && loadFailure.name === 'AbortError')) setLoadError(formatError(loadFailure, 'Agent 不存在或尚未发布'))
    })
    return () => loadController.abort()
  }, [slug])

  const run = async (input: Record<string, unknown>) => {
    if (!manifest) return
    controller.current?.abort()
    const runController = new AbortController()
    controller.current = runController
    setEvents([])
    setError('')
    setRunning(true)
    try {
      const response = await api.runAgent(slug, { workflowVersionId: manifest.workflowVersionId, input }, runController.signal)
      await readNDJSON(response, (event) => setEvents((current) => [...current, event]), runController.signal)
    } catch (runFailure) {
      if (!(runFailure instanceof DOMException && runFailure.name === 'AbortError')) setError(formatError(runFailure, '运行失败，请稍后重试'))
    } finally {
      setRunning(false)
    }
  }

  if (loadError) return <main className="agent-page"><div className="agent-card"><p role="alert">{loadError}</p></div></main>
  if (!manifest) return <main className="agent-page" aria-live="polite">正在加载 Agent…</main>
  const completed = [...events].reverse().find((event) => event.type === 'run.completed')

  return (
    <main className="agent-page">
      <article className="agent-card">
        <header><span className="agent-version">Agent · v{manifest.version}</span><h2>{manifest.title}</h2>{manifest.description && <p>{manifest.description}</p>}</header>
        <SchemaForm schema={manifest.inputSchema as JSONSchema} value={input} onChange={setInput} onSubmit={run} submitLabel={running ? '运行中…' : '运行 Agent'} disabled={running} />
        {running && <button className="cancel-button" type="button" onClick={() => controller.current?.abort()}>取消运行</button>}
        <RunProgress events={events} />
        {completed && <section className="agent-result" aria-label="运行结果"><h3>运行结果</h3><pre>{formatOutput(completed.output)}</pre></section>}
        {error && <p className="form-error" role="alert">{error}</p>}
      </article>
    </main>
  )
}

function formatOutput(value: unknown) {
  return typeof value === 'string' ? value : JSON.stringify(value, null, 2)
}

function formatError(error: unknown, fallback: string) {
  if (!(error instanceof APIError)) return fallback
  return error.requestId ? `${error.message}（请求 ID：${error.requestId}）` : error.message
}
