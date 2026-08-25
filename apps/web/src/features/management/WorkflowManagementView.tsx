import { useEffect, useMemo, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'

import { ConfirmDialog } from '../../components/ui/ConfirmDialog'
import { StatusBadge } from '../../components/ui/StatusBadge'
import { APIError, api, type Workflow, type WorkflowSummary } from '../../lib/api/client'
import { ImportWorkflowTemplateDialog } from '../workflows/ImportWorkflowTemplateDialog'
import { readWorkflowSearch, writeWorkflowSearch } from './managementQuery'
import { useWorkflowList } from './useWorkflowList'
import { WorkflowMutationDialog } from './WorkflowMutationDialog'

type Mutation = { mode: 'create' } | { mode: 'copy' | 'rename'; workflow: WorkflowSummary }

export function WorkflowManagementView() {
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const parsed = useMemo(() => readWorkflowSearch(searchParams), [searchParams])
  const { page, loading, error, reload } = useWorkflowList(parsed.query)
  const [invalidNotice, setInvalidNotice] = useState(parsed.hadInvalid)
  const [notice, setNotice] = useState('')
  const [mutation, setMutation] = useState<Mutation | null>(null)
  const [importing, setImporting] = useState(false)
  const [menuID, setMenuID] = useState<string | null>(null)
  const [archiveTarget, setArchiveTarget] = useState<WorkflowSummary | null>(null)
  const [operationPending, setOperationPending] = useState(false)

  useEffect(() => {
    if (parsed.hadInvalid) {
      setSearchParams(parsed.params, { replace: true })
      setInvalidNotice(true)
    }
  }, [parsed.hadInvalid, parsed.params.toString(), setSearchParams])

  const changeQuery = (patch: Partial<typeof parsed.query>) => {
    setMenuID(null)
    setMutation(null)
    setArchiveTarget(null)
    setSearchParams(writeWorkflowSearch({ ...parsed.query, ...patch, cursor: undefined }))
  }

  const mutationSuccess = (workflow: Workflow) => {
    setMutation(null)
    if (mutation?.mode === 'rename') {
      setNotice('已更新工作流')
      reload()
    } else navigate(`/workflows/${workflow.id}`)
  }

  const stateChanged = () => {
    setNotice('工作流状态已变化')
    reload()
  }

  const archive = async () => {
    if (!archiveTarget || operationPending) return
    setOperationPending(true)
    try {
      await api.archiveWorkflow(archiveTarget.id)
      setArchiveTarget(null)
      setNotice('已归档')
      reload()
    } catch (cause) {
      setNotice(publicOperationError(cause))
      if (cause instanceof APIError && cause.code === 'WORKFLOW_ARCHIVED') reload()
    } finally {
      setOperationPending(false)
    }
  }

  const restore = async (workflow: WorkflowSummary) => {
    if (operationPending) return
    setOperationPending(true)
    setMenuID(null)
    try {
      await api.restoreWorkflow(workflow.id)
      setNotice('已恢复')
      reload()
    } catch (cause) {
      setNotice(publicOperationError(cause))
    } finally {
      setOperationPending(false)
    }
  }

  return <section aria-labelledby="workflow-management-title">
    <div className="page-heading">
      <div><p className="eyebrow">MANAGEMENT</p><h2 id="workflow-management-title">工作流</h2><p>搜索、维护并归档 Agent 工作流。</p></div>
      <div className="page-actions"><button type="button" onClick={() => setImporting(true)}>导入模板</button><button className="primary-button" type="button" onClick={() => setMutation({ mode: 'create' })}>新建工作流</button></div>
    </div>
    <div className="management-filters">
      <label>搜索工作流<input type="search" aria-label="搜索工作流" value={parsed.query.q} onChange={(event) => changeQuery({ q: event.target.value })} /></label>
      <label>工作流状态<select value={parsed.query.state} onChange={(event) => changeQuery({ state: event.target.value as typeof parsed.query.state })}><option value="active">活跃</option><option value="archived">已归档</option><option value="all">全部</option></select></label>
    </div>
    {invalidNotice && <p className="inline-notice" role="status">已移除无效筛选条件</p>}
    {notice && <p className="inline-notice" role="status">{notice}</p>}
    {error && <div className="state-card" role="alert">{error}<button type="button" onClick={reload}>重试</button></div>}
    {loading && page === null && <div className="state-card" aria-live="polite">正在加载工作流…</div>}
    {!loading && page?.items.length === 0 && <div className="state-card"><h3>还没有工作流</h3><p>新建一个工作流，从开始节点配置 Agent 输入。</p></div>}
    {page && page.items.length > 0 && <>
      <table className="management-table"><thead><tr><th>名称</th><th>状态</th><th>草稿</th><th>发布</th><th>更新</th><th>操作</th></tr></thead><tbody>
        {page.items.map((workflow) => <tr key={workflow.id}>
          <td><Link to={`/workflows/${workflow.id}`}>{workflow.name}</Link><small>{workflow.description || workflow.slug}</small></td>
          <td><StatusBadge tone={workflow.archivedAt ? 'warning' : 'success'}>{workflow.archivedAt ? '已归档' : '活跃'}</StatusBadge></td>
          <td>r{workflow.draftRevision}</td><td>{workflow.publishedVersion ? `v${workflow.publishedVersion}` : '未发布'}</td><td>{formatDate(workflow.updatedAt)}</td>
          <td><button type="button" aria-label={`${workflow.name} 的操作`} disabled={operationPending} onClick={() => setMenuID(menuID === workflow.id ? null : workflow.id)}>操作</button>
            {menuID === workflow.id && <div className="row-actions"><button type="button" onClick={() => { setMutation({ mode: 'rename', workflow }); setMenuID(null) }}>重命名</button><button type="button" onClick={() => { setMutation({ mode: 'copy', workflow }); setMenuID(null) }}>复制</button>{workflow.archivedAt ? <button type="button" onClick={() => restore(workflow)}>恢复</button> : <button type="button" onClick={() => { setArchiveTarget(workflow); setMenuID(null) }}>归档</button>}</div>}
          </td>
        </tr>)}
      </tbody></table>
      {page.nextCursor && <button type="button" disabled={loading} onClick={() => setSearchParams(writeWorkflowSearch({ ...parsed.query, cursor: page.nextCursor ?? undefined }))}>下一页</button>}
    </>}
    {mutation && <WorkflowMutationDialog {...mutation} onClose={() => setMutation(null)} onSuccess={mutationSuccess} onStateChanged={stateChanged} />}
    {importing && <ImportWorkflowTemplateDialog onClose={() => setImporting(false)} onImported={(workflow) => navigate(`/workflows/${workflow.id}`)} />}
    <ConfirmDialog open={archiveTarget !== null} title="归档工作流" description={`归档后“${archiveTarget?.name ?? ''}”将进入只读状态。`} confirmLabel="确认归档" cancelLabel="取消" confirmDisabled={operationPending} onConfirm={archive} onCancel={() => setArchiveTarget(null)} />
  </section>
}

function formatDate(value: string) {
  return new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
}

function publicOperationError(error: unknown) {
  if (error instanceof APIError) return `${error.message}${error.requestId ? ` · Request ID: ${error.requestId}` : ''}`
  return '操作失败，请稍后重试'
}
