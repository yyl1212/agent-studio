import { useEffect, useRef, useState, type ChangeEvent, type FormEvent } from 'react'

import {
  APIError,
  api,
  type Workflow,
  type WorkflowTemplate,
  type WorkflowTemplatePreview,
} from '../../lib/api/client'

type Props = {
  onClose: () => void
  onImported: (workflow: Workflow) => void
}

const maxTemplateBytes = 2 << 20
const slugPattern = /^[a-z0-9]+(?:-[a-z0-9]+)*$/

export function ImportWorkflowTemplateDialog({ onClose, onImported }: Props) {
  const controller = useRef<AbortController | null>(null)
  const [template, setTemplate] = useState<WorkflowTemplate | null>(null)
  const [preview, setPreview] = useState<WorkflowTemplatePreview | null>(null)
  const [name, setName] = useState('')
  const [slug, setSlug] = useState('')
  const [description, setDescription] = useState('')
  const [loading, setLoading] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => () => controller.current?.abort(), [])

  const close = () => {
    controller.current?.abort()
    onClose()
  }

  const selectFile = async (event: ChangeEvent<HTMLInputElement>) => {
    controller.current?.abort()
    const currentController = new AbortController()
    controller.current = currentController
    setTemplate(null)
    setPreview(null)
    setName('')
    setSlug('')
    setDescription('')
    setError('')
    setLoading(false)
    setSubmitting(false)

    const file = event.target.files?.[0]
    if (!file) return
    if (file.size > maxTemplateBytes) {
      setError('模板文件不能超过 2 MiB')
      return
    }

    let parsed: WorkflowTemplate
    try {
      const text = await file.text()
      if (currentController.signal.aborted) return
      const value: unknown = JSON.parse(text)
      if (!isRecord(value)) {
        setError('JSON 根节点必须是模板对象')
        return
      }
      parsed = value as WorkflowTemplate
    } catch {
      if (!currentController.signal.aborted) setError('模板文件不是有效的 JSON')
      return
    }

    const previewBody = JSON.stringify({ template: parsed })
    if (new Blob([previewBody]).size > maxTemplateBytes) {
      setError('模板预览请求不能超过 2 MiB')
      return
    }
    setTemplate(parsed)
    setName(typeof parsed.metadata?.name === 'string' ? parsed.metadata.name : '')
    setSlug(suggestSlug(typeof parsed.metadata?.name === 'string' ? parsed.metadata.name : ''))
    setDescription(typeof parsed.metadata?.description === 'string' ? parsed.metadata.description : '')
    setLoading(true)
    try {
      const analyzed = await api.previewWorkflowTemplate(parsed, currentController.signal)
      if (!currentController.signal.aborted) setPreview(analyzed)
    } catch (requestError) {
      if (!currentController.signal.aborted && !isAbort(requestError)) {
        setError(publicTemplateMessage(requestError, '模板预览失败，请检查文件后重试'))
      }
    } finally {
      if (!currentController.signal.aborted) setLoading(false)
    }
  }

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!template || !preview?.valid || submitting) return
    const normalizedName = name.trim()
    const normalizedSlug = slug.trim()
    if (!normalizedName || !slugPattern.test(normalizedSlug)) {
      setError('请填写名称，并使用小写字母、数字和连字符设置地址标识')
      return
    }
    const body = { template, name: normalizedName, slug: normalizedSlug, description }
    if (new Blob([JSON.stringify(body)]).size > maxTemplateBytes) {
      setError('模板导入请求不能超过 2 MiB')
      return
    }
    const currentController = controller.current ?? new AbortController()
    controller.current = currentController
    setSubmitting(true)
    setError('')
    try {
      const imported = await api.importWorkflowTemplate(body, currentController.signal)
      if (!currentController.signal.aborted) onImported(imported)
    } catch (requestError) {
      if (!currentController.signal.aborted && !isAbort(requestError)) {
        setError(publicTemplateMessage(requestError, '导入模板失败，请稍后重试'))
      }
    } finally {
      if (!currentController.signal.aborted) setSubmitting(false)
    }
  }

  const parameters = preview ? inputParameters(preview) : []

  return (
    <div className="dialog-backdrop">
      <dialog className="template-import-dialog" open aria-labelledby="import-template-title">
        <form onSubmit={submit}>
          <h3 id="import-template-title">导入工作流模板</h3>
          <p>选择本地 JSON 文件，先审查兼容性，再创建一个未发布的新工作流。</p>
          <label>
            选择模板文件
            <input type="file" accept=".json,application/json" onChange={selectFile} />
          </label>

          {loading && <p aria-live="polite">正在分析模板…</p>}
          {preview && (
            <section className="template-summary" aria-label="模板预览">
              <div>
                <strong>{preview.metadata.name}</strong>
                {preview.metadata.description && <p>{preview.metadata.description}</p>}
                <p>{preview.summary.nodeCount} 个节点 · {preview.summary.edgeCount} 条连线</p>
              </div>
              {parameters.length > 0 && (
                <div>
                  <h4>开始参数</h4>
                  <ul>{parameters.map((parameter) => <li key={parameter.key}>{parameter.key} · {parameter.title} · {parameter.required ? '必填' : '选填'}</li>)}</ul>
                </div>
              )}
              <div>
                <h4>节点兼容性</h4>
                <ul className="template-node-list">
                  {preview.summary.nodeTypes.map((node) => (
                    <li key={`${node.type}@${node.version}`}>
                      <span>{node.title} · {node.version}</span>
                      <span>{node.available ? `可用 · ${node.count} 个` : '不可用'}</span>
                      {node.capabilities.map((capability) => <span className="capability-badge" key={capability}>{capability}</span>)}
                    </li>
                  ))}
                </ul>
              </div>
              {preview.issues.length > 0 && (
                <div>
                  <h4>需要处理</h4>
                  <ul>{preview.issues.map((issue, index) => <li key={`${issue.code}-${issue.nodeId ?? ''}-${issue.path ?? ''}-${index}`}>{issue.message}</li>)}</ul>
                </div>
              )}
            </section>
          )}

          {template && (
            <fieldset disabled={submitting}>
              <legend>新工作流信息</legend>
              <label>名称<input value={name} onChange={(event) => setName(event.target.value)} required /></label>
              <label>Agent 地址标识<input value={slug} onChange={(event) => setSlug(event.target.value)} required pattern="[a-z0-9]+(?:-[a-z0-9]+)*" /></label>
              <label>说明<textarea value={description} onChange={(event) => setDescription(event.target.value)} /></label>
            </fieldset>
          )}

          {error && <p className="form-error" role="alert">{error}</p>}
          <div className="dialog-actions">
            <button type="button" onClick={close}>取消</button>
            <button className="primary-button" type="submit" disabled={!preview?.valid || loading || submitting}>
              {submitting ? '导入中…' : '导入并打开'}
            </button>
          </div>
        </form>
      </dialog>
    </div>
  )
}

export function suggestSlug(name: string) {
  const candidate = name.normalize('NFKD').toLowerCase()
    .replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '')
  return candidate || 'imported-workflow'
}

function inputParameters(preview: WorkflowTemplatePreview) {
  const schema = preview.summary.inputSchema
  const properties = isRecord(schema.properties) ? schema.properties : {}
  const required = new Set(Array.isArray(schema.required) ? schema.required.filter((item): item is string => typeof item === 'string') : [])
  return Object.entries(properties).map(([key, value]) => ({
    key,
    title: isRecord(value) && typeof value.title === 'string' ? value.title : key,
    required: required.has(key),
  }))
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isAbort(error: unknown) {
  return error instanceof DOMException && error.name === 'AbortError'
}

function publicTemplateMessage(error: unknown, fallback: string) {
  if (error instanceof APIError && error.code === 'WORKFLOW_SLUG_CONFLICT') return 'Agent 地址标识已存在'
  if (error instanceof APIError && error.code === 'WORKFLOW_TEMPLATE_INVALID') return '模板已变化或不兼容，请重新选择文件'
  return fallback
}
