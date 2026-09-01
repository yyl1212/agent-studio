import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'

import { APIError, api, type NodeRun, type Run, type RunSummary } from '../../lib/api/client'
import { ConfirmDialog } from '../../components/ui/ConfirmDialog'
import { WorkbenchPanel } from '../studio/WorkbenchPanel'
import { RetryRunDialog } from './RetryRunDialog'
import { RunRecoveryDialog } from './RunRecoveryDialog'

interface RunDetailPanelProps {
  summary: RunSummary
  onRequestClose: () => void
  onRunChanged?: (summary: RunSummary) => void
  onRetryCreated?: (runID: string) => void
}

export function RunDetailPanel({ summary, onRequestClose, onRunChanged, onRetryCreated }: RunDetailPanelProps) {
  const [detail, setDetail] = useState<{ run: Run; nodeRuns: NodeRun[] }>()
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [generation, setGeneration] = useState(0)
	const [cancelConfirm, setCancelConfirm] = useState(false)
	const [operationPending, setOperationPending] = useState(false)
	const [retryOpen, setRetryOpen] = useState(false)
	const [recoveryOpen, setRecoveryOpen] = useState(false)
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
  }, [summary.id, summary.status, summary.cancelRequestedAt, generation])

  useEffect(() => { if (detail) title.current?.focus() }, [detail])

	const run = detail?.run
	const cancellable = run?.status === 'running' || run?.status === 'queued'
	const retryable = (run?.status === 'failed' || run?.status === 'cancelled') && (run.mode === 'test' || run.mode === 'published')
	const cancel = async () => {
		if (!run || operationPending) return
		setOperationPending(true)
		setError('')
		try {
			const changed = await api.cancelRun(run.id)
			setDetail((current) => current ? { ...current, run: { ...current.run, status: changed.status, cancelRequestedAt: changed.cancelRequestedAt } } : current)
			onRunChanged?.(changed)
			setCancelConfirm(false)
		} catch (cause) { setError(publicError(cause)) } finally { setOperationPending(false) }
	}

	if (retryOpen) return <RetryRunDialog sourceRunID={summary.id} onRequestClose={() => setRetryOpen(false)} onRetryCreated={(runID) => onRetryCreated?.(runID)} />

  return <WorkbenchPanel titleId="run-detail-title" onRequestClose={onRequestClose}>
    <h2 id="run-detail-title" className="workbench-title" tabIndex={-1} ref={title}>运行详情</h2>
    <p>{summary.workflowName} · {modeLabel(run?.mode ?? summary.mode)} · {statusLabel(run?.status ?? summary.status)}</p>
    {loading && <p aria-live="polite">正在加载运行详情…</p>}
    {error && <div role="alert">{error}<button type="button" onClick={() => setGeneration((value) => value + 1)}>重试加载详情</button></div>}
    {detail && run && <div className="run-detail-content">
		<div className="run-detail-actions">
			{cancellable && <button type="button" disabled={operationPending} onClick={() => setCancelConfirm(true)}>取消运行</button>}
			{run.status === 'recovery_required' && <button type="button" className="primary-button" onClick={() => setRecoveryOpen(true)}>处理恢复</button>}
			{run?.status === 'cancelling' && <button type="button" disabled>取消中</button>}
			{retryable && <button type="button" onClick={() => setRetryOpen(true)}>重新运行</button>}
			{run?.mode === 'debug' && (run.status === 'failed' || run.status === 'cancelled') && <span>局部调试运行请通过调试回放继续。</span>}
		</div>
		<dl>
			<div><dt>Run ID</dt><dd>{run.id}</dd></div>
			<div><dt>版本</dt><dd>{run.mode === 'test' ? `草稿 revision ${run.draftRevision ?? '—'}` : run.mode === 'published' ? `发布版本 ${summary.workflowVersion ?? run.workflowVersionId ?? '—'}` : '局部调试'}</dd></div>
			<div><dt>开始</dt><dd>{formatDate(run.startedAt)}</dd></div><div><dt>结束</dt><dd>{run.endedAt ? formatDate(run.endedAt) : '—'}</dd></div><div><dt>耗时</dt><dd>{duration(run.startedAt, run.endedAt)}</dd></div>
		</dl>
		{run.retryOfRunId && <p>重试来源：<button type="button" onClick={() => onRetryCreated?.(run.retryOfRunId!)}>{run.retryOfRunId}</button></p>}
		{run.sourceRunId && <p>调试来源：{run.sourceRunId}{run.sourceNodeId ? ` / ${run.sourceNodeId}` : ''}</p>}
		{run.error && <section><h3>运行错误</h3><p>{run.error.code} · {run.error.message}</p></section>}
		<section><h3>输入</h3><pre>{formatOutput(run.input)}</pre></section>
		{run.output !== undefined && <section><h3>输出</h3><pre>{formatOutput(run.output)}</pre></section>}
		<section><h3>节点记录</h3>{detail.nodeRuns.length === 0 ? <p>没有节点记录</p> : detail.nodeRuns.map((node) => <article key={node.id}><strong>{node.nodeId}</strong><span>{node.nodeType} · {node.status}</span><small>{node.startedAt ? formatDate(node.startedAt) : '未开始'}{node.endedAt ? ` → ${formatDate(node.endedAt)}` : ''}</small>{node.error && <p>{node.error.code} · {node.error.message}</p>}{node.input !== undefined && <pre>{formatOutput(node.input)}</pre>}{node.output !== undefined && <pre>{formatOutput(node.output)}</pre>}</article>)}</section>
		{(run.status === 'completed' || run.status === 'failed' || run.status === 'cancelled') && <Link to={`/workflows/${summary.workflowId}/runs/${summary.id}/debug`}>调试回放</Link>}
    </div>}
		<ConfirmDialog open={cancelConfirm} title="取消运行" description="取消只能阻止后续节点，已发出的外部副作用可能无法撤回。" confirmLabel="确认取消" cancelLabel="继续运行" confirmDisabled={operationPending} onConfirm={() => void cancel()} onCancel={() => { if (!operationPending) setCancelConfirm(false) }} />
		<RunRecoveryDialog runID={summary.id} open={recoveryOpen} onClose={() => setRecoveryOpen(false)} onRecovered={() => { setGeneration((value) => value + 1); onRunChanged?.(summary) }} />
  </WorkbenchPanel>
}

function isAbort(error: unknown) { return error instanceof DOMException && error.name === 'AbortError' }
function publicError(error: unknown) { return error instanceof APIError ? `${error.message}${error.requestId ? ` · Request ID: ${error.requestId}` : ''}` : '加载运行详情失败' }
function modeLabel(mode: RunSummary['mode']) { return { test: '草稿测试', published: '已发布', debug: '局部调试' }[mode] }
function statusLabel(status: RunSummary['status']) { return { queued: '排队中', running: '运行中', recovery_required: '等待人工恢复', cancelling: '取消中', completed: '已完成', failed: '失败', cancelled: '已取消' }[status] }
function formatDate(value: string) { return new Intl.DateTimeFormat('zh-CN', { dateStyle: 'short', timeStyle: 'medium' }).format(new Date(value)) }
function duration(startedAt: string, endedAt?: string | null) { return endedAt ? `${((Date.parse(endedAt) - Date.parse(startedAt)) / 1000).toFixed(1)} 秒` : '—' }
function formatOutput(value: unknown) { return typeof value === 'string' ? value : JSON.stringify(value, null, 2) }
