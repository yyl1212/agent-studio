import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { SchemaForm } from './SchemaForm'

const schema = {
  type: 'object' as const,
  required: ['topic'],
  'x-ui-order': ['topic', 'enabled', 'style', 'payload'],
  properties: {
    topic: { type: 'string' as const, title: '主题', minLength: 2, 'x-ui-placeholder': '请输入主题' },
    enabled: { type: 'boolean' as const, title: '启用' },
    style: { type: 'string' as const, title: '风格', enum: ['brief', 'detail'] },
    payload: { type: 'object' as const, title: '参数', 'x-ui-widget': 'json' as const },
  },
}

describe('SchemaForm', () => {
  it('支持服务端生成的 JSON Schema 2020-12', () => {
    render(<SchemaForm schema={{ ...schema, $schema: 'https://json-schema.org/draft/2020-12/schema' }} value={{}} onChange={vi.fn()} onSubmit={vi.fn()} submitLabel="运行" />)
    expect(screen.getByLabelText('主题')).toBeInTheDocument()
  })

  it('渲染必填文本、布尔、单选和 JSON 字段并显示中文校验', async () => {
    render(<SchemaForm schema={schema} value={{}} onChange={vi.fn()} onSubmit={vi.fn()} submitLabel="运行" />)
    expect(screen.getByLabelText('主题')).toBeRequired()
    expect(screen.getByLabelText('主题')).toHaveAttribute('placeholder', '请输入主题')
    expect(screen.getByRole('checkbox', { name: '启用' })).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: '风格' })).toBeInTheDocument()
    expect(screen.getByLabelText('参数')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '运行' }))
    expect(screen.getByText('主题为必填项')).toBeInTheDocument()
  })

  it('JSON 非法时不提交', async () => {
    const onSubmit = vi.fn()
    render(<SchemaForm schema={schema} value={{ topic: '有效主题' }} onChange={vi.fn()} onSubmit={onSubmit} submitLabel="运行" />)
    await userEvent.type(screen.getByLabelText('参数'), '{{bad')
    await userEvent.click(screen.getByRole('button', { name: '运行' }))
    expect(screen.getByText('参数必须是合法 JSON')).toBeInTheDocument()
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('递归渲染对象与数组字段', async () => {
    const nestedSchema = {
      type: 'object' as const,
      properties: {
        settings: {
          type: 'object' as const,
          title: '设置',
          properties: { label: { type: 'string' as const, title: '标签' } },
        },
        tags: { type: 'array' as const, title: '标签列表', items: { type: 'string' as const } },
      },
    }
    render(<SchemaForm schema={nestedSchema} value={{ tags: [] }} onChange={vi.fn()} onSubmit={vi.fn()} submitLabel="保存" />)
    expect(screen.getByLabelText('标签')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '添加一项' }))
    expect(screen.getByLabelText('标签列表 1')).toBeInTheDocument()
  })
})
