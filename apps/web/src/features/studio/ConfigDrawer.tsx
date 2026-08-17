import { SchemaForm } from '../../components/schema-form/SchemaForm'
import type { JSONSchema } from '../../components/schema-form/types'
import type { StudioNode } from './types'

interface ConfigDrawerProps {
  node: StudioNode
  onChange: (config: Record<string, unknown>) => void
  onClose: () => void
}

export function ConfigDrawer({ node, onChange, onClose }: ConfigDrawerProps) {
  return (
    <aside className="studio-drawer right" role="dialog" aria-label="节点配置">
      <div className="drawer-heading"><div><span className="node-category">节点配置</span><h2>{node.data.definition?.title ?? node.data.nodeType}</h2></div><button type="button" aria-label="关闭节点配置" onClick={onClose}>×</button></div>
      {node.data.issues.map((issue) => <p className="form-error" key={`${issue.code}-${issue.path}`}>{issue.message}</p>)}
      <SchemaForm
        schema={node.data.definition?.configSchema as JSONSchema ?? { type: 'object', properties: {} }}
        value={node.data.config}
        onChange={onChange}
        onSubmit={() => undefined}
        submitLabel="应用配置"
      />
    </aside>
  )
}
