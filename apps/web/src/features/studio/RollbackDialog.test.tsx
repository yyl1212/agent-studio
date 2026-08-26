import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { RollbackDialog } from './RollbackDialog'

const summary = { total: 5, nodes: 2, startParameters: 1, connections: 1, agentPresentation: 1, layout: 0 }

describe('RollbackDialog', () => {
	it('展示恢复边界、差异计数，并支持 Escape 取消和焦点恢复', async () => {
		const trigger = document.createElement('button')
		trigger.textContent = '触发恢复'
		document.body.append(trigger)
		trigger.focus()
		const onCancel = vi.fn()
		const props = { open: true, targetVersion: 1, draftRevision: 8, summary, submitting: false, error: '', onConfirm: vi.fn(), onCancel }
		const { rerender } = render(<RollbackDialog {...props} />)
		const dialog = screen.getByRole('dialog', { name: '恢复 v1 为草稿？' })
		expect(dialog).toHaveTextContent('当前草稿 r8')
		expect(dialog).toHaveTextContent('节点 2')
		expect(dialog).toHaveTextContent('自动保存一个回滚前草稿检查点')
		expect(dialog).toHaveTextContent('不会改变线上 Agent、历史版本或历史运行')
		expect(screen.getByRole('button', { name: '确认恢复' })).toHaveFocus()
		await userEvent.keyboard('{Escape}')
		expect(onCancel).toHaveBeenCalledTimes(1)
		rerender(<RollbackDialog {...props} open={false} />)
		expect(trigger).toHaveFocus()
		trigger.remove()
	})

	it('提交中禁止取消、确认和 Escape，并以 alert 展示错误', async () => {
		const onCancel = vi.fn()
		render(<RollbackDialog open targetVersion={2} draftRevision={9} summary={summary} submitting error="revision 已变化" onConfirm={vi.fn()} onCancel={onCancel} />)
		expect(screen.getByRole('button', { name: '恢复中…' })).toBeDisabled()
		expect(screen.getByRole('button', { name: '取消' })).toBeDisabled()
		expect(screen.getByRole('alert')).toHaveTextContent('revision 已变化')
		await userEvent.keyboard('{Escape}')
		expect(onCancel).not.toHaveBeenCalled()
	})
})
