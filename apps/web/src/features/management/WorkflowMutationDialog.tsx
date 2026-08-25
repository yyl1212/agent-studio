import { useState, type FormEvent } from 'react'

import { APIError, api, type Workflow, type WorkflowSummary } from '../../lib/api/client'

type Mode = 'create' | 'copy' | 'rename'

interface Props {
  mode: Mode
  workflow?: WorkflowSummary
  onClose: () => void
  onSuccess: (workflow: Workflow) => void
  onStateChanged: () => void
}

export function WorkflowMutationDialog({ mode, workflow, onClose, onSuccess, onStateChanged }: Props) {
  const [name, setName] = useState(mode === 'copy' ? `${workflow?.name ?? ''} 副本` : workflow?.name ?? '')
  const [slug, setSlug] = useState(mode === 'copy' ? `${workflow?.slug ?? ''}-copy` : '')
  const [description, setDescription] = useState(workflow?.description ?? '')
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (pending) return
    const normalizedName = name.trim()
    const normalizedSlug = slug.trim()
    if (!normalizedName || ((mode === 'create' || mode === 'copy') && !/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(normalizedSlug))) {
      setError('请填写名称，并使用小写字母、数字和连字符设置地址标识')
      return
    }
    setPending(true)
    setError('')
    try {
      let result: Workflow
      if (mode === 'create') result = await api.createWorkflow({ name: normalizedName, slug: normalizedSlug, description })
      else if (mode === 'copy' && workflow) result = await api.copyWorkflow(workflow.id, { name: normalizedName, slug: normalizedSlug })
      else if (mode === 'rename' && workflow) result = await api.updateWorkflow(workflow.id, { name: normalizedName, description })
      else throw new Error('workflow mutation is missing source')
      onSuccess(result)
    } catch (cause) {
      if (cause instanceof APIError && cause.code === 'WORKFLOW_ARCHIVED') {
        onStateChanged()
        onClose()
      } else {
        setError(publicMutationError(cause))
      }
    } finally {
      setPending(false)
    }
  }

  const title = mode === 'create' ? '新建工作流' : mode === 'copy' ? '复制工作流' : '重命名工作流'
  const submitLabel = mode === 'create' ? '创建' : mode === 'copy' ? '创建副本' : '保存修改'
  return <div className="dialog-backdrop"><dialog open aria-labelledby="workflow-mutation-title">
    <form onSubmit={submit}>
      <h3 id="workflow-mutation-title">{title}</h3>
      <fieldset disabled={pending}>
        <label>名称<input value={name} onChange={(event) => setName(event.target.value)} required autoFocus /></label>
        {(mode === 'create' || mode === 'copy') && <label>Agent 地址标识<input value={slug} onChange={(event) => setSlug(event.target.value)} required pattern="[a-z0-9]+(?:-[a-z0-9]+)*" /></label>}
        {(mode === 'create' || mode === 'rename') && <label>说明<textarea value={description} onChange={(event) => setDescription(event.target.value)} /></label>}
      </fieldset>
      {error && <p className="form-error" role="alert">{error}</p>}
      <div className="dialog-actions">
        <button type="button" disabled={pending} onClick={onClose}>取消</button>
        <button className="primary-button" type="submit" disabled={pending}>{pending ? '提交中…' : submitLabel}</button>
      </div>
    </form>
  </dialog></div>
}

function publicMutationError(error: unknown) {
  if (error instanceof APIError) return `${error.message}${error.requestId ? ` · Request ID: ${error.requestId}` : ''}`
  return '操作失败，请稍后重试'
}
