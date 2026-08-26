import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import type { AgentPresentation } from '../../lib/api/client'
import { AgentPageSettingsDialog } from './AgentPageSettingsDialog'

const value: AgentPresentation = {
  title: '知识助手', description: '回答问题', accent: 'indigo', submitLabel: '运行 Agent', resultMode: 'auto',
}

function renderDialog(overrides: Partial<Parameters<typeof AgentPageSettingsDialog>[0]> = {}) {
  const props = {
    open: true, workflowName: '工作流名称', workflowDescription: '工作流说明', value,
    saving: false, onClose: vi.fn(), onSave: vi.fn(), ...overrides,
  }
  return { ...render(<AgentPageSettingsDialog {...props} />), props }
}

describe('AgentPageSettingsDialog', () => {
  it('使用模态语义并在 Escape 后恢复触发点焦点', async () => {
    const trigger = document.createElement('button')
    document.body.append(trigger)
    trigger.focus()
    const { props } = renderDialog()
    const dialog = screen.getByRole('dialog', { name: '页面设置' })
    expect(dialog).toHaveAttribute('aria-modal', 'true')
    dialog.dispatchEvent(new Event('cancel', { cancelable: true }))
    expect(props.onClose).toHaveBeenCalled()
  })
  it('按当前设置初始化并实时预览全部字段', async () => {
    renderDialog()
    expect(screen.getByLabelText('页面标题')).toHaveValue('知识助手')
    expect(screen.getByLabelText('页面说明')).toHaveValue('回答问题')
    await userEvent.clear(screen.getByLabelText('页面标题'))
    await userEvent.type(screen.getByLabelText('页面标题'), '研究助手')
    await userEvent.clear(screen.getByLabelText('页面说明'))
    await userEvent.type(screen.getByLabelText('页面说明'), '生成结果')
    await userEvent.selectOptions(screen.getByLabelText('强调色'), 'teal')
    await userEvent.clear(screen.getByLabelText('提交按钮文案'))
    await userEvent.type(screen.getByLabelText('提交按钮文案'), '开始研究')
    await userEvent.selectOptions(screen.getByLabelText('结果展示方式'), 'json')
    const preview = screen.getByRole('region', { name: 'Agent 页面预览' })
    expect(preview).toHaveTextContent('研究助手')
    expect(preview).toHaveTextContent('生成结果')
    expect(preview).toHaveTextContent('开始研究')
    expect(preview).toHaveTextContent('JSON')
    expect(preview).toHaveClass('accent-teal')
  })

  it('恢复工作流信息和默认样式', async () => {
    renderDialog({ value: { title: '自定义', description: '自定义说明', accent: 'rose', submitLabel: '提交', resultMode: 'json' } })
    await userEvent.click(screen.getByRole('button', { name: '使用工作流信息' }))
    expect(screen.getByLabelText('页面标题')).toHaveValue('工作流名称')
    expect(screen.getByLabelText('页面说明')).toHaveValue('工作流说明')
    expect(screen.getByLabelText('强调色')).toHaveValue('indigo')
    expect(screen.getByLabelText('提交按钮文案')).toHaveValue('运行 Agent')
    expect(screen.getByLabelText('结果展示方式')).toHaveValue('auto')
  })

  it('阻止空白和超长内容并显示中文错误', async () => {
    renderDialog()
    const title = screen.getByLabelText('页面标题')
    await userEvent.clear(title)
    expect(screen.getByText('页面标题不能为空')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存设置' })).toBeDisabled()
    await userEvent.type(title, 'a'.repeat(81))
    expect(screen.getByText('页面标题不能超过 80 个字符')).toBeInTheDocument()
    await userEvent.clear(screen.getByLabelText('页面说明'))
    await userEvent.type(screen.getByLabelText('页面说明'), 'b'.repeat(501))
    expect(screen.getByText('页面说明不能超过 500 个字符')).toBeInTheDocument()
    await userEvent.clear(screen.getByLabelText('提交按钮文案'))
    expect(screen.getByText('提交按钮文案不能为空')).toBeInTheDocument()
    await userEvent.type(screen.getByLabelText('提交按钮文案'), 'c'.repeat(25))
    expect(screen.getByText('提交按钮文案不能超过 24 个字符')).toBeInTheDocument()
  })

  it('保存 trim 后完整设置且服务端错误不关闭', async () => {
    const { props, rerender } = renderDialog()
    await userEvent.clear(screen.getByLabelText('页面标题'))
    await userEvent.type(screen.getByLabelText('页面标题'), ' 研究助手 ')
    await userEvent.clear(screen.getByLabelText('提交按钮文案'))
    await userEvent.type(screen.getByLabelText('提交按钮文案'), ' 开始 ')
    await userEvent.click(screen.getByRole('button', { name: '保存设置' }))
    expect(props.onSave).toHaveBeenCalledWith({ ...value, title: '研究助手', submitLabel: '开始' })
    rerender(<AgentPageSettingsDialog {...props} error="保存失败，请重试" />)
    expect(screen.getByRole('alert')).toHaveTextContent('保存失败，请重试')
    expect(props.onClose).not.toHaveBeenCalled()
  })

  it('修改后关闭需确认，未修改时直接关闭', async () => {
    const dirty = renderDialog()
    await userEvent.type(screen.getByLabelText('页面标题'), '新')
    await userEvent.click(screen.getByRole('button', { name: '关闭页面设置' }))
    expect(screen.getByText('放弃未保存的页面设置？')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '继续编辑' }))
    expect(screen.queryByText('放弃未保存的页面设置？')).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '关闭页面设置' }))
    await userEvent.click(screen.getByRole('button', { name: '放弃更改' }))
    expect(dirty.props.onClose).toHaveBeenCalledTimes(1)

    const clean = renderDialog()
    await userEvent.click(screen.getAllByRole('button', { name: '关闭页面设置' }).at(-1)!)
    expect(clean.props.onClose).toHaveBeenCalledTimes(1)
  })
})
