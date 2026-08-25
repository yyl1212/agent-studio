import { useEffect, useMemo, useRef, useState, type MouseEvent } from 'react'
import { useSearchParams } from 'react-router-dom'

import { StatusBadge } from '../../components/ui/StatusBadge'
import type { RunSummary } from '../../lib/api/client'
import { readRunSearch, writeRunSearch } from './managementQuery'
import { RunDetailPanel } from './RunDetailPanel'
import { useRunList } from './useRunList'

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

export function RunManagementView() {
  const [searchParams, setSearchParams] = useSearchParams()
  const parsed = useMemo(() => readRunSearch(searchParams), [searchParams])
  const { page, loading, error, reload } = useRunList(parsed.query)
  const [invalidNotice, setInvalidNotice] = useState(parsed.hadInvalid)
  const [selected, setSelected] = useState<RunSummary>()
  const [workflowInput, setWorkflowInput] = useState(parsed.query.workflowId ?? '')
  const [runInput, setRunInput] = useState(parsed.query.runId ?? '')
  const [afterInput, setAfterInput] = useState(parsed.query.startedAfter ?? '')
  const [beforeInput, setBeforeInput] = useState(parsed.query.startedBefore ?? '')
  const returnFocus = useRef<HTMLButtonElement | null>(null)
  const previousFilter = useRef('')

  useEffect(() => {
    if (parsed.hadInvalid) {
      setSearchParams(parsed.params, { replace: true })
      setInvalidNotice(true)
    }
  }, [parsed.hadInvalid, parsed.params.toString(), setSearchParams])
  useEffect(() => {
    setWorkflowInput(parsed.query.workflowId ?? '')
    setRunInput(parsed.query.runId ?? '')
    setAfterInput(parsed.query.startedAfter ?? '')
    setBeforeInput(parsed.query.startedBefore ?? '')
  }, [parsed.query.workflowId, parsed.query.runId, parsed.query.startedAfter, parsed.query.startedBefore])
  useEffect(() => {
    const current = parsed.params.toString()
    if (previousFilter.current && previousFilter.current !== current) setSelected(undefined)
    previousFilter.current = current
  }, [parsed.params.toString()])

  const changeQuery = (patch: Partial<typeof parsed.query>) => {
    setSelected(undefined)
    setSearchParams(writeRunSearch({ ...parsed.query, ...patch, cursor: undefined }))
  }
  const toggleStatus = (status: RunSummary['status']) => changeQuery({ statuses: toggle(parsed.query.statuses, status) })
  const toggleMode = (mode: RunSummary['mode']) => changeQuery({ modes: toggle(parsed.query.modes, mode) })
  const selectRun = (summary: RunSummary, event: MouseEvent<HTMLButtonElement>) => {
    returnFocus.current = event.currentTarget
    setSelected(summary)
  }
  const closeDetail = () => {
    setSelected(undefined)
    queueMicrotask(() => returnFocus.current?.focus())
  }

  return <section aria-labelledby="run-management-title">
    <div className="page-heading"><div><p className="eyebrow">RUNS</p><h2 id="run-management-title">运行</h2><p>按工作流、状态和时间查看运行摘要。</p></div></div>
    <div className="management-filters run-filters">
      <label>工作流 ID<input value={workflowInput} onChange={(event) => { const value = event.target.value; setWorkflowInput(value); if (!value || uuidPattern.test(value)) changeQuery({ workflowId: value || undefined }) }} /></label>
      <label>运行 ID<input value={runInput} onChange={(event) => { const value = event.target.value; setRunInput(value); if (!value || uuidPattern.test(value)) changeQuery({ runId: value || undefined }) }} /></label>
      <fieldset><legend>状态</legend>{(['running', 'cancelling', 'completed', 'failed', 'cancelled'] as const).map((status) => <label key={status}><input type="checkbox" checked={parsed.query.statuses.includes(status)} onChange={() => toggleStatus(status)} />{statusLabel(status)}</label>)}</fieldset>
      <fieldset><legend>模式</legend>{(['test', 'published', 'debug'] as const).map((mode) => <label key={mode}><input type="checkbox" checked={parsed.query.modes.includes(mode)} onChange={() => toggleMode(mode)} />{modeLabel(mode)}</label>)}</fieldset>
      <label>开始时间下限<input aria-label="开始时间下限" value={afterInput} onChange={(event) => { const value = event.target.value; setAfterInput(value); if (!value || isRFC3339(value)) changeQuery({ startedAfter: value || undefined }) }} /></label>
      <label>开始时间上限<input aria-label="开始时间上限" value={beforeInput} onChange={(event) => { const value = event.target.value; setBeforeInput(value); if (!value || isRFC3339(value)) changeQuery({ startedBefore: value || undefined }) }} /></label>
    </div>
    {invalidNotice && <p className="inline-notice" role="status">已移除无效筛选条件</p>}
    {error && <div className="state-card" role="alert">{error}<button type="button" onClick={reload}>重试</button></div>}
    {loading && page === null && <div className="state-card" aria-live="polite">正在加载运行记录…</div>}
    {!loading && page?.items.length === 0 && <div className="state-card">还没有运行记录</div>}
    {page && page.items.length > 0 && <><div className="management-table-scroll" tabIndex={0} aria-label="运行列表，可横向滚动"><table className="management-table"><thead><tr><th>工作流</th><th>模式</th><th>状态</th><th>开始时间</th><th>耗时</th><th>操作</th></tr></thead><tbody>{page.items.map((summary) => <tr key={summary.id}>
      <td>{summary.workflowName}<small>{summary.workflowSlug}</small></td><td>{modeLabel(summary.mode)}</td><td><StatusBadge tone={statusTone(summary.status)}>{statusLabel(summary.status)}</StatusBadge></td><td>{formatDate(summary.startedAt)}</td><td>{duration(summary)}</td><td><button type="button" aria-label={`查看运行 ${summary.id}`} onClick={(event) => selectRun(summary, event)}>查看</button></td>
    </tr>)}</tbody></table></div>{page.nextCursor && <button type="button" disabled={loading} onClick={() => setSearchParams(writeRunSearch({ ...parsed.query, cursor: page.nextCursor ?? undefined }))}>下一页</button>}</>}
    {selected && <RunDetailPanel summary={selected} onRequestClose={closeDetail} />}
  </section>
}

function toggle<T>(values: T[], value: T) { return values.includes(value) ? values.filter((item) => item !== value) : [...values, value] }
function modeLabel(mode: RunSummary['mode']) { return { test: '草稿测试', published: '已发布', debug: '局部调试' }[mode] }
function statusLabel(status: RunSummary['status']) { return { running: '运行中', cancelling: '取消中', completed: '已完成', failed: '失败', cancelled: '已取消' }[status] }
function statusTone(status: RunSummary['status']): 'info' | 'success' | 'danger' | 'warning' { return { running: 'info', cancelling: 'warning', completed: 'success', failed: 'danger', cancelled: 'warning' }[status] as 'info' | 'success' | 'danger' | 'warning' }
function formatDate(value: string) { return new Intl.DateTimeFormat('zh-CN', { dateStyle: 'short', timeStyle: 'medium' }).format(new Date(value)) }
function duration(run: RunSummary) { return run.endedAt ? `${((Date.parse(run.endedAt) - Date.parse(run.startedAt)) / 1000).toFixed(1)} 秒` : '—' }
function isRFC3339(value: string) { return /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.test(value) && Number.isFinite(Date.parse(value)) }
