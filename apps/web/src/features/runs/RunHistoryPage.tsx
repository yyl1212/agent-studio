import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'

import { APIError, api, type NodeRun, type Run } from '../../lib/api/client'

export function RunHistoryPage() {
  const { id = '' } = useParams()
  const [runs, setRuns] = useState<Run[] | null>(null)
  const [nodeRuns, setNodeRuns] = useState<NodeRun[]>([])
  const [selectedRun, setSelectedRun] = useState<string>()
  const [error, setError] = useState('')

  useEffect(() => {
    const controller = new AbortController()
    api.listRuns(id, 50, controller.signal).then(setRuns).catch((failure: unknown) => {
      if (!(failure instanceof DOMException && failure.name === 'AbortError')) setError(message(failure))
    })
    return () => controller.abort()
  }, [id])

  const loadDetail = async (runID: string) => {
    setSelectedRun(runID)
    setError('')
    try {
      const detail = await api.getRun(runID)
      setNodeRuns(detail.nodeRuns)
    } catch (failure) {
      setError(message(failure))
    }
  }

  return (
    <main className="page-container">
      <div className="page-heading"><div><p className="eyebrow">RUNS</p><h2>运行记录</h2></div><Link to={`/workflows/${id}`}>返回编辑器</Link></div>
      {error && <p className="form-error" role="alert">{error}</p>}
      {runs === null && !error && <p aria-live="polite">正在加载运行记录…</p>}
      {runs?.length === 0 && <div className="state-card">还没有运行记录</div>}
      {runs && runs.length > 0 && (
        <div className="run-history-layout">
          <div className="run-table" role="table" aria-label="运行记录">
            {runs.map((run) => (
              <button type="button" className={selectedRun === run.id ? 'selected' : ''} key={run.id} aria-label={`查看运行 ${run.id}`} onClick={() => loadDetail(run.id)}>
                <span>{run.mode === 'published' ? '已发布' : '草稿测试'}</span>
                <span>{run.workflowVersionId ? `版本 ${shortID(run.workflowVersionId)}` : `r${run.draftRevision ?? '-'}`}</span>
                <span>{statusLabel(run.status)}</span>
                <span>{formatDate(run.startedAt)}</span>
                <span>{duration(run)}</span>
              </button>
            ))}
          </div>
          {selectedRun && <aside className="run-detail"><h3>节点详情</h3>{nodeRuns.map((nodeRun) => <article key={nodeRun.id}><strong>{nodeRun.nodeId}</strong><span>{nodeRun.nodeType} · {nodeRun.status}</span>{nodeRun.output !== undefined && <pre>{formatOutput(nodeRun.output)}</pre>}</article>)}</aside>}
        </div>
      )}
    </main>
  )
}

function shortID(value: string) { return value.slice(0, 8) }
function formatDate(value: string) { return new Intl.DateTimeFormat('zh-CN', { dateStyle: 'short', timeStyle: 'medium' }).format(new Date(value)) }
function statusLabel(status: Run['status']) { return { running: '运行中', completed: '已完成', failed: '失败', cancelled: '已取消' }[status] }
function duration(run: Run) {
  if (!run.endedAt) return '—'
  return `${((new Date(run.endedAt).getTime() - new Date(run.startedAt).getTime()) / 1000).toFixed(1)} 秒`
}
function formatOutput(value: unknown) { return typeof value === 'string' ? value : JSON.stringify(value, null, 2) }
function message(error: unknown) { return error instanceof APIError ? `${error.message}${error.requestId ? `（请求 ID：${error.requestId}）` : ''}` : '加载运行记录失败' }
