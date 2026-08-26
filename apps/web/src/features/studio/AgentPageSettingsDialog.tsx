import { useEffect, useMemo, useRef, useState } from 'react'

import type { AgentPresentation } from '../../lib/api/client'

export interface AgentPageSettingsDialogProps {
  open: boolean
  workflowName: string
  workflowDescription: string
  value: AgentPresentation
  saving: boolean
  error?: string
  onClose(): void
  onSave(value: AgentPresentation): void
}

export function AgentPageSettingsDialog(props: AgentPageSettingsDialogProps) {
  const [draft, setDraft] = useState<AgentPresentation>(props.value)
  const [confirmDiscard, setConfirmDiscard] = useState(false)
  const dialogRef = useRef<HTMLDialogElement>(null)
  const restoreFocus = useRef<HTMLElement | null>(null)

  useEffect(() => {
    if (!props.open) return
    setDraft(props.value)
    setConfirmDiscard(false)
  }, [props.open, props.value.title, props.value.description, props.value.accent, props.value.submitLabel, props.value.resultMode])

  useEffect(() => {
    if (!props.open || !dialogRef.current) return
    const dialog = dialogRef.current
    restoreFocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
    if (typeof dialog.showModal === 'function') dialog.showModal()
    else dialog.setAttribute('open', '')
    return () => {
      if (dialog.open && typeof dialog.close === 'function') dialog.close()
      restoreFocus.current?.focus()
    }
  }, [props.open])

  const errors = useMemo(() => validatePresentation(draft), [draft])
  const dirty = !samePresentation(draft, props.value)
  if (!props.open) return null

  const requestClose = () => {
    if (dirty) setConfirmDiscard(true)
    else props.onClose()
  }
  const save = () => {
    if (Object.keys(errors).length > 0) return
    props.onSave({
      ...draft,
      title: draft.title.trim(),
      description: draft.description.trim(),
      submitLabel: draft.submitLabel.trim(),
    })
  }

  return <div className="dialog-backdrop agent-settings-backdrop">
    <dialog ref={dialogRef} aria-modal="true" aria-labelledby="agent-page-settings-title" className="agent-settings-dialog" onCancel={(event) => { event.preventDefault(); requestClose() }}>
      <header className="agent-settings-heading">
        <div><span className="node-category">Agent 页面</span><h2 id="agent-page-settings-title">页面设置</h2></div>
        <button type="button" aria-label="关闭页面设置" onClick={requestClose}>×</button>
      </header>
      {confirmDiscard ? <section className="agent-settings-discard" aria-live="polite">
        <h3>放弃未保存的页面设置？</h3>
        <p>这些修改尚未保存，关闭后将无法恢复。</p>
        <div className="dialog-actions">
          <button type="button" onClick={() => setConfirmDiscard(false)}>继续编辑</button>
          <button type="button" className="danger-button" onClick={props.onClose}>放弃更改</button>
        </div>
      </section> : <div className="agent-settings-layout">
        <form className="agent-settings-form" onSubmit={(event) => { event.preventDefault(); save() }}>
          <div className="agent-settings-field">
            <label htmlFor="agent-page-title">页面标题</label>
            <input id="agent-page-title" aria-describedby={errors.title ? 'agent-page-title-error' : undefined} value={draft.title} onChange={(event) => setDraft({ ...draft, title: event.target.value })} />
            {errors.title && <small id="agent-page-title-error" className="form-error">{errors.title}</small>}
          </div>
          <div className="agent-settings-field">
            <label htmlFor="agent-page-description">页面说明</label>
            <textarea id="agent-page-description" aria-describedby={errors.description ? 'agent-page-description-error' : undefined} value={draft.description} onChange={(event) => setDraft({ ...draft, description: event.target.value })} />
            {errors.description && <small id="agent-page-description-error" className="form-error">{errors.description}</small>}
          </div>
          <div className="agent-settings-field">
            <label htmlFor="agent-page-accent">强调色</label>
            <select id="agent-page-accent" value={draft.accent} onChange={(event) => setDraft({ ...draft, accent: event.target.value as AgentPresentation['accent'] })}>
              <option value="indigo">靛蓝</option><option value="blue">蓝色</option><option value="teal">青色</option><option value="amber">琥珀</option><option value="rose">玫红</option>
            </select>
          </div>
          <div className="agent-settings-field">
            <label htmlFor="agent-page-submit-label">提交按钮文案</label>
            <input id="agent-page-submit-label" aria-describedby={errors.submitLabel ? 'agent-page-submit-label-error' : undefined} value={draft.submitLabel} onChange={(event) => setDraft({ ...draft, submitLabel: event.target.value })} />
            {errors.submitLabel && <small id="agent-page-submit-label-error" className="form-error">{errors.submitLabel}</small>}
          </div>
          <div className="agent-settings-field">
            <label htmlFor="agent-page-result-mode">结果展示方式</label>
            <select id="agent-page-result-mode" value={draft.resultMode} onChange={(event) => setDraft({ ...draft, resultMode: event.target.value as AgentPresentation['resultMode'] })}>
              <option value="auto">自动识别</option><option value="text">文本</option><option value="json">JSON</option>
            </select>
          </div>
          <button type="button" className="secondary-button" onClick={() => setDraft({
            title: props.workflowName, description: props.workflowDescription,
            accent: 'indigo', submitLabel: '运行 Agent', resultMode: 'auto',
          })}>使用工作流信息</button>
          {props.error && <p className="form-error" role="alert">{props.error}</p>}
          <div className="dialog-actions">
            <button type="button" onClick={requestClose}>取消</button>
            <button type="submit" className="primary-button" disabled={props.saving || Object.keys(errors).length > 0}>{props.saving ? '保存中…' : '保存设置'}</button>
          </div>
        </form>
        <section className={`agent-settings-preview accent-${draft.accent}`} role="region" aria-label="Agent 页面预览">
          <small>页面预览</small>
          <h3>{draft.title.trim() || 'Agent 标题'}</h3>
          <p>{draft.description.trim() || '页面说明将显示在这里'}</p>
          <div className="agent-settings-preview-field">开始节点表单</div>
          <button type="button">{draft.submitLabel.trim() || '提交'}</button>
          <span>结果：{resultModeLabel(draft.resultMode)}</span>
        </section>
      </div>}
    </dialog>
  </div>
}

function validatePresentation(value: AgentPresentation) {
  const errors: Partial<Record<'title' | 'description' | 'submitLabel', string>> = {}
  const title = value.title.trim()
  const submitLabel = value.submitLabel.trim()
  if (!title) errors.title = '页面标题不能为空'
  else if (characterCount(title) > 80) errors.title = '页面标题不能超过 80 个字符'
  if (characterCount(value.description.trim()) > 500) errors.description = '页面说明不能超过 500 个字符'
  if (!submitLabel) errors.submitLabel = '提交按钮文案不能为空'
  else if (characterCount(submitLabel) > 24) errors.submitLabel = '提交按钮文案不能超过 24 个字符'
  return errors
}

function characterCount(value: string) { return Array.from(value).length }

function samePresentation(left: AgentPresentation, right: AgentPresentation) {
  return left.title === right.title && left.description === right.description && left.accent === right.accent && left.submitLabel === right.submitLabel && left.resultMode === right.resultMode
}

function resultModeLabel(mode: AgentPresentation['resultMode']) {
  if (mode === 'json') return 'JSON'
  if (mode === 'text') return '文本'
  return '自动识别'
}
