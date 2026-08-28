import { useEffect, useRef } from 'react'

import { SchemaForm } from '../../components/schema-form/SchemaForm'
import type { JSONSchema } from '../../components/schema-form/types'
import type { ResolvedPorts } from '../../lib/api/client'
import { NodeIcon } from './NodeIcon'
import type { StudioNode } from './types'
import type { UseNodeConfigDraftResult } from './useNodeConfigDraft'

interface NodeConfigPanelProps {
  titleId: string
  node: StudioNode
  draft: UseNodeConfigDraftResult
  onApply: (config: Record<string, unknown>, ports: ResolvedPorts) => void | Promise<void>
  onApplyAndTest: (config: Record<string, unknown>, ports: ResolvedPorts) => void | Promise<void>
}

export function NodeConfigPanel({ titleId, node, draft, onApply, onApplyAndTest }: NodeConfigPanelProps) {
  const panelRef = useRef<HTMLDivElement>(null)
  const titleRef = useRef<HTMLHeadingElement>(null)
  const schema = node.data.definition?.configSchema as JSONSchema ?? { type: 'object', properties: {} }
  const hasFields = Object.keys(schema.properties ?? {}).length > 0
  const boundary = node.data.nodeType === 'start' || node.data.nodeType === 'end'
  const canApply = Boolean(draft.dirty && draft.status === 'ready' && draft.normalized && draft.preview)
  const submitDisabled = !canApply
  const apply = () => canApply ? onApply(draft.normalized!, draft.preview!.ports) : undefined
  const applyAndTest = () => canApply ? onApplyAndTest(draft.normalized!, draft.preview!.ports) : undefined
  useEffect(() => {
    const frame = window.requestAnimationFrame(() => {
      const field = panelRef.current?.querySelector<HTMLElement>('.schema-form input:not(:disabled), .schema-form textarea:not(:disabled), .schema-form select:not(:disabled), .schema-form button:not([type="submit"]):not(:disabled)')
      if (field) field.focus()
      else titleRef.current?.focus()
    })
    return () => window.cancelAnimationFrame(frame)
  }, [node.id])
  useEffect(() => {
    const shortcut = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key === 'Enter' && canApply) { event.preventDefault(); void apply() }
    }
    window.addEventListener('keydown', shortcut)
    return () => window.removeEventListener('keydown', shortcut)
  })
  const statusLabel = {
    idle: '已应用', dirty: '有未应用更改', resolving: '正在解析端口', ready: '可以应用', error: '需要处理',
  }[draft.status]
  const statusIcon = { idle: '✓', dirty: '●', resolving: '◌', ready: '✓', error: '!' }[draft.status]
  const firstValidationError = Object.keys(draft.validation.errors)[0]
  const focusValidationError = () => document.getElementById(`field-${firstValidationError.slice(1).replace(/[^a-zA-Z0-9_-]/g, '-')}`)?.focus()
  return <div ref={panelRef} className="node-config-panel">
    <header className="node-config-header">
      <span className="node-config-icon"><NodeIcon category={node.data.definition?.category ?? '节点'} decorative /></span>
      <span className="node-category">节点配置</span>
      <h2 ref={titleRef} id={titleId} tabIndex={-1}>{node.data.definition?.title ?? node.data.nodeType}</h2>
      <small>{node.data.nodeType}@{node.data.typeVersion} · {node.id}</small>
      <span className={`node-config-status ${draft.status}`} role="status" aria-live="polite"><span aria-hidden="true">{statusIcon}</span>{statusLabel}</span>
      {boundary && <span className="node-boundary-note">工作流唯一节点，不可删除</span>}
    </header>
    <div className="node-config-body">
    {node.data.issues.map((issue) => <p className="form-error" key={`${issue.code}-${issue.path}`}>{issue.message}</p>)}
    {draft.errorKind === 'validation' && firstValidationError && <button className="form-error-summary" type="button" onClick={focusValidationError}>{Object.keys(draft.validation.errors).length} 项配置需要处理</button>}
    {draft.errorKind === 'resolve' && draft.error && <div className="form-error" role="alert"><p>{draft.error}</p><button type="button" onClick={draft.retry}>重试端口解析</button></div>}
    {hasFields ? <SchemaForm
      key={node.id}
      schema={schema}
      value={draft.draft}
      onChange={draft.setDraft}
      onSubmit={apply}
      submitLabel={draft.status === 'resolving' ? '解析端口中…' : '应用配置'}
      secondarySubmit={{ label: '应用并试运行', onSubmit: applyAndTest }}
      resetAction={{ label: '重置', onReset: draft.reset, disabled: !draft.dirty }}
      disabled={submitDisabled}
      groupOptional
    /> : <div className="node-config-empty"><p>此节点无需配置</p><small>可以直接连接端口并继续搭建工作流。</small></div>}
    {draft.preview && <section className="port-preview"><h3>端口变化预览</h3>
      {draft.preview.added.map((key) => <p key={`added-${key}`}>新增 {key}</p>)}
      {draft.preview.removed.map((key) => <p key={`removed-${key}`}>移除 {key}</p>)}
      {draft.preview.invalidEdges.length > 0 && <><p className="form-error">{draft.preview.invalidEdges.length} 条连线将失效</p>{draft.preview.invalidEdges.map((edge) => <p key={edge.edgeId}>{edge.sourceNodeId}.{edge.sourcePort} → {edge.targetNodeId}.{edge.targetPort}</p>)}</>}
      {draft.preview.added.length === 0 && draft.preview.removed.length === 0 && <p>端口保持不变</p>}
    </section>}
    </div>
  </div>
}
