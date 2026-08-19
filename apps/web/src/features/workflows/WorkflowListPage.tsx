import { useEffect, useState, type FormEvent } from 'react'
import { Link, useNavigate } from 'react-router-dom'

import { APIError, api, type Workflow } from '../../lib/api/client'
import { ImportWorkflowTemplateDialog } from './ImportWorkflowTemplateDialog'

export function WorkflowListPage() {
  const navigate = useNavigate()
  const [workflows, setWorkflows] = useState<Workflow[] | null>(null)
  const [loadError, setLoadError] = useState(false)
  const [creating, setCreating] = useState(false)
  const [importing, setImporting] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [formError, setFormError] = useState('')

  useEffect(() => {
    const controller = new AbortController()
    api.listWorkflows(controller.signal).then(setWorkflows).catch((error: unknown) => {
      if (!(error instanceof DOMException && error.name === 'AbortError')) setLoadError(true)
    })
    return () => controller.abort()
  }, [])

  const create = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const data = new FormData(event.currentTarget)
    const name = String(data.get('name') ?? '').trim()
    const slug = String(data.get('slug') ?? '').trim()
    const description = String(data.get('description') ?? '').trim()
    if (!name || !/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(slug)) {
      setFormError('请填写名称，并使用小写字母、数字和连字符设置地址标识')
      return
    }
    setSubmitting(true)
    setFormError('')
    try {
      const workflow = await api.createWorkflow({ name, slug, description })
      navigate(`/workflows/${workflow.id}`)
    } catch (error) {
      setFormError((error instanceof APIError && error.code === 'WORKFLOW_SLUG_CONFLICT') || isSlugConflict(error) ? 'Agent 地址标识已存在' : '创建工作流失败，请稍后重试')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <main className="page-container">
      <div className="page-heading">
        <div><p className="eyebrow">WORKFLOWS</p><h2>工作流</h2><p>在画布上组合节点，发布为可直接运行的 Agent。</p></div>
        <div className="page-actions">
          <button type="button" onClick={() => setImporting(true)}>导入模板</button>
          <button className="primary-button" type="button" onClick={() => setCreating(true)}>新建工作流</button>
        </div>
      </div>
      {loadError && <div className="state-card" role="alert">加载工作流失败，请刷新重试</div>}
      {!loadError && workflows === null && <div className="state-card" aria-live="polite">正在加载工作流…</div>}
      {workflows?.length === 0 && <div className="state-card"><h3>还没有工作流</h3><p>新建一个工作流，从开始节点配置 Agent 输入。</p></div>}
      {workflows && workflows.length > 0 && (
        <div className="workflow-grid">
          {workflows.map((workflow) => (
            <Link className="workflow-card" key={workflow.id} to={`/workflows/${workflow.id}`}>
              <div><h3>{workflow.name}</h3><p>{workflow.description || '暂无说明'}</p></div>
              <dl>
                <div><dt>草稿</dt><dd>r{workflow.draftRevision}</dd></div>
                <div><dt>发布</dt><dd>{workflow.publishedVersion ? `v${workflow.publishedVersion}` : '未发布'}</dd></div>
                <div><dt>更新</dt><dd>{formatDate(workflow.updatedAt)}</dd></div>
              </dl>
            </Link>
          ))}
        </div>
      )}
      {creating && (
        <div className="dialog-backdrop">
          <dialog open aria-labelledby="create-workflow-title">
            <form onSubmit={create}>
              <h3 id="create-workflow-title">新建工作流</h3>
              <label>名称<input name="name" required autoFocus /></label>
              <label>Agent 地址标识<input name="slug" required pattern="[a-z0-9]+(?:-[a-z0-9]+)*" placeholder="knowledge-helper" /></label>
              <label>说明<textarea name="description" /></label>
              {formError && <p className="form-error" role="alert">{formError}</p>}
              <div className="dialog-actions">
                <button type="button" onClick={() => { setCreating(false); setFormError('') }}>取消</button>
                <button className="primary-button" type="submit" disabled={submitting}>{submitting ? '创建中…' : '创建'}</button>
              </div>
            </form>
          </dialog>
        </div>
      )}
      {importing && (
        <ImportWorkflowTemplateDialog
          onClose={() => setImporting(false)}
          onImported={(workflow) => navigate(`/workflows/${workflow.id}`)}
        />
      )}
    </main>
  )
}

function isSlugConflict(error: unknown) {
  return typeof error === 'object' && error !== null && 'code' in error && error.code === 'WORKFLOW_SLUG_CONFLICT'
}

function formatDate(value: string) {
  return new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
}
