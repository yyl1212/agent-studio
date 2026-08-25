import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { App } from './App'

describe('App', () => {
  it('显示中文产品名称', () => {
    render(<App />)

    expect(screen.getByRole('heading', { name: 'Agent Studio' })).toBeInTheDocument()
    const navigation = screen.getByRole('navigation', { name: '主导航' })
    expect(within(navigation).getByRole('link', { name: '工作流' })).toBeInTheDocument()
    expect(within(navigation).getByRole('link', { name: '节点包' })).toBeInTheDocument()
  })
})
