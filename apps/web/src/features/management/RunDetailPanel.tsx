import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'

import { APIError, api, type NodeRun, type Run, type RunSummary } from '../../lib/api/client'
import { WorkbenchPanel } from '../studio/WorkbenchPanel'

interface RunDetailPanelProps {
  summary: RunSummary
  onRequestClose: () => void
}

export function RunDetailPanel({ summary, onRequestClose }: RunDetailPanelProps) {
  const [detail, setDetail] = useState<{ run: Run; nodeRuns: NodeRun[] }>()
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [generation, setGeneration] = useState(0)
  const title = useRef<HTMLHeadingElement>(null)

  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    setError('')
    setDetail(undefined)
    api.getRun(summary.id, controller.signal).then((loaded) => {
      if (!controller.signal.aborted) setDetail(loaded)
    }).catch((cause: unknown) => {
      if (!controller.signal.aborted && !isAbort(cause)) setError(publicError(cause))
    }).finally(() => {
      if (!controller.signal.aborted) setLoading(false)
    })
    return () => controller.abort()
  }, [summary.id, generation])

  useEffect(() => { if (detail) title.current?.focus() }, [detail])

  return <WorkbenchPanel titleId="run-detail-title" onRequestClose={onRequestClose}>
    <h2 id="run-detail-title" className="workbench-title" tabIndex={-1} ref={title}>运行详情</h2>
    <p>{summary.workflowName} · {modeLabel(summary.mode)} · {statusLabel(summary.status)}</p>
    {loading && <p aria-live="polite">正在加载运行详情…</p>}
    {error && <div role="alert">{error}<button type="button" onClick={() => setGeneration((value) => value + 1)}>重试加载详情</button></div>}
    {detail && <div className="run-detail-content">
      <dl><div><dt>开始</dt><dd>{formatDate(detail.run.startedAt)}</dd></div><div><dt>耗时</dt><dd>{duration(detail.run.startedAt, detail.run.endedAt)}</dd></div></dl>
      {detail.run.output !== undefined && <section><h3>输出</h3><pre>{formatOutput(detail.run.output)}</pre></section>}
      <section><h3>节点记录</h3>{detail.nodeRuns.length === 0 ? <p>没有节点记录</p> : detail.nodeRuns.map((node) => <article key={node.id}><strong>{node.nodeId}</strong><span>{node.nodeType} · {node.status}</span>{node.output !== undefined && <pre>{formatOutput(node.output)}</pre>}</article>)}</section>
      {summary.status !== 'running' && <Link to={`/workflows/${summary.workflowId}/runs/${summary.id}/debug`}>调试回放</Link>}
    </div>}
  </WorkbenchPanel>
}

function isAbort(error: unknown) { return error instanceof DOMException && error.name === 'AbortError' }
function publicError(error: unknown) { return error instanceof APIError ? `${error.message}${error.requestId ? ` · Request ID: ${error.requestId}` : ''}` : '加载运行详情失败' }
function modeLabel(mode: RunSummary['mode']) { return { test: '草稿测试', published: '已发布', debug: '局部调试' }[mode] }
function statusLabel(status: RunSummary['status']) { return { running: '运行中', completed: '已完成', failed: '失败', cancelled: '已取消' }[status] }
function formatDate(value: string) { return new Intl.DateTimeFormat('zh-CN', { dateStyle: 'short', timeStyle: 'medium' }).format(new Date(value)) }
function duration(startedAt: string, endedAt?: string | null) { return endedAt ? `${((Date.parse(endedAt) - Date.parse(startedAt)) / 1000).toFixed(1)} 秒` : '—' }
function formatOutput(value: unknown) { return typeof value === 'string' ? value : JSON.stringify(value, null, 2) }
