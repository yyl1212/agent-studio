import { useMemo, useState } from 'react'

import type { NodeDefinition } from '../../lib/api/client'

interface NodeLibraryDrawerProps {
  definitions: NodeDefinition[]
  onAdd: (definition: NodeDefinition) => void
  onClose: () => void
}

export function NodeLibraryDrawer({ definitions, onAdd, onClose }: NodeLibraryDrawerProps) {
  const [query, setQuery] = useState('')
  const groups = useMemo(() => {
    const filtered = definitions.filter((definition) => definition.type !== 'start' && definition.type !== 'end')
      .filter((definition) => `${definition.title} ${definition.description}`.toLowerCase().includes(query.toLowerCase()))
    const grouped = new Map<string, NodeDefinition[]>()
    for (const definition of filtered) grouped.set(definition.category, [...(grouped.get(definition.category) ?? []), definition])
    return grouped
  }, [definitions, query])
  return (
    <aside className="studio-drawer left" role="dialog" aria-label="节点库">
      <div className="drawer-heading"><h2>节点库</h2><button type="button" aria-label="关闭节点库" onClick={onClose}>×</button></div>
      <label className="search-field">搜索节点<input value={query} onChange={(event) => setQuery(event.currentTarget.value)} /></label>
      {[...groups.entries()].map(([category, items]) => (
        <section key={category}><h3>{category}</h3>{items.map((definition) => (
          <button className="library-node" type="button" key={`${definition.type}@${definition.version}`} onClick={() => onAdd(definition)}>
            <strong>{definition.title}</strong><small>{definition.description}</small>
          </button>
        ))}</section>
      ))}
      {groups.size === 0 && <p>没有匹配的节点</p>}
    </aside>
  )
}
