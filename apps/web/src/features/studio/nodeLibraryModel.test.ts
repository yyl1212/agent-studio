import { describe, expect, it } from 'vitest'

import type { NodeDefinition } from '../../lib/api/client'
import {
  buildNodeLibraryView,
  compatibleInputPorts,
  nodeDefinitionKey,
  readRecentNodeKeys,
  RECENT_NODE_STORAGE_KEY,
  rememberRecentNodeKey,
  type PortDataType,
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
const port = (key: string, type: PortDataType): NodeDefinition['inputs'][number] => ({
  key,
  title: key,
  type,
  required: false,
  cardinality: 'one',
})

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

  it('全部视图把有效最近项置顶且不在后续分类中重复', () => {
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

  it('最近范围只返回当前仍存在的定义', () => {
    const template = definition('template', '文本')
    const view = buildNodeLibraryView([template], {
      query: '',
      scope: { kind: 'recent' },
      recentNodeKeys: ['removed@1', nodeDefinitionKey(template)],
    })

    expect(view.sections).toEqual([{ id: 'recent', label: '最近使用', definitions: [template] }])
    expect(view.count).toBe(1)
  })

  it('使用 50 个定义时在当前调用内同步返回过滤结果', () => {
    const definitions = Array.from({ length: 50 }, (_, index) => definition(`node-${index}`, index % 2 === 0 ? '文本' : 'AI'))
    const view = buildNodeLibraryView(definitions, { query: 'node-49', scope: all, recentNodeKeys: [] })

    expect(view.sections).toHaveLength(1)
    expect(view.sections[0].definitions.map((item) => item.type)).toEqual(['node-49'])
  })

  it('连接上下文只保留具有兼容输入的普通节点', () => {
    const view = buildNodeLibraryView([
      definition('start', '流程', { inputs: [] }),
      definition('text', '文本', { inputs: [port('prompt', 'string')] }),
      definition('number', '数据', { inputs: [port('value', 'number')] }),
      definition('any', '数据', { inputs: [port('value', 'any')] }),
      definition('end', '流程', { inputs: [port('result', 'any')] }),
    ], {
      query: '',
      scope: all,
      recentNodeKeys: [],
      compatibility: { sourceType: 'string' },
    })

    expect(view.sections.flatMap((section) => section.definitions).map((item) => item.type)).toEqual(['text', 'any'])
  })

  it('搜索字段包含分类且永不返回开始结束节点', () => {
    const candidates = [
      definition('start', '流程'),
      definition('template', '文本处理', { title: '提示词模板' }),
      definition('end', '流程'),
    ]

    const view = buildNodeLibraryView(candidates, {
      query: '文本处理',
      scope: all,
      recentNodeKeys: [],
    })

    expect(view.sections.flatMap((section) => section.definitions).map((item) => item.type)).toEqual(['template'])
  })

  it.each([
    ['string', ['string', 'any']],
    ['number', ['number', 'any']],
    ['any', ['string', 'number', 'any']],
  ] as const)('%s 输出匹配确定的输入集合', (sourceType, expected) => {
    const candidate = definition('candidate', '测试', {
      inputs: [port('s', 'string'), port('n', 'number'), port('a', 'any')],
    })

    expect(compatibleInputPorts(candidate, sourceType).map((input) => input.type)).toEqual(expected)
  })

  it('无输入节点不兼容且函数不修改输入数组', () => {
    const candidate = definition('candidate', '测试', { inputs: [] })
    const before = structuredClone(candidate.inputs)

    expect(compatibleInputPorts(candidate, 'string')).toEqual([])
    expect(candidate.inputs).toEqual(before)
  })
})

describe('recent node storage', () => {
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

  it('读取时过滤非字符串、去重并裁剪为 6 项', () => {
    const storage = {
      getItem: () => JSON.stringify(['a@1', 3, 'a@1', 'b@1', 'c@1', 'd@1', 'e@1', 'f@1', 'g@1']),
      setItem: () => undefined,
    }

    expect(readRecentNodeKeys(storage)).toEqual(['a@1', 'b@1', 'c@1', 'd@1', 'e@1', 'f@1'])
  })

  it('非数组存储值降级为空列表', () => {
    const storage = { getItem: () => JSON.stringify({ node: 'a@1' }), setItem: () => undefined }
    expect(readRecentNodeKeys(storage)).toEqual([])
  })

  it('存储读取、解析或写入失败时降级为空列表', () => {
    const readFailure = { getItem: () => { throw new Error('blocked') }, setItem: () => undefined }
    const parseFailure = { getItem: () => '{bad json', setItem: () => undefined }
    const writeFailure = { getItem: () => null, setItem: () => { throw new Error('quota') } }

    expect(readRecentNodeKeys(readFailure)).toEqual([])
    expect(readRecentNodeKeys(parseFailure)).toEqual([])
    expect(rememberRecentNodeKey([], 'template@1', writeFailure)).toEqual([])
  })
})
