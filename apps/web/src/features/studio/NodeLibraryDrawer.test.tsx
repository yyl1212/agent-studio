import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import type { NodeDefinition } from '../../lib/api/client'
import { NodeLibraryDrawer } from './NodeLibraryDrawer'

const definition = (version?: string): NodeDefinition => ({
  type: 'example.search', version: '1.0.0', title: 'Search', description: '搜索文档', category: '扩展',
  configSchema: {}, inputs: [], outputs: [], capabilities: [],
  package: {
    name: 'github.com/example/nodes', displayName: 'Example Nodes', version,
    license: 'Apache-2.0', repository: 'https://github.com/example/nodes', source: version ? 'module' : 'development',
  },
})

describe('NodeLibraryDrawer', () => {
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
})
