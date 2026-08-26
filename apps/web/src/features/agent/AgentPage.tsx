import { useCallback, useEffect, useState } from 'react'
import { useParams, useSearchParams } from 'react-router-dom'

import { SchemaForm } from '../../components/schema-form/SchemaForm'
import type { JSONSchema } from '../../components/schema-form/types'
import { APIError, api, type AgentManifest, type AgentPresentation } from '../../lib/api/client'
import { AgentRunView } from './AgentRunView'
import { useAgentRun } from './useAgentRun'
import './agent.css'

const accents = new Set<AgentPresentation['accent']>(['indigo', 'blue', 'teal', 'amber', 'rose'])

export function AgentPage() {
  const { slug = '' } = useParams()
  const [searchParams, setSearchParams] = useSearchParams()
  const runId = searchParams.get('runId') || undefined
  const [manifest, setManifest] = useState<AgentManifest>()
  const [input, setInput] = useState<Record<string, unknown>>({})
  const [loadError, setLoadError] = useState('')
  const [reloadKey, setReloadKey] = useState(0)
  const accepted = useCallback((acceptedRunID: string) => {
    setSearchParams({ runId: acceptedRunID }, { replace: true })
  }, [setSearchParams])
  const runner = useAgentRun({ slug, runId, onAccepted: accepted })

  useEffect(() => {
    if (runId) return
    const controller = new AbortController()
    setManifest(undefined)
    setLoadError('')
    api.getAgentManifest(slug, controller.signal).then(setManifest).catch((error: unknown) => {
      if (!(error instanceof DOMException && error.name === 'AbortError')) setLoadError(manifestError(error))
    })
    return () => controller.abort()
  }, [reloadKey, runId, slug])

  const restart = () => {
    const next = new URLSearchParams(searchParams)
    next.delete('runId')
    setSearchParams(next, { replace: true })
    setReloadKey((current) => current + 1)
  }

  if (loadError && !runId) return <AgentShell><p className="agent-load-error" role="alert">{loadError}</p></AgentShell>
  if (runId && !runner.view) {
    if (runner.phase === 'failed') return <AgentShell><AgentRunView {...runner} onCancel={() => { void runner.cancel() }} onRestart={restart} /></AgentShell>
    return <main className="agent-page agent-loading" aria-live="polite"><div className="agent-skeleton" /><p>正在恢复运行…</p></main>
  }
  if (!runId && !manifest) return <main className="agent-page agent-loading" aria-live="polite"><div className="agent-skeleton" /><p>正在加载 Agent…</p></main>

  const presentation = runId ? runner.view!.presentation : manifest!.presentation
  const version = runId ? runner.view!.run.version : manifest!.version
  const accent = accents.has(presentation.accent) ? presentation.accent : 'indigo'
  const starting = runner.phase === 'starting'

  return <main className="agent-page">
    <div className={`agent-shell accent-${accent}`}>
      <header className="agent-hero">
        <span className="agent-version">Agent · v{version}</span>
        <h1>{presentation.title}</h1>
        {presentation.description && <p>{presentation.description}</p>}
      </header>
      {!runId && manifest && <section className="agent-form-card" aria-label="Agent 输入">
        <SchemaForm
          schema={manifest.inputSchema as JSONSchema}
          value={input}
          onChange={setInput}
          onSubmit={(nextInput) => runner.start(manifest, nextInput)}
          submitLabel={starting ? '正在启动…' : presentation.submitLabel}
          disabled={starting}
        />
        {runner.error && <p className="form-error" role="alert">{runner.error}</p>}
      </section>}
      {runId && <>
        <div className="agent-input-summary">输入已提交。出于安全考虑，此处不显示输入值。</div>
        <AgentRunView {...runner} onCancel={() => { void runner.cancel() }} onRestart={restart} />
      </>}
    </div>
  </main>
}

function AgentShell({ children }: { children: React.ReactNode }) {
  return <main className="agent-page"><div className="agent-shell accent-indigo">{children}</div></main>
}

function manifestError(error: unknown) {
  if (!(error instanceof APIError)) return 'Agent 不存在或尚未发布'
  if (error.code === 'WORKFLOW_ARCHIVED') return '该 Agent 已归档，暂时不能运行'
  if (error.status === 404) return 'Agent 不存在或尚未发布'
  if (error.status === 503) return '服务暂时不可用，请稍后重试'
  return 'Agent 加载失败，请稍后重试'
}
