import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { NodeIcon } from './NodeIcon'

describe('NodeIcon', () => {
  it('非装饰图标通过类别提供可访问名称', () => {
    render(<NodeIcon category="AI" />)

    expect(screen.getByRole('img', { name: 'AI 节点' })).toBeVisible()
  })

  it('装饰图标从辅助技术中隐藏', () => {
    const { container } = render(<NodeIcon category="文本" decorative />)

    expect(screen.queryByRole('img')).not.toBeInTheDocument()
    expect(container.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
  })
})
