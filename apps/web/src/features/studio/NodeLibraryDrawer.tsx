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
  compatibleInputPorts,
  NODE_DEFINITION_MIME,
  nodeDefinitionKey,
  type NodeLibraryCompatibility,
  type NodeLibraryScope,
} from './nodeLibraryModel'
import { NodeIcon } from './NodeIcon'

export interface NodeLibrarySelection {
  definition: NodeDefinition
  targetPortKey?: string
}

export interface NodeLibraryDrawerProps {
  definitions: NodeDefinition[]
  recentNodeKeys: string[]
  compatibility?: NodeLibraryCompatibility
  error?: string
  onAdd: (selection: NodeLibrarySelection) => void
  onClose: () => void
}

export function NodeLibraryDrawer({
  definitions,
  recentNodeKeys,
  compatibility,
  error,
  onAdd,
  onClose,
}: NodeLibraryDrawerProps) {
  const [query, setQuery] = useState('')
  const [scope, setScope] = useState<NodeLibraryScope>({ kind: 'all' })
  const [pendingDefinition, setPendingDefinition] = useState<NodeDefinition>()
  const [selectedPortKey, setSelectedPortKey] = useState('')
  const searchRef = useRef<HTMLInputElement>(null)
  const itemRefs = useRef<Array<HTMLButtonElement | null>>([])
  const view = useMemo(
    () => buildNodeLibraryView(definitions, { query, scope, recentNodeKeys, compatibility }),
    [compatibility, definitions, query, scope, recentNodeKeys],
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

  const returnToCatalog = () => {
    setPendingDefinition(undefined)
    setSelectedPortKey('')
    queueMicrotask(() => searchRef.current?.focus())
  }

  const chooseDefinition = (definition: NodeDefinition) => {
    if (!compatibility) {
      onAdd({ definition })
      return
    }
    const ports = compatibleInputPorts(definition, compatibility.sourceType)
    if (ports.length === 1) {
      onAdd({ definition, targetPortKey: ports[0].key })
      return
    }
    setPendingDefinition(definition)
    setSelectedPortKey('')
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
      chooseDefinition(definition)
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
      onKeyDownCapture={(event) => {
        if (event.key !== 'Escape') return
        event.preventDefault()
        event.stopPropagation()
        if (pendingDefinition) {
          returnToCatalog()
        } else {
          onClose()
        }
      }}
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
      {!pendingDefinition && <nav className="node-library-categories" aria-label="节点分类">
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
      </nav>}
      <div className="node-library-catalog">
        {pendingDefinition ? (
          <section className="node-port-selection" aria-labelledby="node-port-selection-title">
            <button type="button" className="secondary" onClick={returnToCatalog}>
              ← 返回节点目录
            </button>
            <div className="node-port-selection-heading">
              <NodeIcon category={pendingDefinition.category} decorative />
              <div>
                <span className="node-category">{pendingDefinition.title}</span>
                <h3 id="node-port-selection-title">选择输入端口</h3>
              </div>
            </div>
            <p>该节点有多个兼容输入，请选择本次连线的目标端口。</p>
            <fieldset>
              <legend>兼容输入端口</legend>
              {compatibility && compatibleInputPorts(pendingDefinition, compatibility.sourceType).map((port, index) => (
                <label key={port.key} className="node-port-option">
                  <input
                    autoFocus={index === 0}
                    type="radio"
                    name="target-port"
                    value={port.key}
                    checked={selectedPortKey === port.key}
                    onChange={() => setSelectedPortKey(port.key)}
                  />
                  <span>{port.title}</span>
                  <small>{port.key} · {port.type}</small>
                </label>
              ))}
            </fieldset>
            <button
              type="button"
              className="primary"
              disabled={!selectedPortKey}
              onClick={() => {
                if (!selectedPortKey) return
                onAdd({ definition: pendingDefinition, targetPortKey: selectedPortKey })
              }}
            >
              添加并连接
            </button>
          </section>
        ) : <>
        <label className="search-field">
          搜索节点
          <input
            ref={searchRef}
            autoFocus
            value={query}
            onChange={(event) => setQuery(event.currentTarget.value)}
            onKeyDown={(event) => {
              if (event.key === 'ArrowDown') {
                event.preventDefault()
                focusItem(0)
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
                    onClick={() => chooseDefinition(definition)}
                    onKeyDown={(event) =>
                      handleItemKeyDown(event, index, definition)
                    }
                  >
                    <span className="library-node-heading">
                      <NodeIcon category={definition.category} decorative />
                      <span>
                        <strong>{definition.title}</strong>
                        <small>{definition.type}@{definition.version}</small>
                      </span>
                    </span>
                    <small className="library-node-description">
                      {definition.description}
                    </small>
                    <small className="library-node-ports">
                      {definition.inputs.length} 输入 · {definition.outputs.length} 输出
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
        </>}
      </div>
    </aside>
  )
}
