import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { Workflow, WorkflowDiff } from '../../lib/api/client'
import type { VersionGovernanceModel } from './useVersionGovernance'

const useVersionGovernanceMock = vi.hoisted(() => vi.fn())
vi.mock('./useVersionGovernance', async (importOriginal) => ({
	...(await importOriginal<typeof import('./useVersionGovernance')>()),
	useVersionGovernance: useVersionGovernanceMock,
}))

import { VersionGovernancePanel } from './VersionGovernancePanel'

const workflow: Workflow = {
	id: 'w1', name: '演示', slug: 'demo', description: '', draftRevision: 8, publishedVersion: 2,
	agentPresentation: { title: '演示', description: '', accent: 'indigo', submitLabel: '运行', resultMode: 'auto' },
	draftGraph: { schemaVersion: 1, nodes: [], edges: [] }, createdAt: '2026-08-27T00:00:00Z', updatedAt: '2026-08-27T00:00:00Z',
}
const diff: WorkflowDiff = {
	base: { kind: 'version', version: 1, versionId: 'v1', createdAt: '2026-08-27T00:00:00Z' }, compare: { kind: 'draft', draftRevision: 8 },
	summary: { total: 2, nodes: 1, startParameters: 0, connections: 0, agentPresentation: 1, layout: 0 }, truncated: true,
	groups: { nodes: [], startParameters: [], connections: [], agentPresentation: [], layout: [] },
}
const model = (): VersionGovernanceModel => ({
	versions: [
		{ id: 'v2', version: 2, current: true, createdAt: '2026-08-27T02:00:00Z' },
		{ id: 'v1', version: 1, current: false, createdAt: '2026-08-27T01:00:00Z' },
	],
	nextCursor: 'next', base: { kind: 'version', version: 1 }, compare: { kind: 'draft', draftRevision: 8 }, diff,
	loading: false, loadingMore: false, diffLoading: false, mutating: false, locked: false, error: '', notice: '',
	setBase: vi.fn(), setCompare: vi.fn(), loadMore: vi.fn(async () => undefined), refresh: vi.fn(async () => undefined),
	openRollback: vi.fn(), closeRollback: vi.fn(), confirmRollback: vi.fn(async () => undefined), undoRollback: vi.fn(async () => undefined),
})
const props = { titleId: 'version-title', workflow, saveState: 'saved' as const, editSerial: 0, archived: false, onApplyWorkflow: vi.fn(async () => undefined), onLockChange: vi.fn() }

describe('VersionGovernancePanel', () => {
	beforeEach(() => useVersionGovernanceMock.mockReset())

	it('展示当前版本、草稿比较、分页和恢复动作', async () => {
		const current = model()
		useVersionGovernanceMock.mockReturnValue(current)
		render(<VersionGovernancePanel {...props} />)
		expect(screen.getByRole('heading', { name: '版本历史' })).toHaveFocus()
		expect(screen.getAllByRole('option', { name: 'v2 · 当前发布' })).toHaveLength(2)
		expect(screen.getAllByRole('option', { name: '当前草稿 r8' })).toHaveLength(2)
		expect(screen.getByRole('status')).toHaveTextContent('仅展示前 500 项详细差异')
		await userEvent.click(screen.getByRole('button', { name: '加载更多版本' }))
		expect(current.loadMore).toHaveBeenCalled()
		await userEvent.click(screen.getByRole('button', { name: '恢复 v1 为草稿' }))
		expect(current.openRollback).toHaveBeenCalledWith(1)
	})

	it('支持切换比较快照并显示有效撤销入口', async () => {
		const current = { ...model(), checkpoint: { sourceRevision: 7, restoredRevision: 8, restoredFromVersion: 1, createdAt: '2026-08-27T03:00:00Z' } }
		useVersionGovernanceMock.mockReturnValue(current)
		render(<VersionGovernancePanel {...props} />)
		await userEvent.selectOptions(screen.getByLabelText('比较起点'), 'version:2')
		expect(current.setBase).toHaveBeenCalledWith({ kind: 'version', version: 2 })
		await userEvent.click(screen.getByRole('button', { name: '撤销回滚' }))
		expect(current.undoRollback).toHaveBeenCalled()
	})

	it('回滚确认期间冻结差异摘要并禁用底层治理控件', async () => {
		const current = {
			...model(),
			checkpoint: { sourceRevision: 7, restoredRevision: 8, restoredFromVersion: 1, createdAt: '2026-08-27T03:00:00Z' },
		}
		useVersionGovernanceMock.mockReturnValue(current)
		const { rerender } = render(<VersionGovernancePanel {...props} />)

		await userEvent.click(screen.getByRole('button', { name: '恢复 v1 为草稿' }))
		expect(current.openRollback).toHaveBeenCalledWith(1)

		useVersionGovernanceMock.mockReturnValue({ ...current, rollbackTarget: 1, diff: undefined })
		rerender(<VersionGovernancePanel {...props} />)

		const dialog = screen.getByRole('dialog', { name: '恢复 v1 为草稿？' })
		expect(dialog).toHaveTextContent('节点 1')
		expect(screen.getByLabelText('比较起点')).toBeDisabled()
		expect(screen.getByLabelText('比较终点')).toBeDisabled()
		expect(screen.getByRole('button', { name: '加载更多版本' })).toBeDisabled()
		expect(screen.getByRole('button', { name: '撤销回滚' })).toBeDisabled()
		expect(screen.getAllByRole('button', { name: /^v/ })).not.toHaveLength(0)
		for (const versionButton of screen.getAllByRole('button', { name: /^v/ })) expect(versionButton).toBeDisabled()

		useVersionGovernanceMock.mockReturnValue({ ...current, rollbackTarget: 1 })
		rerender(<VersionGovernancePanel {...props} />)
		expect(screen.getByRole('button', { name: '恢复 v1 为草稿' })).toBeDisabled()
	})

	it('覆盖未发布、加载失败、归档和不可恢复状态', async () => {
		const unpublished = { ...model(), versions: [], base: undefined, diff: undefined, nextCursor: undefined }
		useVersionGovernanceMock.mockReturnValue(unpublished)
		const { rerender } = render(<VersionGovernancePanel {...props} workflow={{ ...workflow, publishedVersion: null }} />)
		expect(screen.getByText('尚未发布版本')).toBeInTheDocument()
		expect(screen.queryByRole('button', { name: /恢复 v/ })).not.toBeInTheDocument()

		const failed = { ...model(), loading: false, error: '加载版本失败' }
		useVersionGovernanceMock.mockReturnValue(failed)
		rerender(<VersionGovernancePanel {...props} />)
		expect(screen.getByRole('alert')).toHaveTextContent('加载版本失败')
		await userEvent.click(screen.getByRole('button', { name: '重试' }))
		expect(failed.refresh).toHaveBeenCalled()

		useVersionGovernanceMock.mockReturnValue(model())
		rerender(<VersionGovernancePanel {...props} archived />)
		expect(screen.getByText('请先恢复工作流')).toBeInTheDocument()
		expect(screen.queryByRole('button', { name: '恢复 v1 为草稿' })).not.toBeInTheDocument()

		rerender(<VersionGovernancePanel {...props} saveState="saving" />)
		expect(screen.getByRole('button', { name: '恢复 v1 为草稿' })).toBeDisabled()
	})
})
