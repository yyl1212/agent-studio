import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import type { NodeDefinition } from '../../lib/api/client'
import { NodeLibraryDrawer } from './NodeLibraryDrawer'

const definition = (packageVersion?: string, overrides: Partial<NodeDefinition> = {}): NodeDefinition => ({
  type: 'example.search', version: '1.0.0', title: 'Search', description: '搜索文档', category: '扩展',
  configSchema: {}, inputs: [], outputs: [], capabilities: [], executionSafety: 'pure',
  package: {
    name: 'github.com/example/nodes', displayName: 'Example Nodes', version: packageVersion,
    license: 'Apache-2.0', repository: 'https://github.com/example/nodes', source: packageVersion ? 'module' : 'development',
  },
  ...overrides,
})

describe('NodeLibraryDrawer', () => {
  it('打开后聚焦搜索，并支持上下键、回车和 Escape', async () => {
    const onAdd = vi.fn()
    const onClose = vi.fn()
    render(<NodeLibraryDrawer definitions={[
      definition(),
      definition(undefined, { type: 'example.http', title: 'HTTP' }),
    ]} recentNodeKeys={[]} onAdd={onAdd} onClose={onClose} />)

    const search = screen.getByLabelText('搜索节点')
    expect(search).toHaveFocus()
    await userEvent.keyboard('{ArrowDown}{ArrowDown}{Enter}')
    expect(onAdd).toHaveBeenCalledWith(expect.objectContaining({ type: 'example.http' }))
    search.focus()
    await userEvent.keyboard('{Escape}')
    expect(onClose).toHaveBeenCalledOnce()
  })

  it('键盘移动顺序与按类别分组后的视觉顺序一致', async () => {
    const onAdd = vi.fn()
    render(<NodeLibraryDrawer definitions={[
      definition(undefined, { type: 'a.one', title: 'A One', category: 'A' }),
      definition(undefined, { type: 'b.one', title: 'B One', category: 'B' }),
      definition(undefined, { type: 'a.two', title: 'A Two', category: 'A' }),
    ]} recentNodeKeys={[]} onAdd={onAdd} onClose={vi.fn()} />)

    await userEvent.keyboard('{ArrowDown}{ArrowDown}{Enter}')
    expect(onAdd).toHaveBeenCalledWith(expect.objectContaining({ type: 'a.two' }))
  })

  it('展示包摘要并按包名搜索', async () => {
    render(<NodeLibraryDrawer definitions={[definition('v0.3.2')]} recentNodeKeys={[]} onAdd={vi.fn()} onClose={vi.fn()} />)
    expect(screen.getByText('Example Nodes · v0.3.2')).toBeInTheDocument()
    await userEvent.type(screen.getByLabelText('搜索节点'), 'github.com/example/nodes')
    expect(screen.getByRole('button', { name: /Search/ })).toBeInTheDocument()
    await userEvent.clear(screen.getByLabelText('搜索节点'))
    await userEvent.type(screen.getByLabelText('搜索节点'), 'unrelated')
    expect(screen.getByText('没有匹配的节点')).toBeInTheDocument()
  })

  it('开发包无版本时不显示 undefined', () => {
    render(<NodeLibraryDrawer definitions={[definition()]} recentNodeKeys={[]} onAdd={vi.fn()} onClose={vi.fn()} />)
    expect(screen.getByText('Example Nodes')).toBeInTheDocument()
    expect(screen.queryByText(/undefined/)).not.toBeInTheDocument()
  })

  it('区分同类型的 LLM v1 与 v2，并可按结构化能力搜索', async () => {
    const onAdd = vi.fn()
    const definitions = [
      definition(undefined, { type: 'llm', version: '1', title: 'LLM', description: '生成文本', category: 'AI' }),
      definition(undefined, { type: 'llm', version: '2', title: 'LLM · 结构化输出', description: '生成严格结构化结果', category: 'AI' }),
    ]
    render(<NodeLibraryDrawer definitions={definitions} recentNodeKeys={[]} onAdd={onAdd} onClose={vi.fn()} />)

    expect(screen.getByRole('button', { name: /^LLM生成文本/ })).toBeInTheDocument()
    const structured = screen.getByRole('button', { name: /^LLM · 结构化输出/ })
    expect(structured).toBeInTheDocument()
    await userEvent.click(structured)
    expect(onAdd).toHaveBeenCalledWith(expect.objectContaining({ type: 'llm', version: '2' }))

    await userEvent.type(screen.getByLabelText('搜索节点'), '结构化')
    expect(screen.queryByRole('button', { name: /^LLM生成文本/ })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^LLM · 结构化输出/ })).toBeInTheDocument()
  })

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
})
