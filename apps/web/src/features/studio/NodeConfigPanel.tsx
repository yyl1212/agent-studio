import { useEffect, useRef } from 'react'

import { SchemaForm } from '../../components/schema-form/SchemaForm'
import type { JSONSchema } from '../../components/schema-form/types'
import type { ResolvedPorts } from '../../lib/api/client'
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
  const canApply = Boolean(draft.dirty && draft.status === 'ready' && draft.normalized && draft.preview)
  const submitDisabled = !draft.dirty || draft.status === 'idle' || draft.status === 'resolving' || draft.status === 'error'
  const apply = () => canApply ? onApply(draft.normalized!, draft.preview!.ports) : undefined
  const applyAndTest = () => canApply ? onApplyAndTest(draft.normalized!, draft.preview!.ports) : undefined
  useEffect(() => {
    const frame = window.requestAnimationFrame(() => {
      panelRef.current?.querySelector<HTMLElement>('.schema-form input:not(:disabled), .schema-form textarea:not(:disabled), .schema-form select:not(:disabled), .schema-form button:not([type="submit"]):not(:disabled)')?.focus()
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
  return <div ref={panelRef} className="node-config-panel">
    <header><span className="node-category">节点配置</span><h2 id={titleId}>{node.data.definition?.title ?? node.data.nodeType}</h2><small>{node.data.nodeType}@{node.data.typeVersion} · {node.id}</small></header>
    {node.data.issues.map((issue) => <p className="form-error" key={`${issue.code}-${issue.path}`}>{issue.message}</p>)}
    {draft.error && <div className="form-error" role="alert"><p>{draft.error}</p><button type="button" onClick={draft.retry}>重试解析端口</button></div>}
    <SchemaForm
      schema={node.data.definition?.configSchema as JSONSchema ?? { type: 'object', properties: {} }}
      value={draft.draft}
      onChange={draft.setDraft}
      onSubmit={apply}
      submitLabel={draft.status === 'resolving' ? '解析端口中…' : '应用配置'}
      secondarySubmit={{ label: '应用并试运行', onSubmit: applyAndTest }}
      disabled={submitDisabled}
      groupOptional
    />
    {draft.preview && <section className="port-preview"><h3>端口变化预览</h3>
      {draft.preview.added.map((key) => <p key={`added-${key}`}>新增 {key}</p>)}
      {draft.preview.removed.map((key) => <p key={`removed-${key}`}>移除 {key}</p>)}
      {draft.preview.invalidEdges.length > 0 && <p className="form-error">{draft.preview.invalidEdges.length} 条连线将失效</p>}
      {draft.preview.added.length === 0 && draft.preview.removed.length === 0 && <p>端口保持不变</p>}
    </section>}
  </div>
}
