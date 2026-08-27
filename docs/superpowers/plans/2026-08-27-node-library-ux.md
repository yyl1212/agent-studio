# Agent Studio 节点库添加体验优化 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 Studio 节点库升级为支持分类、最近使用、点击中心添加和桌面精确拖放的分层侧边节点库，同时保持现有后端与节点契约不变。

**Architecture:** 在前端新增目录视图模型与放置算法两个纯逻辑模块；`NodeLibraryDrawer` 只负责查找和产生节点选择/拖拽载荷，`WorkflowCanvas` 只负责把拖放屏幕坐标转换为画布坐标，`StudioPage` 统一完成节点创建、最近记录、保存和打开配置。现有 `SaveQueue`、React Flow 受控图状态和 Studio 浮层编排保持为稳定边界。

**Tech Stack:** React 19、TypeScript 5.9、Vite 8、Vitest 4、Testing Library、React Flow 12、Playwright、CSS、浏览器 `localStorage` 与原生 HTML Drag and Drop。

**Spec:** `docs/superpowers/specs/2026-08-27-node-library-ux-design.md`

## Global Constraints

- 以 `origin/main@2eb6d7b` 及其后续最新提交为基线，在 `codex/node-library-ux` 分支开发；开始产品代码前必须拉取最新 `origin/main`。
- 仅修改 Web Studio；不得修改 Go 后端、PostgreSQL、`contracts/openapi.yaml`、节点 SDK、节点包清单或工作流 JSON。
- 不增加运行时依赖、全局状态库、拖拽框架、收藏、推荐权重、服务端最近使用或本地 RAG。
- 可添加目录继续排除 `start` 和 `end`；搜索字段固定为标题、描述、类型、包名和包显示名。
- 最近使用固定保存 6 个 `type@version`，存储键固定为 `agent-studio.node-library.recent.v1`；任何读写异常都必须降级为空列表，不能阻塞节点添加。
- 拖拽 MIME 固定为 `application/x-agent-studio-node`，载荷只包含 `type@version`。
- 点击继续使用当前视口中心和现有防重叠行为；拖放固定吸附到 20px 网格，空闲目标单轴误差最多 10px。
- 点击和拖拽必须复用同一个页面级创建函数：只创建一个节点、只进入一次 `SaveQueue`、只更新一次最近使用，并立即打开新节点配置。
- 触摸和 `< 640px` 窄屏不依赖原生拖放，点击添加必须完整可用；不得产生页面级横向滚动。
- 所有功能按 TDD 顺序完成；Go/仓库级验证命令必须带 `CGO_ENABLED=0`，全量回归使用 Docker PostgreSQL。
- 每个任务提交前运行其定向测试；全部开发完成后必须做代码审查、全量回归并通过 GitHub PR 合并。

---

### Task 1: 节点目录视图模型与最近使用存储

**Files:**
- Create: `apps/web/src/features/studio/nodeLibraryModel.ts`
- Test: `apps/web/src/features/studio/nodeLibraryModel.test.ts`

**Interfaces:**
- Consumes: `NodeDefinition` from `apps/web/src/lib/api/client.ts`；浏览器兼容的 `getItem/setItem` 存储接口。
- Produces: `NODE_DEFINITION_MIME`、`RECENT_NODE_STORAGE_KEY`、`NodeLibraryScope`、`NodeLibraryView`、`nodeDefinitionKey()`、`buildNodeLibraryView()`、`readRecentNodeKeys()`、`rememberRecentNodeKey()`；Tasks 3–5 必须直接复用这些名字和类型。

- [ ] **Step 1: 核对分支并同步最新主线**

Run:

```bash
git status --short --branch
git branch --show-current
git fetch origin main
git rebase origin/main
```

Expected: 当前分支为 `codex/node-library-ux`，工作区无未提交修改；rebase 完成后设计和计划提交位于最新 `origin/main` 之上。

- [ ] **Step 2: 编写目录派生和搜索失败测试**

在 `nodeLibraryModel.test.ts` 先写以下测试骨架；测试夹具的 `package`、`capabilities` 和 `executionSafety` 字段必须完整，避免用不安全的强制转换掩盖类型错误：

```ts
import { describe, expect, it } from 'vitest'

import type { NodeDefinition } from '../../lib/api/client'
import {
  buildNodeLibraryView,
  nodeDefinitionKey,
  readRecentNodeKeys,
  RECENT_NODE_STORAGE_KEY,
  rememberRecentNodeKey,
  type NodeLibraryScope,
} from './nodeLibraryModel'

const definition = (type: string, category: string, overrides: Partial<NodeDefinition> = {}): NodeDefinition => ({
  type,
  version: '1.0.0',
  title: type,
  description: `${type} 描述`,
  category,
  configSchema: {},
  inputs: [],
  outputs: [],
  capabilities: [],
  executionSafety: 'pure',
  package: {
    name: `example.com/${type}`,
    displayName: `${category} Package`,
    version: 'v1.0.0',
    license: 'Apache-2.0',
    repository: 'https://example.com/repository',
    source: 'module',
  },
  ...overrides,
})

const all: NodeLibraryScope = { kind: 'all' }

describe('buildNodeLibraryView', () => {
  it('排除开始和结束节点，并按首次出现顺序派生分类', () => {
    const view = buildNodeLibraryView([
      definition('start', '流程'),
      definition('template', '文本'),
      definition('llm', 'AI'),
      definition('end', '流程'),
    ], { query: '', scope: all, recentNodeKeys: [] })

    expect(view.categories).toEqual(['文本', 'AI'])
    expect(view.sections.map((section) => section.label)).toEqual(['文本', 'AI'])
    expect(view.count).toBe(2)
  })

  it('跨分类搜索标题、描述、类型、包名和包显示名', () => {
    const definitions = [
      definition('template', '文本', { title: '提示词模板' }),
      definition('extension.webhook', '扩展', { description: '发送 JSON POST' }),
    ]
    expect(buildNodeLibraryView(definitions, { query: 'json post', scope: { kind: 'category', category: '文本' }, recentNodeKeys: [] }).count).toBe(1)
    expect(buildNodeLibraryView(definitions, { query: 'example.com/template', scope: all, recentNodeKeys: [] }).count).toBe(1)
    expect(buildNodeLibraryView(definitions, { query: '扩展 package', scope: all, recentNodeKeys: [] }).count).toBe(1)
  })

  it('全部视图把最近项置顶且不在后续分类中重复', () => {
    const template = definition('template', '文本')
    const llm = definition('llm', 'AI')
    const view = buildNodeLibraryView([template, llm], {
      query: '',
      scope: all,
      recentNodeKeys: [nodeDefinitionKey(template), nodeDefinitionKey(template), 'removed@1'],
    })

    expect(view.sections[0]).toEqual(expect.objectContaining({ label: '最近使用', definitions: [template] }))
    expect(view.sections.flatMap((section) => section.definitions).filter((item) => item.type === 'template')).toHaveLength(1)
    expect(view.count).toBe(2)
  })

  it('使用 50 个定义时在当前调用内同步返回过滤结果', () => {
    const definitions = Array.from({ length: 50 }, (_, index) => definition(`node-${index}`, index % 2 === 0 ? '文本' : 'AI'))
    const view = buildNodeLibraryView(definitions, { query: 'node-49', scope: all, recentNodeKeys: [] })
    expect(view.sections).toHaveLength(1)
    expect(view.sections[0].definitions.map((item) => item.type)).toEqual(['node-49'])
  })
})
```

- [ ] **Step 3: 编写最近使用和存储降级失败测试**

继续在同一测试文件加入以下测试；`readRecentNodeKeys`、`RECENT_NODE_STORAGE_KEY` 和 `rememberRecentNodeKey` 已在文件顶部的同一个导入块中引入，不要创建重复导入：

```ts
it('最近使用去重前置并裁剪为 6 项', () => {
  const written = new Map<string, string>()
  const storage = {
    getItem: (key: string) => written.get(key) ?? null,
    setItem: (key: string, value: string) => { written.set(key, value) },
  }
  const recent = rememberRecentNodeKey(['a@1', 'b@1', 'c@1', 'd@1', 'e@1', 'f@1'], 'c@1', storage)
  expect(recent).toEqual(['c@1', 'a@1', 'b@1', 'd@1', 'e@1', 'f@1'])
  expect(JSON.parse(written.get(RECENT_NODE_STORAGE_KEY) ?? '[]')).toEqual(recent)
})

it('读取时过滤非字符串并裁剪为 6 项', () => {
  const storage = {
    getItem: () => JSON.stringify(['a@1', 3, 'a@1', 'b@1', 'c@1', 'd@1', 'e@1', 'f@1', 'g@1']),
    setItem: () => undefined,
  }
  expect(readRecentNodeKeys(storage)).toEqual(['a@1', 'b@1', 'c@1', 'd@1', 'e@1', 'f@1'])
})

it('存储读取、解析或写入失败时降级为空列表', () => {
  const readFailure = { getItem: () => { throw new Error('blocked') }, setItem: () => undefined }
  const parseFailure = { getItem: () => '{bad json', setItem: () => undefined }
  const writeFailure = { getItem: () => null, setItem: () => { throw new Error('quota') } }
  expect(readRecentNodeKeys(readFailure)).toEqual([])
  expect(readRecentNodeKeys(parseFailure)).toEqual([])
  expect(rememberRecentNodeKey([], 'template@1', writeFailure)).toEqual([])
})
```

- [ ] **Step 4: 运行测试并确认按预期失败**

Run:

```bash
corepack pnpm@10.34.5 --filter @agent-studio/web test -- src/features/studio/nodeLibraryModel.test.ts
```

Expected: FAIL，错误指向 `./nodeLibraryModel` 不存在或缺少导出。

- [ ] **Step 5: 实现最小目录模型和存储保护**

创建 `nodeLibraryModel.ts`，实现以下完整接口。`buildNodeLibraryView` 的“全部”视图必须从分类区排除已经进入最近区的项，保证键盘顺序和视觉顺序都没有重复卡片：

```ts
import type { NodeDefinition } from '../../lib/api/client'

export const NODE_DEFINITION_MIME = 'application/x-agent-studio-node'
export const RECENT_NODE_STORAGE_KEY = 'agent-studio.node-library.recent.v1'
export const MAX_RECENT_NODES = 6

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
}

export function nodeDefinitionKey(definition: Pick<NodeDefinition, 'type' | 'version'>) {
  return `${definition.type}@${definition.version}`
}

export function buildNodeLibraryView(definitions: NodeDefinition[], options: ViewOptions): NodeLibraryView {
  const addable = definitions.filter((definition) => definition.type !== 'start' && definition.type !== 'end')
  const categories = [...new Set(addable.map((definition) => definition.category))]
  const byKey = new Map(addable.map((definition) => [nodeDefinitionKey(definition), definition]))
  const recent = [...new Set(options.recentNodeKeys)].map((key) => byKey.get(key)).filter((definition): definition is NodeDefinition => Boolean(definition))
  const query = options.query.trim().toLocaleLowerCase()

  if (query) {
    const matches = addable.filter((definition) => [
      definition.title,
      definition.description,
      definition.type,
      definition.package.name,
      definition.package.displayName,
    ].join(' ').toLocaleLowerCase().includes(query))
    return { categories, sections: matches.length ? [{ id: 'search', label: '搜索结果', definitions: matches }] : [], count: matches.length }
  }

  if (options.scope.kind === 'recent') {
    return { categories, sections: recent.length ? [{ id: 'recent', label: '最近使用', definitions: recent }] : [], count: recent.length }
  }

  const scoped = options.scope.kind === 'category'
    ? addable.filter((definition) => definition.category === options.scope.category)
    : addable
  const recentKeys = options.scope.kind === 'all' ? new Set(recent.map(nodeDefinitionKey)) : new Set<string>()
  const grouped = new Map<string, NodeDefinition[]>()
  for (const definition of scoped) {
    if (recentKeys.has(nodeDefinitionKey(definition))) continue
    grouped.set(definition.category, [...(grouped.get(definition.category) ?? []), definition])
  }
  const sections: NodeLibrarySection[] = options.scope.kind === 'all' && recent.length
    ? [{ id: 'recent', label: '最近使用', definitions: recent }]
    : []
  for (const [category, items] of grouped) sections.push({ id: `category:${category}`, label: category, definitions: items })
  return { categories, sections, count: scoped.length }
}

export function readRecentNodeKeys(storage: StorageLike | undefined = browserStorage()): string[] {
  if (!storage) return []
  try {
    const parsed: unknown = JSON.parse(storage.getItem(RECENT_NODE_STORAGE_KEY) ?? '[]')
    if (!Array.isArray(parsed)) return []
    return [...new Set(parsed.filter((value): value is string => typeof value === 'string'))].slice(0, MAX_RECENT_NODES)
  } catch {
    return []
  }
}

export function rememberRecentNodeKey(current: string[], key: string, storage: StorageLike | undefined = browserStorage()): string[] {
  if (!storage) return []
  const next = [key, ...current.filter((item) => item !== key)].slice(0, MAX_RECENT_NODES)
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
```

- [ ] **Step 6: 运行目录模型测试并确认通过**

Run:

```bash
corepack pnpm@10.34.5 --filter @agent-studio/web test -- src/features/studio/nodeLibraryModel.test.ts
```

Expected: PASS，所有目录、50 项搜索、最近使用和异常降级测试通过。

- [ ] **Step 7: 提交目录模型**

```bash
git add apps/web/src/features/studio/nodeLibraryModel.ts apps/web/src/features/studio/nodeLibraryModel.test.ts
git commit -m "feat(web): add node library view model"
```

---

### Task 2: 可测试的节点放置算法

**Files:**
- Create: `apps/web/src/features/studio/nodePlacement.ts`
- Test: `apps/web/src/features/studio/nodePlacement.test.ts`
- Modify: `apps/web/src/features/studio/StudioPage.tsx:1-9,157-177,562-569`
- Modify: `apps/web/src/features/studio/StudioPage.test.tsx:1-9,289-302`

**Interfaces:**
- Consumes: React Flow `XYPosition` 和 Studio `StudioNode[]`。
- Produces: `NODE_PLACEMENT_GRID = 20`、`availableNodePosition(center, nodes)`（保持现有点击行为）、`dropNodePosition(point, nodes)`（20px 吸附和确定性非重叠）；Task 5 使用这两个导出。

- [ ] **Step 1: 为现有点击行为和新增拖放行为写失败测试**

创建 `nodePlacement.test.ts`：

```ts
import { describe, expect, it } from 'vitest'

import { availableNodePosition, dropNodePosition, snapNodePosition } from './nodePlacement'
import type { StudioNode } from './types'

const nodeAt = (x: number, y: number): StudioNode => ({
  id: `${x}-${y}`,
  type: 'studio',
  position: { x, y },
  data: { nodeType: 'template', typeVersion: '1', config: {}, ports: { inputs: [], outputs: [] }, issues: [] },
})

describe('nodePlacement', () => {
  it('保持点击添加按 190px 向下寻找空位的现有行为', () => {
    expect(availableNodePosition({ x: 320, y: 260 }, [nodeAt(320, 260)])).toEqual({ x: 320, y: 450 })
  })

  it('把自由拖放位置吸附到 20px 网格且单轴误差不超过 10px', () => {
    expect(snapNodePosition({ x: 111, y: 249 })).toEqual({ x: 120, y: 240 })
    expect(dropNodePosition({ x: 111, y: 249 }, [])).toEqual({ x: 120, y: 240 })
  })

  it('完全重叠时按确定性顺序选择最近的非重叠网格', () => {
    expect(dropNodePosition({ x: 120, y: 240 }, [nodeAt(120, 240)])).toEqual({ x: 120, y: 420 })
    expect(dropNodePosition({ x: 120, y: 240 }, [nodeAt(120, 240), nodeAt(120, 420)])).toEqual({ x: 120, y: 60 })
  })
})
```

同时从 `StudioPage.test.tsx` 删除 `availableNodePosition` 的页面级纯逻辑测试和对应导入；该行为改由新模块测试拥有。

- [ ] **Step 2: 运行放置测试并确认失败**

Run:

```bash
corepack pnpm@10.34.5 --filter @agent-studio/web test -- src/features/studio/nodePlacement.test.ts
```

Expected: FAIL，`./nodePlacement` 不存在。

- [ ] **Step 3: 提取现有算法并实现 20px 拖放算法**

创建 `nodePlacement.ts`：

```ts
import type { XYPosition } from '@xyflow/react'

import type { StudioNode } from './types'

export const NODE_PLACEMENT_GRID = 20
const COLLISION_X = 280
const COLLISION_Y = 170

export function availableNodePosition(center: XYPosition, nodes: StudioNode[]): XYPosition {
  for (let step = 0; step <= nodes.length; step += 1) {
    const candidate = { x: center.x, y: center.y + step * 190 }
    if (!isOccupied(candidate, nodes)) return candidate
  }
  return { x: center.x, y: center.y + (nodes.length + 1) * 190 }
}

export function snapNodePosition(position: XYPosition): XYPosition {
  return {
    x: Math.round(position.x / NODE_PLACEMENT_GRID) * NODE_PLACEMENT_GRID,
    y: Math.round(position.y / NODE_PLACEMENT_GRID) * NODE_PLACEMENT_GRID,
  }
}

export function dropNodePosition(position: XYPosition, nodes: StudioNode[]): XYPosition {
  const origin = snapNodePosition(position)
  if (!isOccupied(origin, nodes)) return origin
  const stepX = Math.ceil(COLLISION_X / NODE_PLACEMENT_GRID) * NODE_PLACEMENT_GRID
  const stepY = Math.ceil(COLLISION_Y / NODE_PLACEMENT_GRID) * NODE_PLACEMENT_GRID
  for (let ring = 1; ring <= nodes.length + 1; ring += 1) {
    const candidates = [
      { x: origin.x, y: origin.y + stepY * ring },
      { x: origin.x, y: origin.y - stepY * ring },
      { x: origin.x + stepX * ring, y: origin.y },
      { x: origin.x - stepX * ring, y: origin.y },
      { x: origin.x + stepX * ring, y: origin.y + stepY * ring },
      { x: origin.x - stepX * ring, y: origin.y + stepY * ring },
      { x: origin.x + stepX * ring, y: origin.y - stepY * ring },
      { x: origin.x - stepX * ring, y: origin.y - stepY * ring },
    ]
    const available = candidates.find((candidate) => !isOccupied(candidate, nodes))
    if (available) return available
  }
  return origin
}

function isOccupied(candidate: XYPosition, nodes: StudioNode[]) {
  return nodes.some((node) => Math.abs(node.position.x - candidate.x) < COLLISION_X && Math.abs(node.position.y - candidate.y) < COLLISION_Y)
}
```

从 `StudioPage.tsx` 删除文件尾部的 `availableNodePosition` 实现，改为从 `./nodePlacement` 导入；此任务暂时仍让现有点击 `addNode` 调用导入函数，不在本任务改变页面行为。

- [ ] **Step 4: 运行放置和页面回归测试**

Run:

```bash
corepack pnpm@10.34.5 --filter @agent-studio/web test -- src/features/studio/nodePlacement.test.ts src/features/studio/StudioPage.test.tsx
```

Expected: PASS；现有点击中心、防重叠和 Studio 页面测试无回归。

- [ ] **Step 5: 提交放置模块**

```bash
git add apps/web/src/features/studio/nodePlacement.ts apps/web/src/features/studio/nodePlacement.test.ts apps/web/src/features/studio/StudioPage.tsx apps/web/src/features/studio/StudioPage.test.tsx
git commit -m "refactor(web): isolate node placement rules"
```

---

### Task 3: 分层节点库组件与拖拽源

**Files:**
- Modify: `apps/web/src/features/studio/NodeLibraryDrawer.tsx:1-73`
- Modify: `apps/web/src/features/studio/NodeLibraryDrawer.test.tsx:1-85`

**Interfaces:**
- Consumes: Task 1 的 `buildNodeLibraryView()`、`nodeDefinitionKey()`、`NODE_DEFINITION_MIME`、`NodeLibraryScope`；新增必填 prop `recentNodeKeys: string[]` 和可选 prop `error?: string`。
- Produces: 可分类、搜索、键盘选择并写入拖拽载荷的 `NodeLibraryDrawer`；仍通过 `onAdd(definition)` 处理点击，不创建图节点。

- [ ] **Step 1: 更新现有测试调用并写分类/最近视图失败测试**

先给所有已有 `NodeLibraryDrawer` 渲染调用加入 `recentNodeKeys={[]}`，再增加：

```tsx
it('按分类浏览并在搜索时跨分类匹配，清除后恢复原分类', async () => {
  render(<NodeLibraryDrawer definitions={[
    definition(undefined, { type: 'template', title: '提示词模板', category: '文本' }),
    definition(undefined, { type: 'llm', title: 'LLM', category: 'AI' }),
  ]} recentNodeKeys={[]} onAdd={vi.fn()} onClose={vi.fn()} />)

  await userEvent.click(screen.getByRole('button', { name: '文本' }))
  expect(screen.getByRole('button', { name: /^提示词模板/ })).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /^LLM/ })).not.toBeInTheDocument()

  await userEvent.type(screen.getByLabelText('搜索节点'), 'LLM')
  expect(screen.getByRole('button', { name: /^LLM/ })).toBeInTheDocument()
  await userEvent.clear(screen.getByLabelText('搜索节点'))
  expect(screen.getByRole('button', { name: /^提示词模板/ })).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /^LLM/ })).not.toBeInTheDocument()
})

it('全部视图优先显示最近使用且不重复卡片', () => {
  const template = definition(undefined, { type: 'template', version: '1', title: '提示词模板', category: '文本' })
  render(<NodeLibraryDrawer definitions={[template]} recentNodeKeys={['template@1']} onAdd={vi.fn()} onClose={vi.fn()} />)
  expect(screen.getByRole('heading', { name: '最近使用' })).toBeInTheDocument()
  expect(screen.getAllByRole('button', { name: /^提示词模板/ })).toHaveLength(1)
})

it('最近分类没有有效记录时显示明确空状态', async () => {
  render(<NodeLibraryDrawer definitions={[definition()]} recentNodeKeys={['removed@1']} onAdd={vi.fn()} onClose={vi.fn()} />)
  await userEvent.click(screen.getByRole('button', { name: '最近' }))
  expect(screen.getByText('暂无最近使用的节点')).toBeInTheDocument()
})
```

- [ ] **Step 2: 写拖拽载荷、结果播报和错误提示失败测试**

```tsx
it('卡片拖拽只写入固定 MIME 的 type@version 载荷', () => {
  const setData = vi.fn()
  render(<NodeLibraryDrawer definitions={[definition(undefined, { type: 'template', version: '1', title: '提示词模板' })]} recentNodeKeys={[]} onAdd={vi.fn()} onClose={vi.fn()} />)
  const card = screen.getByRole('button', { name: /^提示词模板/ })
  fireEvent.dragStart(card, { dataTransfer: { setData, effectAllowed: 'none' } })
  expect(setData).toHaveBeenCalledWith('application/x-agent-studio-node', 'template@1')
})

it('播报搜索结果数量和页面传入的拖放错误', async () => {
  render(<NodeLibraryDrawer definitions={[definition()]} recentNodeKeys={[]} error="节点定义已更新，请重新选择" onAdd={vi.fn()} onClose={vi.fn()} />)
  expect(screen.getByRole('status')).toHaveTextContent('当前显示 1 个节点')
  expect(screen.getByRole('alert')).toHaveTextContent('节点定义已更新，请重新选择')
  await userEvent.type(screen.getByLabelText('搜索节点'), 'missing')
  expect(screen.getByRole('status')).toHaveTextContent('当前显示 0 个节点')
  expect(screen.getByRole('button', { name: '清除搜索' })).toBeInTheDocument()
})
```

若夹具标题不是 `Search`，让拖拽测试的角色查询与夹具实际标题一致；不要用 `document.querySelector` 绕过可访问名称。

- [ ] **Step 3: 运行组件测试并确认失败**

Run:

```bash
corepack pnpm@10.34.5 --filter @agent-studio/web test -- src/features/studio/NodeLibraryDrawer.test.tsx
```

Expected: FAIL，缺少 `recentNodeKeys` 行为、分类按钮、最近区、清除搜索或拖拽载荷。

- [ ] **Step 4: 用 Task 1 视图模型重构组件**

把 React 导入调整为 `useMemo`、`useRef`、`useState`、`type DragEvent` 和 `type KeyboardEvent`；测试文件从 Testing Library 一并导入 `fireEvent`。组件使用以下状态和确定的键盘处理函数：

```tsx
interface NodeLibraryDrawerProps {
  definitions: NodeDefinition[]
  recentNodeKeys: string[]
  error?: string
  onAdd: (definition: NodeDefinition) => void
  onClose: () => void
}

export function NodeLibraryDrawer({ definitions, recentNodeKeys, error, onAdd, onClose }: NodeLibraryDrawerProps) {
  const [query, setQuery] = useState('')
  const [scope, setScope] = useState<NodeLibraryScope>({ kind: 'all' })
  const itemRefs = useRef<Array<HTMLButtonElement | null>>([])
  const view = useMemo(
    () => buildNodeLibraryView(definitions, { query, scope, recentNodeKeys }),
    [definitions, query, scope, recentNodeKeys],
  )
  const orderedDefinitions = useMemo(() => view.sections.flatMap((section) => section.definitions), [view.sections])

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

  const beginDrag = (event: DragEvent<HTMLButtonElement>, definition: NodeDefinition) => {
    event.dataTransfer.effectAllowed = 'copy'
    event.dataTransfer.setData(NODE_DEFINITION_MIME, nodeDefinitionKey(definition))
  }

  return <aside className="studio-drawer left node-library-drawer" role="dialog" aria-label="节点库">
    <header className="node-library-heading">
      <div><span className="node-category">节点目录</span><h2>添加节点</h2></div>
      <button type="button" aria-label="关闭节点库" onClick={onClose}>×</button>
    </header>
    <nav className="node-library-categories" aria-label="节点分类">
      <button type="button" aria-pressed={scope.kind === 'all'} onClick={() => setScope({ kind: 'all' })}>全部</button>
      <button type="button" aria-pressed={scope.kind === 'recent'} onClick={() => setScope({ kind: 'recent' })}>最近</button>
      {view.categories.map((category) => <button key={category} type="button"
        aria-pressed={scope.kind === 'category' && scope.category === category}
        onClick={() => setScope({ kind: 'category', category })}>{category}</button>)}
    </nav>
    <div className="node-library-catalog">
      <label className="search-field">搜索节点<input autoFocus value={query} onChange={(event) => setQuery(event.currentTarget.value)} onKeyDown={(event) => {
        if (event.key === 'ArrowDown') {
          event.preventDefault()
          focusItem(0)
        } else if (event.key === 'Escape') {
          event.preventDefault()
          onClose()
        }
      }} /></label>
      <output className="sr-status" role="status" aria-live="polite">当前显示 {view.count} 个节点</output>
      {error && <p className="node-library-error" role="alert">{error}</p>}
      {view.sections.map((section) => <section key={section.id}>
        <h3>{section.label}</h3>
        <div className="node-library-grid">{section.definitions.map((definition) => {
          const index = orderedDefinitions.indexOf(definition)
          return <button
          key={nodeDefinitionKey(definition)}
          ref={(element) => { itemRefs.current[index] = element }}
          className="library-node"
          type="button"
          draggable
          onDragStart={(event) => beginDrag(event, definition)}
          onClick={() => onAdd(definition)}
          onKeyDown={(event) => handleItemKeyDown(event, index, definition)}
        >
          <strong>{definition.title}</strong>
          <small className="library-node-description">{definition.description}</small>
          <small className="package-summary">{definition.package.displayName}{definition.package.version ? ` · ${definition.package.version}` : ''}</small>
        </button>})}</div>
      </section>)}
      {view.sections.length === 0 && <div className="node-library-empty">
        <p>{query ? '没有匹配的节点' : scope.kind === 'recent' ? '暂无最近使用的节点' : '当前分类没有可用节点'}</p>
        {query && <button type="button" onClick={() => setQuery('')}>清除搜索</button>}
      </div>}
    </div>
  </aside>
}
```

在渲染前执行 `itemRefs.current.length = orderedDefinitions.length`，确保搜索或分类缩短列表后没有指向已隐藏元素的旧 ref。卡片 ref 索引固定使用上面的扁平视觉顺序。

- [ ] **Step 5: 运行组件和目录模型测试**

Run:

```bash
corepack pnpm@10.34.5 --filter @agent-studio/web test -- src/features/studio/nodeLibraryModel.test.ts src/features/studio/NodeLibraryDrawer.test.tsx
```

Expected: PASS；旧搜索/键盘测试和新增分类、最近、拖拽、播报测试全部通过。

- [ ] **Step 6: 提交节点库组件**

```bash
git add apps/web/src/features/studio/NodeLibraryDrawer.tsx apps/web/src/features/studio/NodeLibraryDrawer.test.tsx
git commit -m "feat(web): add layered node library browsing"
```

---

### Task 4: React Flow 画布拖放接收与坐标转换

**Files:**
- Modify: `apps/web/src/features/studio/WorkflowCanvas.tsx:1-118`
- Modify: `apps/web/src/features/studio/WorkflowCanvas.test.tsx:1-105`

**Interfaces:**
- Consumes: Task 1 的 `NODE_DEFINITION_MIME`；React Flow `screenToFlowPosition()`。
- Produces: `WorkflowCanvasHandle.screenToFlowPosition(point): XYPosition | undefined` 和可选 prop `onNodeDefinitionDrop?: (nodeKey: string, position: XYPosition) => void`；Task 5 使用该 prop。只读画布不接受拖放。

- [ ] **Step 1: 扩展 React Flow mock 并写句柄转换失败测试**

在现有 `WorkflowCanvas.test.tsx` 的首个句柄测试中增加：

```tsx
it('通过窄接口转换任意屏幕坐标', () => {
  const ref = createRef<WorkflowCanvasHandle>()
  render(<WorkflowCanvas ref={ref} {...baseProps} nodes={[node('a')]} />)
  expect(ref.current?.screenToFlowPosition({ x: 900, y: 500 })).toEqual({ x: 420, y: 260 })
  expect(screenToFlowPosition).toHaveBeenCalledWith({ x: 900, y: 500 })
})
```

现有 mock 的 `screenToFlowPosition` 已固定返回 `{ x: 420, y: 260 }`，不新增第二套坐标假实现。

- [ ] **Step 2: 写有效、无效和只读拖放失败测试**

```tsx
const transfer = (key: string, types = ['application/x-agent-studio-node']) => ({
  types,
  getData: vi.fn((type: string) => type === 'application/x-agent-studio-node' ? key : ''),
  setData: vi.fn(),
  dropEffect: 'none',
  effectAllowed: 'copy',
}) as unknown as DataTransfer

it('只接收固定 MIME 并上报转换后的画布坐标', () => {
  const onNodeDefinitionDrop = vi.fn()
  render(<WorkflowCanvas {...baseProps} nodes={[node('a')]} onNodeDefinitionDrop={onNodeDefinitionDrop} />)
  const canvas = screen.getByLabelText('工作流画布')
  const dataTransfer = transfer('template@1')
  fireEvent.dragOver(canvas, { dataTransfer })
  expect(canvas).toHaveAttribute('data-node-drop-active', 'true')
  fireEvent.drop(canvas, { dataTransfer, clientX: 900, clientY: 500 })
  expect(onNodeDefinitionDrop).toHaveBeenCalledWith('template@1', { x: 420, y: 260 })
  expect(canvas).toHaveAttribute('data-node-drop-active', 'false')
})

it('忽略错误 MIME、空载荷和只读画布', () => {
  const onNodeDefinitionDrop = vi.fn()
  const { rerender } = render(<WorkflowCanvas {...baseProps} nodes={[node('a')]} onNodeDefinitionDrop={onNodeDefinitionDrop} />)
  const canvas = screen.getByLabelText('工作流画布')
  fireEvent.drop(canvas, { dataTransfer: transfer('template@1', ['text/plain']), clientX: 10, clientY: 10 })
  fireEvent.drop(canvas, { dataTransfer: transfer(''), clientX: 10, clientY: 10 })
  rerender(<WorkflowCanvas {...baseProps} nodes={[node('a')]} onNodeDefinitionDrop={onNodeDefinitionDrop} readOnly />)
  fireEvent.drop(canvas, { dataTransfer: transfer('template@1'), clientX: 10, clientY: 10 })
  expect(onNodeDefinitionDrop).not.toHaveBeenCalled()
})
```

- [ ] **Step 3: 运行画布测试并确认失败**

Run:

```bash
corepack pnpm@10.34.5 --filter @agent-studio/web test -- src/features/studio/WorkflowCanvas.test.tsx
```

Expected: FAIL，缺少句柄方法、drop prop 和 `data-node-drop-active` 状态。

- [ ] **Step 4: 实现受限坐标句柄和拖放事件**

在 `WorkflowCanvas.tsx` 把 React 导入扩展为：

```tsx
import { forwardRef, useCallback, useEffect, useImperativeHandle, useRef, useState, type ComponentProps, type DragEvent } from 'react'
import { NODE_DEFINITION_MIME } from './nodeLibraryModel'
```

在现有 `WorkflowCanvasProps` 的 `onInvalidConnection` 字段后新增这一精确字段：

```tsx
onNodeDefinitionDrop?: (nodeKey: string, position: XYPosition) => void
```

把句柄接口替换为：

```tsx
export interface WorkflowCanvasHandle {
  getViewportCenter: () => XYPosition
  screenToFlowPosition: (point: XYPosition) => XYPosition | undefined
  fitView: () => Promise<boolean>
}
```

在组件中增加 `const [nodeDropActive, setNodeDropActive] = useState(false)`，并让句柄和容器事件共用当前 `instanceRef`：

```tsx
useImperativeHandle(ref, () => ({
  getViewportCenter: () => {
    const instance = instanceRef.current
    const rect = containerRef.current?.getBoundingClientRect()
    if (!instance || !rect) return { x: 320, y: 260 }
    return instance.screenToFlowPosition({ x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 })
  },
  screenToFlowPosition: (point) => instanceRef.current?.screenToFlowPosition(point),
  fitView: () => instanceRef.current?.fitView({ padding: 0.2, maxZoom: 1.2 }) ?? Promise.resolve(false),
}), [])

const acceptsNodeDefinition = (event: DragEvent<HTMLDivElement>) =>
  !props.readOnly && Boolean(props.onNodeDefinitionDrop) && event.dataTransfer.types.includes(NODE_DEFINITION_MIME)

const handleNodeDragOver = (event: DragEvent<HTMLDivElement>) => {
  if (!acceptsNodeDefinition(event)) return
  event.preventDefault()
  event.dataTransfer.dropEffect = 'copy'
  setNodeDropActive(true)
}

const handleNodeDrop = (event: DragEvent<HTMLDivElement>) => {
  setNodeDropActive(false)
  if (!acceptsNodeDefinition(event)) return
  const nodeKey = event.dataTransfer.getData(NODE_DEFINITION_MIME)
  const position = instanceRef.current?.screenToFlowPosition({ x: event.clientX, y: event.clientY })
  if (!nodeKey || !position) return
  event.preventDefault()
  props.onNodeDefinitionDrop?.(nodeKey, position)
}
```

把外层容器改为：

```tsx
<div
  ref={containerRef}
  className="workflow-canvas"
  aria-label="工作流画布"
  data-node-drop-active={nodeDropActive}
  onDragOver={handleNodeDragOver}
  onDragLeave={(event) => {
    if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setNodeDropActive(false)
  }}
  onDrop={handleNodeDrop}
>
```

- [ ] **Step 5: 运行画布及现有 Studio 壳层回归**

Run:

```bash
corepack pnpm@10.34.5 --filter @agent-studio/web test -- src/features/studio/WorkflowCanvas.test.tsx src/features/studio/StudioShell.test.tsx
```

Expected: PASS；视口中心、fitView、连线、删除、焦点和新拖放测试全部通过。

- [ ] **Step 6: 提交画布拖放能力**

```bash
git add apps/web/src/features/studio/WorkflowCanvas.tsx apps/web/src/features/studio/WorkflowCanvas.test.tsx
git commit -m "feat(web): accept node drops on workflow canvas"
```

---

### Task 5: 页面级统一创建、最近记录和错误恢复

**Files:**
- Modify: `apps/web/src/features/studio/StudioPage.tsx:17-34,38-76,157-177,485-525`
- Modify: `apps/web/src/features/studio/StudioPage.test.tsx:1-9,116-125,263-305`

**Interfaces:**
- Consumes: Task 1 的 `nodeDefinitionKey()`、`readRecentNodeKeys()`、`rememberRecentNodeKey()`；Task 2 的 `availableNodePosition()`、`dropNodePosition()`；Task 4 的 `onNodeDefinitionDrop(nodeKey, position)`。
- Produces: 页面内统一 `addNode(definition, requestedPosition?)`；向节点库传入 `recentNodeKeys` 和 `error`，向画布传入 `onNodeDefinitionDrop`。成功创建仍走现有 `workbench.request({ kind: 'config', nodeId }, false)` 和 `commit(next, edges)`。

- [ ] **Step 1: 写点击添加后记录最近使用的失败测试**

在 `StudioPage.test.tsx` 的 `beforeEach` 中加入 `window.localStorage.clear()`，并新增：

```tsx
it('点击添加只保存一次、立即配置并记录最近使用', async () => {
  window.localStorage.setItem('agent-studio.node-library.recent.v1', JSON.stringify(['removed@1']))
  render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
  await screen.findByText('演示助手')
  await userEvent.click(screen.getByRole('button', { name: '添加节点' }))
  await userEvent.click(screen.getByRole('button', { name: /^提示词模板/ }))

  expect(screen.getByRole('dialog', { name: '提示词模板' })).toBeInTheDocument()
  await vi.waitFor(() => expect(api.saveWorkflow).toHaveBeenCalledOnce(), { timeout: 2000 })
  expect(JSON.parse(window.localStorage.getItem('agent-studio.node-library.recent.v1') ?? '[]')).toEqual(['template@1'])

  await userEvent.click(screen.getByRole('button', { name: '关闭工作台' }))
  await userEvent.click(screen.getByRole('button', { name: '添加节点' }))
  expect(screen.getByRole('heading', { name: '最近使用' })).toBeInTheDocument()
  expect(screen.getAllByRole('button', { name: /^提示词模板/ })).toHaveLength(1)
})

it('关闭节点库后把焦点恢复到添加节点按钮', async () => {
  render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
  await screen.findByText('演示助手')
  const add = screen.getByRole('button', { name: '添加节点' })
  await userEvent.click(add)
  await userEvent.keyboard('{Escape}')
  await vi.waitFor(() => expect(add).toHaveFocus())
})
```

- [ ] **Step 2: 写拖放统一创建和失效定义恢复失败测试**

在测试文件加入一个不依赖浏览器 `DataTransfer` 构造器的夹具：

```ts
function nodeTransfer(nodeKey: string) {
  return {
    types: ['application/x-agent-studio-node'],
    getData: (type: string) => type === 'application/x-agent-studio-node' ? nodeKey : '',
    setData: vi.fn(),
    dropEffect: 'none',
    effectAllowed: 'copy',
  } as unknown as DataTransfer
}
```

新增测试：

```tsx
it('拖放通过统一入口创建一次并立即打开配置', async () => {
  render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
  await screen.findByText('演示助手')
  await userEvent.click(screen.getByRole('button', { name: '添加节点' }))
  const transfer = nodeTransfer('template@1')
  const canvas = screen.getByLabelText('工作流画布')
  fireEvent.dragOver(canvas, { dataTransfer: transfer })
  fireEvent.drop(canvas, { dataTransfer: transfer, clientX: 640, clientY: 360 })

  expect(await screen.findByRole('dialog', { name: '提示词模板' })).toBeInTheDocument()
  await vi.waitFor(() => expect(api.saveWorkflow).toHaveBeenCalledOnce(), { timeout: 2000 })
  expect(JSON.parse(window.localStorage.getItem('agent-studio.node-library.recent.v1') ?? '[]')).toEqual(['template@1'])
})

it('拖放失效定义时保持节点库并提示，不创建也不保存', async () => {
  render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
  await screen.findByText('演示助手')
  await userEvent.click(screen.getByRole('button', { name: '添加节点' }))
  const transfer = nodeTransfer('removed@1')
  const canvas = screen.getByLabelText('工作流画布')
  fireEvent.dragOver(canvas, { dataTransfer: transfer })
  fireEvent.drop(canvas, { dataTransfer: transfer, clientX: 640, clientY: 360 })

  expect(screen.getByRole('dialog', { name: '节点库' })).toBeInTheDocument()
  expect(screen.getByRole('alert')).toHaveTextContent('节点定义已更新，请重新选择')
  expect(api.saveWorkflow).not.toHaveBeenCalled()
})

it('只开始拖拽但未释放时不创建、不保存也不记录最近使用', async () => {
  render(<MemoryRouter initialEntries={['/workflows/w1']}><Routes><Route path="/workflows/:id" element={<StudioPage />} /></Routes></MemoryRouter>)
  await screen.findByText('演示助手')
  await userEvent.click(screen.getByRole('button', { name: '添加节点' }))
  const transfer = nodeTransfer('template@1')
  fireEvent.dragStart(screen.getByRole('button', { name: /^提示词模板/ }), { dataTransfer: transfer })
  fireEvent.dragEnd(screen.getByRole('button', { name: /^提示词模板/ }), { dataTransfer: transfer })
  expect(screen.getByRole('dialog', { name: '节点库' })).toBeInTheDocument()
  expect(api.saveWorkflow).not.toHaveBeenCalled()
  expect(window.localStorage.getItem('agent-studio.node-library.recent.v1')).toBeNull()
})
```

- [ ] **Step 3: 运行页面测试并确认失败**

Run:

```bash
corepack pnpm@10.34.5 --filter @agent-studio/web test -- src/features/studio/StudioPage.test.tsx
```

Expected: FAIL，节点库缺少 `recentNodeKeys`，页面没有拖放回调和错误状态。

- [ ] **Step 4: 接入最近状态并收敛统一创建函数**

在 `StudioPage.tsx` 增加导入与状态：

```tsx
import { nodeDefinitionKey, readRecentNodeKeys, rememberRecentNodeKey } from './nodeLibraryModel'
import { availableNodePosition, dropNodePosition } from './nodePlacement'

const [recentNodeKeys, setRecentNodeKeys] = useState<string[]>(() => readRecentNodeKeys())
const [nodeLibraryError, setNodeLibraryError] = useState('')
```

把现有 `addNode` 改为唯一创建入口：

```tsx
const addNode = (definition: NodeDefinition, requestedPosition?: XYPosition) => {
  if (archived || versionLocked) return
  const position = requestedPosition
    ? dropNodePosition(requestedPosition, nodes)
    : availableNodePosition(canvasRef.current?.getViewportCenter() ?? { x: 320, y: 260 }, nodes)
  const node: StudioNode = {
    id: createID(definition.type),
    type: 'studio',
    position,
    data: {
      nodeType: definition.type,
      typeVersion: definition.version,
      config: defaultValue(definition.configSchema as JSONSchema) as Record<string, unknown>,
      definition,
      ports: portsFromDefinition(definition),
      issues: [],
    },
  }
  const next = [...nodes, node]
  setNodes(next)
  const validDefinitionKeys = new Set(definitions.map(nodeDefinitionKey))
  setRecentNodeKeys((current) => rememberRecentNodeKey(
    current.filter((key) => validDefinitionKeys.has(key)),
    nodeDefinitionKey(definition),
  ))
  setNodeLibraryError('')
  setLibraryOpen(false)
  workbench.request({ kind: 'config', nodeId: node.id }, false)
  commit(next, edges)
}

const dropNodeDefinition = (nodeKey: string, position: XYPosition) => {
  const definition = definitions.find((candidate) => nodeDefinitionKey(candidate) === nodeKey)
  if (!definition) {
    setNodeLibraryError('节点定义已更新，请重新选择')
    return
  }
  addNode(definition, position)
}
```

打开或关闭节点库时清除旧的拖放错误，但不要清除最近记录：在 `executeIntent()` 的 `open-library` 分支设置 `setNodeLibraryError('')`，在 `closeTopLayer()` 的 `libraryOpen` 分支以及 `NodeLibraryDrawer.onClose` 回调中再次清除。所有工具栏和快捷键打开路径都已经汇入 `executeIntent()`，不要在每个入口重复维护。

- [ ] **Step 5: 把页面状态传给画布和节点库**

在现有 `<WorkflowCanvas>` 调用中新增 `onNodeDefinitionDrop={libraryOpen ? dropNodeDefinition : undefined}`，保证节点库关闭时画布不接受外部同 MIME 拖放；其他已有画布 props 保持原值。节点库调用替换为：

```tsx
{libraryOpen ? <NodeLibraryDrawer
  definitions={definitions}
  recentNodeKeys={recentNodeKeys}
  error={nodeLibraryError}
  onAdd={(definition) => addNode(definition)}
  onClose={() => { setNodeLibraryError(''); setLibraryOpen(false) }}
/> : undefined}
```

不要让 `WorkflowCanvas` 读取 `definitions`，也不要让 `NodeLibraryDrawer` 直接修改 `nodes`；失效键只能由 `StudioPage` 解析和提示。

- [ ] **Step 6: 运行页面、画布、节点库联合回归**

Run:

```bash
corepack pnpm@10.34.5 --filter @agent-studio/web test -- src/features/studio/nodeLibraryModel.test.ts src/features/studio/nodePlacement.test.ts src/features/studio/NodeLibraryDrawer.test.tsx src/features/studio/WorkflowCanvas.test.tsx src/features/studio/StudioPage.test.tsx
```

Expected: PASS；点击和拖放都只保存一次，最近记录、失效定义、脏配置、归档和现有 Studio 行为全部通过。

- [ ] **Step 7: 提交页面编排**

```bash
git add apps/web/src/features/studio/StudioPage.tsx apps/web/src/features/studio/StudioPage.test.tsx
git commit -m "feat(web): unify node creation flows"
```

---

### Task 6: 节点库视觉、响应式与真实浏览器回归

**Files:**
- Modify: `apps/web/src/features/studio/studio.css:1-9,62,77-88,180-207`
- Modify: `apps/web/e2e/agent-studio.spec.ts:1-53`

**Interfaces:**
- Consumes: Tasks 3–5 已稳定的类名 `.node-library-drawer`、`.node-library-categories`、`.node-library-catalog`、`.node-library-grid`、`.library-node` 和画布 `data-node-drop-active`。
- Produces: 桌面分层侧边库、窄屏单列、拖放视觉反馈，以及覆盖点击、最近、分类、拖放、刷新和移动端的 Playwright 回归。

- [ ] **Step 1: 先写端到端节点库体验失败测试**

在 `agent-studio.spec.ts` 靠近现有全画布测试处新增：

```ts
test('分层节点库支持分类、最近、拖放和移动端降级', async ({ page }) => {
  const suffix = Date.now().toString(36)
  await createWorkflow(page, `node-library-${suffix}`, `节点库体验 ${suffix}`)

  await page.getByRole('button', { name: '添加节点' }).click()
  const library = page.getByRole('dialog', { name: '节点库' })
  await expect(library).toBeVisible()
  await page.getByRole('button', { name: '文本' }).click()
  await expect(page.getByRole('button', { name: /^提示词模板/ })).toBeVisible()
  await expect(page.getByRole('button', { name: /^LLM/ })).toHaveCount(0)
  const desktopColumns = await page.locator('.node-library-grid').first().evaluate((element) => getComputedStyle(element).gridTemplateColumns.split(' ').length)
  expect(desktopColumns).toBe(2)

  await page.getByRole('button', { name: /^提示词模板/ }).click()
  await expect(page.getByRole('dialog', { name: '提示词模板' })).toBeVisible()
  await page.getByRole('button', { name: '关闭工作台' }).click()
  await page.getByRole('button', { name: '添加节点' }).click()
  await page.getByRole('button', { name: '最近' }).click()
  await expect(page.getByRole('button', { name: /^提示词模板/ })).toBeVisible()

  await page.reload()
  await page.getByRole('button', { name: '添加节点' }).click()
  await page.getByRole('button', { name: '最近' }).click()
  await expect(page.getByRole('button', { name: /^提示词模板/ })).toBeVisible()
  await page.getByRole('button', { name: '全部' }).click()
  await page.getByRole('button', { name: /^LLM · 结构化输出/ }).dragTo(page.getByLabel('工作流画布'), {
    targetPosition: { x: 760, y: 280 },
  })
  await expect(page.getByRole('dialog', { name: 'LLM · 结构化输出' })).toBeVisible()

  await page.getByRole('button', { name: '关闭工作台' }).click()
  await page.setViewportSize({ width: 390, height: 844 })
  await page.getByRole('button', { name: '添加节点' }).click()
  await expect(page.locator('.node-library-grid').first()).toHaveCSS('grid-template-columns', /.+/)
  const columns = await page.locator('.node-library-grid').first().evaluate((element) => getComputedStyle(element).gridTemplateColumns.split(' ').length)
  expect(columns).toBe(1)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await page.getByRole('button', { name: /^提示词模板/ }).click()
  await expect(page.getByRole('dialog', { name: '提示词模板' })).toBeVisible()
})
```

- [ ] **Step 2: 运行定向 E2E 并确认视觉/交互测试失败**

Run:

```bash
make db-up
corepack pnpm@10.34.5 --filter @agent-studio/web exec playwright test e2e/agent-studio.spec.ts --grep "分层节点库"
```

Expected: FAIL，节点库尚无分类栏/最近视图、拖放链路或移动单列样式；Playwright 配置自动启动 API 与 Web 服务，数据库固定使用 Docker PostgreSQL。

- [ ] **Step 3: 实现桌面分层布局和卡片状态**

在 `studio.css` 用现有 Token 替换节点库旧的散落色值，并加入：

```css
.studio-shell-library { width: min(520px, calc(100% - 80px)); }
.node-library-drawer { display: grid; grid-template: auto minmax(0, 1fr) / 76px minmax(0, 1fr); padding: 0; overflow: hidden; border-color: var(--as-border); border-radius: var(--as-radius-md); background: var(--as-surface); box-shadow: var(--as-shadow-panel); }
.node-library-heading { grid-column: 1 / -1; display: flex; align-items: start; justify-content: space-between; gap: 12px; padding: 18px 18px 14px; border-bottom: 1px solid var(--as-border); }
.node-library-heading h2 { margin: 2px 0 0; font-size: 21px; }
.node-library-heading > button { width: 34px; height: 34px; border: 0; border-radius: var(--as-radius-sm); background: var(--as-surface-muted); font-size: 22px; }
.node-library-categories { display: grid; align-content: start; gap: 6px; min-height: 0; padding: 10px 7px; overflow-y: auto; background: var(--as-surface-muted); }
.node-library-categories button { width: 100%; padding: 8px 4px; border: 0; border-radius: var(--as-radius-sm); color: var(--as-text-muted); background: transparent; font-size: 12px; }
.node-library-categories button[aria-pressed="true"] { color: var(--as-surface); background: var(--as-primary); font-weight: 750; }
.node-library-catalog { min-width: 0; min-height: 0; padding: 0 16px 18px; overflow-y: auto; }
.node-library-catalog .search-field { position: sticky; z-index: 2; top: 0; margin: 0 -2px 12px; padding: 14px 2px 4px; background: var(--as-surface); }
.node-library-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 9px; }
.node-library-catalog section h3 { margin: 15px 0 8px; color: var(--as-text-muted); font-size: 12px; text-transform: none; }
.library-node { display: grid; min-width: 0; gap: 5px; margin: 0; padding: 12px; border: 1px solid var(--as-border); border-radius: var(--as-radius-sm); color: var(--as-text); text-align: left; background: var(--as-surface); cursor: grab; }
.library-node:hover { border-color: color-mix(in srgb, var(--as-primary) 42%, var(--as-border)); background: color-mix(in srgb, var(--as-primary) 4%, var(--as-surface)); }
.library-node:focus-visible { outline: 3px solid color-mix(in srgb, var(--as-primary) 28%, transparent); outline-offset: 2px; }
.library-node-description { display: -webkit-box; min-height: 2.7em; overflow: hidden; color: var(--as-text-muted); -webkit-box-orient: vertical; -webkit-line-clamp: 2; }
.library-node .package-summary { overflow: hidden; color: var(--as-text-muted); font-size: 11px; font-weight: 650; text-overflow: ellipsis; white-space: nowrap; }
.node-library-empty { display: grid; justify-items: start; gap: 8px; padding: 20px 2px; color: var(--as-text-muted); }
.node-library-error { margin: 8px 0; padding: 9px 10px; border: 1px solid color-mix(in srgb, var(--as-danger) 28%, var(--as-border)); border-radius: var(--as-radius-sm); color: var(--as-danger); background: color-mix(in srgb, var(--as-danger) 5%, var(--as-surface)); }
.workflow-canvas[data-node-drop-active="true"] { box-shadow: inset 0 0 0 3px color-mix(in srgb, var(--as-primary) 46%, transparent); }
```

以上颜色只复用 `apps/web/src/app/tokens.css` 已存在的语义 Token，不为同一语义新增硬编码色值。

- [ ] **Step 4: 实现窄屏单列和 reduced-motion 降级**

在现有媒体查询内加入：

```css
@media (max-width: 639px) {
  .node-library-drawer { grid-template: auto auto minmax(0, 1fr) / minmax(0, 1fr); }
  .node-library-heading { grid-column: 1; }
  .node-library-categories { display: flex; padding: 8px 10px; overflow-x: auto; overflow-y: hidden; }
  .node-library-categories button { flex: 0 0 auto; width: auto; min-width: 58px; }
  .node-library-catalog { padding-right: 14px; padding-left: 14px; }
  .node-library-grid { grid-template-columns: minmax(0, 1fr); }
  .library-node { cursor: pointer; }
}

@media (prefers-reduced-motion: reduce) {
  .workflow-canvas[data-node-drop-active="true"] { transition: none; }
}
```

- [ ] **Step 5: 运行定向前端测试和 E2E**

Run:

```bash
corepack pnpm@10.34.5 --filter @agent-studio/web test -- src/features/studio/NodeLibraryDrawer.test.tsx src/features/studio/WorkflowCanvas.test.tsx src/features/studio/StudioPage.test.tsx
make db-up
corepack pnpm@10.34.5 --filter @agent-studio/web exec playwright test e2e/agent-studio.spec.ts --grep "分层节点库"
```

Expected: PASS；桌面分类/最近/拖放、刷新恢复、390px 单列点击和无横向溢出全部通过。

- [ ] **Step 6: 提交视觉和 E2E**

```bash
git add apps/web/src/features/studio/studio.css apps/web/e2e/agent-studio.spec.ts
git commit -m "feat(web): polish node library experience"
```

---

### Task 7: 代码审查、全量回归与 PR

**Files:**
- Review: `docs/superpowers/specs/2026-08-27-node-library-ux-design.md`
- Review: `apps/web/src/features/studio/nodeLibraryModel.ts`
- Review: `apps/web/src/features/studio/nodeLibraryModel.test.ts`
- Review: `apps/web/src/features/studio/nodePlacement.ts`
- Review: `apps/web/src/features/studio/nodePlacement.test.ts`
- Review: `apps/web/src/features/studio/NodeLibraryDrawer.tsx`
- Review: `apps/web/src/features/studio/NodeLibraryDrawer.test.tsx`
- Review: `apps/web/src/features/studio/WorkflowCanvas.tsx`
- Review: `apps/web/src/features/studio/WorkflowCanvas.test.tsx`
- Review: `apps/web/src/features/studio/StudioPage.tsx`
- Review: `apps/web/src/features/studio/StudioPage.test.tsx`
- Review: `apps/web/src/features/studio/studio.css`
- Review: `apps/web/e2e/agent-studio.spec.ts`

**Interfaces:**
- Consumes: Tasks 1–6 的所有提交和本规格。
- Produces: 无 P1/P2 审查问题、全部自动化验证通过、已推送的 `codex/node-library-ux` 分支和面向 `main` 的 GitHub PR。

- [ ] **Step 1: 运行格式、类型、单测和构建检查**

Run:

```bash
git diff --check origin/main...HEAD
corepack pnpm@10.34.5 --filter @agent-studio/web test
corepack pnpm@10.34.5 --filter @agent-studio/web typecheck
corepack pnpm@10.34.5 --filter @agent-studio/web build
```

Expected: 所有命令 exit 0；无空白错误、TypeScript 错误、失败测试或构建错误。

- [ ] **Step 2: 使用代码审查技能检查完整变更**

Invoke `superpowers:requesting-code-review`，审查范围固定为 `origin/main...HEAD`，并把以下验收点随审查请求传入：

```text
1. NodeLibraryDrawer 不直接修改图状态；WorkflowCanvas 不解析 NodeDefinition。
2. 点击与拖拽只创建、保存和记录一次。
3. localStorage 任意异常不阻塞添加。
4. 拖放只接受 application/x-agent-studio-node，归档/版本锁定时只读。
5. 分类、搜索、最近视图的视觉顺序与键盘顺序一致且无重复卡片。
6. 20px 网格、空闲单轴 10px 误差和占用时确定性非重叠均有测试。
7. 移动端点击、焦点恢复、aria 状态和无横向溢出均有回归。
```

Expected: 审查没有未解决的 P1/P2 问题。若发现问题，先为问题补充失败测试，再做最小修复，运行受影响测试并提交 `fix(web): address node library review findings`；之后重新执行本步骤。

- [ ] **Step 3: 使用 Docker PostgreSQL 运行仓库全量回归**

Run:

```bash
CGO_ENABLED=0 make verify
CGO_ENABLED=0 make test-e2e
```

Expected: 两个命令 exit 0；两个 Make 目标都会先执行 `db-up`，数据库固定为 Docker PostgreSQL；Go、前端、契约、构建和完整 Playwright 回归通过。

- [ ] **Step 4: 核对最终提交和工作区**

Run:

```bash
git status --short --branch
git log --oneline origin/main..HEAD
git diff --stat origin/main...HEAD
```

Expected: 工作区干净；提交仅包含设计、计划和节点库前端增量；没有 OpenAPI、Go、数据库或生成客户端变更。

- [ ] **Step 5: 推送分支并创建 GitHub PR**

Run:

```bash
git push -u origin codex/node-library-ux
gh pr create --base main --head codex/node-library-ux --title "feat(web): 优化节点库添加体验" --body "实现分层节点库、分类与最近使用、点击中心添加、桌面拖放和移动端降级。保持 Go 后端、数据库、OpenAPI 和节点契约不变。验证：前端单测、typecheck、build、CGO_ENABLED=0 make verify、CGO_ENABLED=0 make test-e2e。"
```

Expected: GitHub 返回 PR URL，base 为 `main`，head 为 `codex/node-library-ux`；PR 公开列出范围、非目标和验证结果。

- [ ] **Step 6: 等待 CI 并完成交付说明**

Run:

```bash
gh pr checks --watch
```

Expected: 所有必需检查通过。向用户报告 PR 链接、核心功能、验证命令和已知非目标；未获得用户明确同意前不合并 PR。
