import { useEffect } from 'react'

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
}

export function NodeConfigPanel({ titleId, node, draft, onApply }: NodeConfigPanelProps) {
  const canApply = draft.dirty && draft.status === 'ready' && draft.normalized && draft.preview
  const apply = () => canApply ? onApply(draft.normalized!, draft.preview!.ports) : undefined
  useEffect(() => {
    const shortcut = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key === 'Enter' && canApply) { event.preventDefault(); void apply() }
    }
    window.addEventListener('keydown', shortcut)
    return () => window.removeEventListener('keydown', shortcut)
  })
  return <div className="node-config-panel">
    <header><span className="node-category">节点配置</span><h2 id={titleId}>{node.data.definition?.title ?? node.data.nodeType}</h2><small>{node.data.nodeType}@{node.data.typeVersion} · {node.id}</small></header>
    {node.data.issues.map((issue) => <p className="form-error" key={`${issue.code}-${issue.path}`}>{issue.message}</p>)}
    {draft.error && <p className="form-error" role="alert">{draft.error}</p>}
    <SchemaForm schema={node.data.definition?.configSchema as JSONSchema ?? { type: 'object', properties: {} }} value={draft.draft} onChange={draft.setDraft} onSubmit={apply} submitLabel={draft.status === 'resolving' ? '解析端口中…' : '应用配置'} disabled={!canApply} groupOptional />
    {draft.preview && <section className="port-preview"><h3>端口变化预览</h3>
      {draft.preview.added.map((key) => <p key={`added-${key}`}>新增 {key}</p>)}
      {draft.preview.removed.map((key) => <p key={`removed-${key}`}>移除 {key}</p>)}
      {draft.preview.invalidEdgeIds.length > 0 && <p className="form-error">{draft.preview.invalidEdgeIds.length} 条连线将失效</p>}
      {draft.preview.added.length === 0 && draft.preview.removed.length === 0 && <p>端口保持不变</p>}
    </section>}
  </div>
}
