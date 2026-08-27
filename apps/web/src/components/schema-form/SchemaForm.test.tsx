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

const documentsSchema = {
  type: 'object' as const,
  required: ['documents', 'topK'],
  properties: {
    documents: {
      type: 'array' as const,
      title: '文档',
      minItems: 1,
      maxItems: 2,
      items: {
        type: 'object' as const,
        title: '文档项',
        required: ['id', 'text'],
        properties: {
          id: { type: 'string' as const, title: '文档标识', minLength: 1 },
          text: { type: 'string' as const, title: '文档内容', minLength: 1, 'x-ui-widget': 'textarea' as const },
          enabled: { type: 'boolean' as const, title: '启用', default: true },
        },
      },
    },
    topK: { type: 'integer' as const, title: '返回数量', default: 1 },
  },
}

describe('SchemaForm', () => {
  it('第二提交复用校验并调用独立处理器', async () => {
    const onSecondarySubmit = vi.fn()
    render(<SchemaForm schema={schema} value={{ topic: 'Agent' }} onChange={vi.fn()} onSubmit={vi.fn()} submitLabel="应用"
      secondarySubmit={{ label: '应用并试运行', onSubmit: onSecondarySubmit }} />)
    await userEvent.click(screen.getByRole('button', { name: '应用并试运行' }))
    expect(onSecondarySubmit).toHaveBeenCalledWith(expect.objectContaining({ topic: 'Agent' }))
  })

  it('无效提交展示错误并自动聚焦首个字段', async () => {
    render(<SchemaForm schema={schema} value={{ topic: '' }} onChange={vi.fn()} onSubmit={vi.fn()} submitLabel="应用" />)
    await userEvent.click(screen.getByRole('button', { name: '应用' }))
    await vi.waitFor(() => expect(screen.getByLabelText('主题')).toHaveFocus())
  })

  it('按 x-ui-order 分组必要与可选配置并从摘要定位首个错误', async () => {
    render(<SchemaForm schema={{
      type: 'object',
      required: ['model'],
      'x-ui-order': ['prompt', 'model', 'temperature'],
      properties: {
        prompt: { type: 'string', title: '提示词' },
        model: { type: 'string', title: '模型', minLength: 1 },
        temperature: { type: 'number', title: '温度' },
      },
    }} value={{ model: '', prompt: '' }} onChange={vi.fn()} onSubmit={vi.fn()} submitLabel="应用配置" groupOptional />)
    expect(screen.getByRole('heading', { name: '必要配置' })).toBeInTheDocument()
    expect(screen.getByText('可选配置')).toBeInTheDocument()
    expect(screen.getByLabelText('提示词')).not.toBeVisible()
    await userEvent.click(screen.getByRole('button', { name: '应用配置' }))
    const summary = screen.getByRole('button', { name: '1 项需要处理' })
    expect(summary).toBeInTheDocument()
    await userEvent.click(summary)
    expect(screen.getByLabelText('模型')).toHaveFocus()
  })

  it('报告归一化后的完整验证结果且不修改输入对象', async () => {
    const value = { topic: '有效主题', payload: { answer: 42 } }
    const original = structuredClone(value)
    const onValidationChange = vi.fn()
    render(<SchemaForm schema={schema} value={value} onChange={vi.fn()} onSubmit={vi.fn()} submitLabel="运行" onValidationChange={onValidationChange} />)
    await vi.waitFor(() => expect(onValidationChange).toHaveBeenCalled())
    expect(onValidationChange).toHaveBeenLastCalledWith(expect.objectContaining({ valid: true, normalized: value, errors: {} }))
    expect(value).toEqual(original)
  })

  it('失焦后只显示当前字段的校验错误', async () => {
    render(<SchemaForm schema={{ type: 'object', required: ['model', 'prompt'], properties: {
      model: { type: 'string', title: '模型', minLength: 1 }, prompt: { type: 'string', title: '提示词', minLength: 1 },
    } }} value={{ model: '', prompt: '' }} onChange={vi.fn()} onSubmit={vi.fn()} submitLabel="应用" />)
    await userEvent.click(screen.getByLabelText('模型'))
    await userEvent.tab()
    expect(screen.getByText('模型长度不能少于 1 个字符')).toBeInTheDocument()
    expect(screen.queryByText('提示词长度不能少于 1 个字符')).not.toBeInTheDocument()
  })

  it('字段同时关联帮助说明与校验错误', async () => {
    render(<SchemaForm schema={{ type: 'object', required: ['model'], properties: {
      model: { type: 'string', title: '模型', description: '填写模型标识', minLength: 1 },
    } }} value={{ model: '' }} onChange={vi.fn()} onSubmit={vi.fn()} submitLabel="应用" />)
    await userEvent.click(screen.getByRole('button', { name: '应用' }))
    expect(screen.getByLabelText('模型')).toHaveAttribute('aria-describedby', 'field-model-description field-model-error')
  })

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

  it('按 item schema 创建对象默认值并支持编辑删除', async () => {
    const onChange = vi.fn()
    render(<SchemaForm schema={documentsSchema} value={{ documents: [], topK: 1 }} onChange={onChange} onSubmit={vi.fn()} submitLabel="保存" />)
    await userEvent.click(screen.getByRole('button', { name: '添加一项' }))
    expect(screen.getByLabelText('文档标识')).toHaveValue('')
    expect(screen.getByLabelText('文档内容')).toHaveValue('')
    expect(screen.getByRole('checkbox', { name: '启用' })).toBeChecked()
    await userEvent.type(screen.getByLabelText('文档标识'), 'doc-1')
    await userEvent.type(screen.getByLabelText('文档内容'), 'Agent Studio')
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({
      documents: [{ id: 'doc-1', text: 'Agent Studio', enabled: true }],
    }))
    await userEvent.click(screen.getByRole('button', { name: '添加一项' }))
    expect(screen.getAllByLabelText('文档标识')).toHaveLength(2)
    await userEvent.click(screen.getByRole('button', { name: '移除文档 2' }))
    expect(screen.getAllByLabelText('文档标识')).toHaveLength(1)
    expect(screen.getByLabelText('文档标识')).toHaveValue('doc-1')
  })

  it('限制数组最少和最多项并显示中文错误', async () => {
    const onChange = vi.fn()
    const { rerender } = render(<SchemaForm schema={documentsSchema} value={{ documents: [], topK: 1 }} onChange={onChange} onSubmit={vi.fn()} submitLabel="保存" />)
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    expect(screen.getByText('文档至少需要 1 项')).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: '添加一项' }))
    const removeFirst = screen.getByRole('button', { name: '移除文档 1' })
    expect(removeFirst).toBeDisabled()
    const callsAtMinimum = onChange.mock.calls.length
    await userEvent.click(removeFirst)
    expect(onChange).toHaveBeenCalledTimes(callsAtMinimum)

    await userEvent.click(screen.getByRole('button', { name: '添加一项' }))
    const addAtMaximum = screen.getByRole('button', { name: '添加一项' })
    expect(addAtMaximum).toBeDisabled()
    const callsAtMaximum = onChange.mock.calls.length
    await userEvent.click(addAtMaximum)
    expect(onChange).toHaveBeenCalledTimes(callsAtMaximum)

    rerender(<SchemaForm schema={documentsSchema} value={{ documents: [{}, {}, {}], topK: 1 }} onChange={onChange} onSubmit={vi.fn()} submitLabel="保存" />)
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    expect(screen.getByText('文档最多允许 2 项')).toBeInTheDocument()
  })

  it('使用嵌套字段标题显示对象数组校验错误', async () => {
    render(<SchemaForm schema={documentsSchema} value={{ documents: [{ id: '', text: '' }], topK: 1 }} onChange={vi.fn()} onSubmit={vi.fn()} submitLabel="保存" />)
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    expect(screen.getByText('文档标识长度不能少于 1 个字符')).toBeInTheDocument()
    expect(screen.getByText('文档内容长度不能少于 1 个字符')).toBeInTheDocument()
  })

	it('按 RFC 6901 实例路径限制编辑与必填且锁定数组形状', async () => {
		const pathSchema = {
			type: 'object' as const,
			properties: {
				credentials: { type: 'object' as const, title: '凭据', properties: {
					token: { type: 'string' as const, title: '令牌' }, note: { type: 'string' as const, title: '说明' },
				} },
				items: { type: 'array' as const, title: '账户', items: { type: 'object' as const, properties: {
					password: { type: 'string' as const, title: '密码' }, label: { type: 'string' as const, title: '名称' },
				} } },
			},
		}
		const onSubmit = vi.fn()
		render(<SchemaForm schema={pathSchema} value={{ credentials: { token: '', note: '只读说明' }, items: [{ password: '', label: '首项' }, { password: '', label: '次项' }] }}
			onChange={vi.fn()} onSubmit={onSubmit} submitLabel="重新运行"
			editablePaths={new Set(['/credentials/token', '/items/0/password'])}
			requiredPaths={new Set(['/credentials/token', '/items/0/password'])} />)

		const passwords = screen.getAllByLabelText('密码')
		expect(screen.getByLabelText('令牌')).toHaveFocus()
		expect(screen.getByLabelText('令牌')).toBeRequired()
		expect(screen.getByLabelText('说明')).toHaveAttribute('readonly')
		expect(passwords[0]).toBeRequired()
		expect(passwords[0]).not.toHaveAttribute('readonly')
		expect(passwords[1]).not.toBeRequired()
		expect(passwords[1]).toHaveAttribute('readonly')
		expect(screen.getByRole('button', { name: '添加一项' })).toBeDisabled()
		for (const button of screen.getAllByRole('button', { name: /移除账户/ })) expect(button).toBeDisabled()

		await userEvent.click(screen.getByRole('button', { name: '重新运行' }))
		expect(screen.getAllByText(/为必填项/)).toHaveLength(2)
		expect(onSubmit).not.toHaveBeenCalled()
	})
})
