import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { AgentResult } from './AgentResult'

describe('AgentResult', () => {
  afterEach(() => vi.unstubAllGlobals())

  it.each([
    ['auto', 'plain', 'plain'],
    ['auto', { z: 1, a: { y: 2, x: 1 } }, '{\n  "a": {\n    "x": 1,\n    "y": 2\n  },\n  "z": 1\n}'],
    ['text', { z: 1, a: 2 }, '{"a":2,"z":1}'],
    ['json', 'plain', '"plain"'],
  ] as const)('%s 模式安全格式化结果', (mode, value, expected) => {
    render(<AgentResult value={value} mode={mode} />)
    expect(screen.getByRole('region', { name: '运行结果' }).querySelector('pre')).toHaveTextContent(expected, { normalizeWhitespace: false })
  })

  it('复制成功和失败都有明确反馈', async () => {
    const writeText = vi.fn().mockResolvedValueOnce(undefined).mockRejectedValueOnce(new Error('denied'))
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    const { rerender } = render(<AgentResult value="结果" mode="text" />)
    await userEvent.click(screen.getByRole('button', { name: '复制结果' }))
    expect(screen.getByText('已复制')).toBeInTheDocument()
    rerender(<AgentResult value="另一个结果" mode="text" />)
    await userEvent.click(screen.getByRole('button', { name: '复制结果' }))
    expect(screen.getByRole('alert')).toHaveTextContent('复制失败')
  })

  it('循环或非 JSON 值显示固定安全文案', () => {
    const value: Record<string, unknown> = {}
    value.self = value
    render(<AgentResult value={value} mode="json" />)
    expect(screen.getByText('结果无法安全序列化')).toBeInTheDocument()
  })
})
