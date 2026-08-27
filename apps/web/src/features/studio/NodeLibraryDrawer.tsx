import { useMemo, useRef, useState, type KeyboardEvent } from 'react'

import type { NodeDefinition } from '../../lib/api/client'

interface NodeLibraryDrawerProps {
  definitions: NodeDefinition[]
  onAdd: (definition: NodeDefinition) => void
  onClose: () => void
}

export function NodeLibraryDrawer({ definitions, onAdd, onClose }: NodeLibraryDrawerProps) {
  const [query, setQuery] = useState('')
  const itemRefs = useRef<Array<HTMLButtonElement | null>>([])
  const visibleDefinitions = useMemo(() => definitions
    .filter((definition) => definition.type !== 'start' && definition.type !== 'end')
    .filter((definition) => [definition.title, definition.description, definition.type, definition.package.name, definition.package.displayName]
      .join(' ').toLowerCase().includes(query.toLowerCase())), [definitions, query])
  const groups = useMemo(() => {
    const grouped = new Map<string, NodeDefinition[]>()
    for (const definition of visibleDefinitions) grouped.set(definition.category, [...(grouped.get(definition.category) ?? []), definition])
    return grouped
  }, [visibleDefinitions])
  const orderedDefinitions = useMemo(() => [...groups.values()].flat(), [groups])
  const focusItem = (index: number) => {
    if (orderedDefinitions.length === 0) return
    itemRefs.current[(index + orderedDefinitions.length) % orderedDefinitions.length]?.focus()
  }
  const handleItemKeyDown = (event: KeyboardEvent<HTMLButtonElement>, index: number, definition: NodeDefinition) => {
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      focusItem(index + 1)
    } else if (event.key === 'ArrowUp') {
      event.preventDefault()
      focusItem(index - 1)
    } else if (event.key === 'Enter') {
      event.preventDefault()
      onAdd(definition)
    } else if (event.key === 'Escape') {
      event.preventDefault()
      onClose()
    }
  }
  return (
    <aside className="studio-drawer left" role="dialog" aria-label="节点库">
      <div className="drawer-heading"><h2>节点库</h2><button type="button" aria-label="关闭节点库" onClick={onClose}>×</button></div>
      <label className="search-field">搜索节点<input autoFocus value={query} onChange={(event) => setQuery(event.currentTarget.value)} onKeyDown={(event) => {
        if (event.key === 'ArrowDown') {
          event.preventDefault()
          focusItem(0)
        } else if (event.key === 'Escape') {
          event.preventDefault()
          onClose()
        }
      }} /></label>
      {[...groups.entries()].map(([category, items]) => (
        <section key={category}><h3>{category}</h3>{items.map((definition) => (
          <button
            ref={(element) => { itemRefs.current[orderedDefinitions.indexOf(definition)] = element }}
            className="library-node"
            type="button"
            key={`${definition.type}@${definition.version}`}
            onClick={() => onAdd(definition)}
            onKeyDown={(event) => handleItemKeyDown(event, orderedDefinitions.indexOf(definition), definition)}
          >
            <strong>{definition.title}</strong><small>{definition.description}</small>
            <small className="package-summary">{definition.package.displayName}{definition.package.version ? ` · ${definition.package.version}` : ''}</small>
          </button>
        ))}</section>
      ))}
      {groups.size === 0 && <p>没有匹配的节点</p>}
    </aside>
  )
}
