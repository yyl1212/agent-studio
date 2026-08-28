import type { NodeDefinition } from '../../lib/api/client'

export const NODE_DEFINITION_MIME = 'application/x-agent-studio-node'
export const RECENT_NODE_STORAGE_KEY = 'agent-studio.node-library.recent.v1'
export const MAX_RECENT_NODES = 6

export type PortDataType = NodeDefinition['inputs'][number]['type']

export interface NodeLibraryCompatibility {
  sourceType: PortDataType
}

export type NodeLibraryScope =
  | { kind: 'all' }
  | { kind: 'recent' }
  | { kind: 'category'; category: string }

export interface NodeLibrarySection {
  id: string
  label: string
  definitions: NodeDefinition[]
}

export interface NodeLibraryView {
  categories: string[]
  sections: NodeLibrarySection[]
  count: number
}

interface StorageLike {
  getItem: (key: string) => string | null
  setItem: (key: string, value: string) => void
}

interface ViewOptions {
  query: string
  scope: NodeLibraryScope
  recentNodeKeys: string[]
  compatibility?: NodeLibraryCompatibility
}

export function nodeDefinitionKey(definition: Pick<NodeDefinition, 'type' | 'version'>) {
  return `${definition.type}@${definition.version}`
}

export function isAddableDefinition(definition: NodeDefinition) {
  return definition.type !== 'start' && definition.type !== 'end'
}

export function compatibleInputPorts(
  definition: NodeDefinition,
  sourceType: PortDataType,
) {
  return definition.inputs.filter(
    (input) =>
      sourceType === 'any' ||
      input.type === 'any' ||
      input.type === sourceType,
  )
}

export function buildNodeLibraryView(
  definitions: NodeDefinition[],
  options: ViewOptions,
): NodeLibraryView {
  const addable = definitions
    .filter(isAddableDefinition)
    .filter(
      (definition) =>
        !options.compatibility ||
        compatibleInputPorts(definition, options.compatibility.sourceType).length > 0,
    )
  const categories = [...new Set(addable.map((definition) => definition.category))]
  const byKey = new Map(
    addable.map((definition) => [nodeDefinitionKey(definition), definition]),
  )
  const recent = [...new Set(options.recentNodeKeys)]
    .map((key) => byKey.get(key))
    .filter((definition): definition is NodeDefinition => Boolean(definition))
  const query = options.query.trim().toLocaleLowerCase()

  if (query) {
    const matches = addable.filter((definition) =>
      [
        definition.title,
        definition.description,
        definition.type,
        definition.category,
        definition.package.name,
        definition.package.displayName,
      ]
        .join(' ')
        .toLocaleLowerCase()
        .includes(query),
    )

    return {
      categories,
      sections: matches.length
        ? [{ id: 'search', label: '搜索结果', definitions: matches }]
        : [],
      count: matches.length,
    }
  }

  if (options.scope.kind === 'recent') {
    return {
      categories,
      sections: recent.length
        ? [{ id: 'recent', label: '最近使用', definitions: recent }]
        : [],
      count: recent.length,
    }
  }

  let scoped = addable
  if (options.scope.kind === 'category') {
    const category = options.scope.category
    scoped = addable.filter((definition) => definition.category === category)
  }
  const recentKeys =
    options.scope.kind === 'all'
      ? new Set(recent.map(nodeDefinitionKey))
      : new Set<string>()
  const grouped = new Map<string, NodeDefinition[]>()

  for (const definition of scoped) {
    if (recentKeys.has(nodeDefinitionKey(definition))) continue
    grouped.set(definition.category, [
      ...(grouped.get(definition.category) ?? []),
      definition,
    ])
  }

  const sections: NodeLibrarySection[] =
    options.scope.kind === 'all' && recent.length
      ? [{ id: 'recent', label: '最近使用', definitions: recent }]
      : []
  for (const [category, items] of grouped) {
    sections.push({
      id: `category:${category}`,
      label: category,
      definitions: items,
    })
  }

  return { categories, sections, count: scoped.length }
}

export function readRecentNodeKeys(
  storage: StorageLike | undefined = browserStorage(),
): string[] {
  if (!storage) return []

  try {
    const parsed: unknown = JSON.parse(
      storage.getItem(RECENT_NODE_STORAGE_KEY) ?? '[]',
    )
    if (!Array.isArray(parsed)) return []
    return [
      ...new Set(
        parsed.filter((value): value is string => typeof value === 'string'),
      ),
    ].slice(0, MAX_RECENT_NODES)
  } catch {
    return []
  }
}

export function rememberRecentNodeKey(
  current: string[],
  key: string,
  storage: StorageLike | undefined = browserStorage(),
): string[] {
  if (!storage) return []

  const next = [key, ...current.filter((item) => item !== key)].slice(
    0,
    MAX_RECENT_NODES,
  )
  try {
    storage.setItem(RECENT_NODE_STORAGE_KEY, JSON.stringify(next))
    return next
  } catch {
    return []
  }
}

function browserStorage(): StorageLike | undefined {
  if (typeof window === 'undefined') return undefined
  try {
    return window.localStorage
  } catch {
    return undefined
  }
}
