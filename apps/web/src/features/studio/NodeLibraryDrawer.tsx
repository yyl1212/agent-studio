import {
  useMemo,
  useRef,
  useState,
  type DragEvent,
  type KeyboardEvent,
} from 'react'

import type { NodeDefinition } from '../../lib/api/client'
import {
  buildNodeLibraryView,
  NODE_DEFINITION_MIME,
  nodeDefinitionKey,
  type NodeLibraryScope,
} from './nodeLibraryModel'

interface NodeLibraryDrawerProps {
  definitions: NodeDefinition[]
  recentNodeKeys: string[]
  error?: string
  onAdd: (definition: NodeDefinition) => void
  onClose: () => void
}

export function NodeLibraryDrawer({
  definitions,
  recentNodeKeys,
  error,
  onAdd,
  onClose,
}: NodeLibraryDrawerProps) {
  const [query, setQuery] = useState('')
  const [scope, setScope] = useState<NodeLibraryScope>({ kind: 'all' })
  const itemRefs = useRef<Array<HTMLButtonElement | null>>([])
  const view = useMemo(
    () => buildNodeLibraryView(definitions, { query, scope, recentNodeKeys }),
    [definitions, query, scope, recentNodeKeys],
  )
  const orderedDefinitions = useMemo(
    () => view.sections.flatMap((section) => section.definitions),
    [view.sections],
  )
  itemRefs.current.length = orderedDefinitions.length

  const focusItem = (index: number) => {
    if (orderedDefinitions.length === 0) return
    itemRefs.current[
      (index + orderedDefinitions.length) % orderedDefinitions.length
    ]?.focus()
  }

  const handleItemKeyDown = (
    event: KeyboardEvent<HTMLButtonElement>,
    index: number,
    definition: NodeDefinition,
  ) => {
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

  const beginDrag = (
    event: DragEvent<HTMLButtonElement>,
    definition: NodeDefinition,
  ) => {
    event.dataTransfer.effectAllowed = 'copy'
    event.dataTransfer.setData(
      NODE_DEFINITION_MIME,
      nodeDefinitionKey(definition),
    )
  }

  return (
    <aside
      className="studio-drawer left node-library-drawer"
      role="dialog"
      aria-label="节点库"
    >
      <header className="node-library-heading">
        <div>
          <span className="node-category">节点目录</span>
          <h2>添加节点</h2>
        </div>
        <button type="button" aria-label="关闭节点库" onClick={onClose}>
          ×
        </button>
      </header>
      <nav className="node-library-categories" aria-label="节点分类">
        <button
          type="button"
          aria-pressed={scope.kind === 'all'}
          onClick={() => setScope({ kind: 'all' })}
        >
          全部
        </button>
        <button
          type="button"
          aria-pressed={scope.kind === 'recent'}
          onClick={() => setScope({ kind: 'recent' })}
        >
          最近
        </button>
        {view.categories.map((category) => (
          <button
            key={category}
            type="button"
            aria-pressed={
              scope.kind === 'category' && scope.category === category
            }
            onClick={() => setScope({ kind: 'category', category })}
          >
            {category}
          </button>
        ))}
      </nav>
      <div className="node-library-catalog">
        <label className="search-field">
          搜索节点
          <input
            autoFocus
            value={query}
            onChange={(event) => setQuery(event.currentTarget.value)}
            onKeyDown={(event) => {
              if (event.key === 'ArrowDown') {
                event.preventDefault()
                focusItem(0)
              } else if (event.key === 'Escape') {
                event.preventDefault()
                onClose()
              }
            }}
          />
        </label>
        <output className="sr-status" role="status" aria-live="polite">
          当前显示 {view.count} 个节点
        </output>
        {error && (
          <p className="node-library-error" role="alert">
            {error}
          </p>
        )}
        {view.sections.map((section) => (
          <section key={section.id}>
            <h3>{section.label}</h3>
            <div className="node-library-grid">
              {section.definitions.map((definition) => {
                const index = orderedDefinitions.indexOf(definition)
                return (
                  <button
                    key={nodeDefinitionKey(definition)}
                    ref={(element) => {
                      itemRefs.current[index] = element
                    }}
                    className="library-node"
                    type="button"
                    draggable
                    onDragStart={(event) => beginDrag(event, definition)}
                    onClick={() => onAdd(definition)}
                    onKeyDown={(event) =>
                      handleItemKeyDown(event, index, definition)
                    }
                  >
                    <strong>{definition.title}</strong>
                    <small className="library-node-description">
                      {definition.description}
                    </small>
                    <small className="package-summary">
                      {definition.package.displayName}
                      {definition.package.version
                        ? ` · ${definition.package.version}`
                        : ''}
                    </small>
                  </button>
                )
              })}
            </div>
          </section>
        ))}
        {view.sections.length === 0 && (
          <div className="node-library-empty">
            <p>
              {query
                ? '没有匹配的节点'
                : scope.kind === 'recent'
                  ? '暂无最近使用的节点'
                  : '当前分类没有可用节点'}
            </p>
            {query && (
              <button type="button" onClick={() => setQuery('')}>
                清除搜索
              </button>
            )}
          </div>
        )}
      </div>
    </aside>
  )
}
