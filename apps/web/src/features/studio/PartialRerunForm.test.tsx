import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'

import type { RerunPreview } from '../../lib/api/client'
import { PartialRerunForm } from './PartialRerunForm'

const preview = (safety: RerunPreview['effectiveSafety'], requiresConfirmation = false): RerunPreview => ({
	sourceRunId: 'r1', sourceNodeId: 'webhook-1', entryInput: { body: [{ ok: true }] }, entryInputRedactedPaths: [], frozenEdges: [],
	activeNodes: [{ id: 'webhook-1', type: 'extension.webhook', version: '1.0.0', title: 'Webhook', safety }],
	effectiveSafety: safety, requiresConfirmation,
})

describe('PartialRerunForm', () => {
	it('pure 可直接提交并严格解析入口对象', async () => {
		const onSubmit = vi.fn()
		render(<PartialRerunForm preview={preview('pure')} events={[]} running={false} error="" onSubmit={onSubmit} onCancel={vi.fn()} onClose={vi.fn()} />)
		await userEvent.click(screen.getByRole('button', { name: '开始局部重跑' }))
		expect(onSubmit).toHaveBeenCalledWith({ body: [{ ok: true }] }, false)
		fireEvent.change(screen.getByLabelText('入口输入 JSON'), { target: { value: '[]' } })
		await userEvent.click(screen.getByRole('button', { name: '开始局部重跑' }))
		expect(screen.getByRole('alert')).toHaveTextContent('入口输入必须是 JSON 对象')
		fireEvent.change(screen.getByLabelText('入口输入 JSON'), { target: { value: '{' } })
		await userEvent.click(screen.getByRole('button', { name: '开始局部重跑' }))
		expect(screen.getByRole('alert')).toHaveTextContent('入口输入必须是合法 JSON')
		expect(onSubmit).toHaveBeenCalledTimes(1)
	})

	it('read_only 提示可能费用但不强制确认', () => {
		render(<PartialRerunForm preview={preview('read_only')} events={[]} running={false} error="" onSubmit={vi.fn()} onCancel={vi.fn()} onClose={vi.fn()} />)
		expect(screen.getByText(/可能产生模型调用费用/)).toBeInTheDocument()
		expect(screen.queryByRole('checkbox')).not.toBeInTheDocument()
		expect(screen.getByRole('button', { name: '开始局部重跑' })).toBeEnabled()
	})

	it('side_effect 展示活动节点并要求不可撤销确认', async () => {
		render(<PartialRerunForm preview={preview('side_effect', true)} events={[]} running={false} error="" onSubmit={vi.fn()} onCancel={vi.fn()} onClose={vi.fn()} />)
		expect(screen.getByText('Webhook · side_effect')).toBeInTheDocument()
		const submit = screen.getByRole('button', { name: '开始局部重跑' })
		expect(submit).toBeDisabled()
		await userEvent.click(screen.getByRole('checkbox', { name: /我了解外部操作无法撤销/ }))
		expect(submit).toBeEnabled()
	})

	it('未知安全等级保守要求不可撤销确认', () => {
		const unknown = { ...preview('side_effect'), effectiveSafety: 'future_value', requiresConfirmation: false } as unknown as RerunPreview
		render(<PartialRerunForm preview={unknown} events={[]} running={false} error="" onSubmit={vi.fn()} onCancel={vi.fn()} onClose={vi.fn()} />)
		expect(screen.getByRole('checkbox', { name: /我了解外部操作无法撤销/ })).toBeInTheDocument()
		expect(screen.getByRole('button', { name: '开始局部重跑' })).toBeDisabled()
	})

	it('服务端失败后保留输入、聚焦错误并提供新运行链接和取消', async () => {
		const onCancel = vi.fn()
		const { rerender } = render(<MemoryRouter><PartialRerunForm preview={preview('pure')} events={[]} running={false} error="" onSubmit={vi.fn()} onCancel={onCancel} onClose={vi.fn()} /></MemoryRouter>)
		fireEvent.change(screen.getByLabelText('入口输入 JSON'), { target: { value: '{"body":[{"edited":true}]}' } })
		rerender(<MemoryRouter><PartialRerunForm preview={preview('pure')} events={[]} running error="确认失败" debugRunPath="/workflows/w1/runs/debug-1/debug" onSubmit={vi.fn()} onCancel={onCancel} onClose={vi.fn()} /></MemoryRouter>)
		expect(screen.getByLabelText('入口输入 JSON')).toHaveValue('{"body":[{"edited":true}]}')
		expect(screen.getByRole('alert')).toHaveFocus()
		expect(screen.getByRole('link', { name: '打开新调试运行' })).toHaveAttribute('href', '/workflows/w1/runs/debug-1/debug')
		await userEvent.click(screen.getByRole('button', { name: '取消运行' }))
		expect(onCancel).toHaveBeenCalled()
	})
})
