import { render, screen } from '@testing-library/react'
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
    ]} onAdd={onAdd} onClose={onClose} />)

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
    ]} onAdd={onAdd} onClose={vi.fn()} />)

    await userEvent.keyboard('{ArrowDown}{ArrowDown}{Enter}')
    expect(onAdd).toHaveBeenCalledWith(expect.objectContaining({ type: 'a.two' }))
  })

  it('展示包摘要并按包名搜索', async () => {
    render(<NodeLibraryDrawer definitions={[definition('v0.3.2')]} onAdd={vi.fn()} onClose={vi.fn()} />)
    expect(screen.getByText('Example Nodes · v0.3.2')).toBeInTheDocument()
    await userEvent.type(screen.getByLabelText('搜索节点'), 'github.com/example/nodes')
    expect(screen.getByRole('button', { name: /Search/ })).toBeInTheDocument()
    await userEvent.clear(screen.getByLabelText('搜索节点'))
    await userEvent.type(screen.getByLabelText('搜索节点'), 'unrelated')
    expect(screen.getByText('没有匹配的节点')).toBeInTheDocument()
  })

  it('开发包无版本时不显示 undefined', () => {
    render(<NodeLibraryDrawer definitions={[definition()]} onAdd={vi.fn()} onClose={vi.fn()} />)
    expect(screen.getByText('Example Nodes')).toBeInTheDocument()
    expect(screen.queryByText(/undefined/)).not.toBeInTheDocument()
  })

  it('区分同类型的 LLM v1 与 v2，并可按结构化能力搜索', async () => {
    const onAdd = vi.fn()
    const definitions = [
      definition(undefined, { type: 'llm', version: '1', title: 'LLM', description: '生成文本', category: 'AI' }),
      definition(undefined, { type: 'llm', version: '2', title: 'LLM · 结构化输出', description: '生成严格结构化结果', category: 'AI' }),
    ]
    render(<NodeLibraryDrawer definitions={definitions} onAdd={onAdd} onClose={vi.fn()} />)

    expect(screen.getByRole('button', { name: /^LLM生成文本/ })).toBeInTheDocument()
    const structured = screen.getByRole('button', { name: /^LLM · 结构化输出/ })
    expect(structured).toBeInTheDocument()
    await userEvent.click(structured)
    expect(onAdd).toHaveBeenCalledWith(expect.objectContaining({ type: 'llm', version: '2' }))

    await userEvent.type(screen.getByLabelText('搜索节点'), '结构化')
    expect(screen.queryByRole('button', { name: /^LLM生成文本/ })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^LLM · 结构化输出/ })).toBeInTheDocument()
  })
})
