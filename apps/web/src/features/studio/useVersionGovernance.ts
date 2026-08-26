import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import {
	APIError,
	api,
	type RollbackCheckpointSummary,
	type Workflow,
	type WorkflowDiff,
	type WorkflowSnapshotRef,
	type WorkflowVersionSummary,
} from '../../lib/api/client'
import type { SaveState } from './saveQueue'

export interface UseVersionGovernanceOptions {
	workflow: Workflow
	saveState: SaveState
	editSerial: number
	archived: boolean
	onApplyWorkflow: (workflow: Workflow) => Promise<void>
	onLockChange: (locked: boolean) => void
}

export interface VersionGovernanceModel {
	versions: WorkflowVersionSummary[]
	nextCursor?: string
	checkpoint?: RollbackCheckpointSummary
	base?: WorkflowSnapshotRef
	compare?: WorkflowSnapshotRef
	diff?: WorkflowDiff
	loading: boolean
	loadingMore: boolean
	diffLoading: boolean
	mutating: boolean
	locked: boolean
	error: string
	notice: string
	rollbackTarget?: number
	setBase: (ref: WorkflowSnapshotRef) => void
	setCompare: (ref: WorkflowSnapshotRef) => void
	loadMore: () => Promise<void>
	refresh: () => Promise<void>
	openRollback: (version: number) => void
	closeRollback: () => void
	confirmRollback: () => Promise<void>
	undoRollback: () => Promise<void>
}

export function useVersionGovernance(options: UseVersionGovernanceOptions): VersionGovernanceModel {
	const initialBase = options.workflow.publishedVersion
		? { kind: 'version' as const, version: options.workflow.publishedVersion }
		: undefined
	const [versions, setVersions] = useState<WorkflowVersionSummary[]>([])
	const [nextCursor, setNextCursor] = useState<string>()
	const [checkpoint, setCheckpoint] = useState<RollbackCheckpointSummary>()
	const [base, setBase] = useState<WorkflowSnapshotRef | undefined>(initialBase)
	const [compare, setCompare] = useState<WorkflowSnapshotRef>({ kind: 'draft', draftRevision: options.workflow.draftRevision })
	const [diff, setDiff] = useState<WorkflowDiff>()
	const [loading, setLoading] = useState(true)
	const [loadingMore, setLoadingMore] = useState(false)
	const [diffLoading, setDiffLoading] = useState(false)
	const [mutating, setMutating] = useState(false)
	const [error, setError] = useState('')
	const [notice, setNotice] = useState('')
	const [rollbackTarget, setRollbackTarget] = useState<number>()
	const [listRefresh, setListRefresh] = useState(0)
	const [diffRefresh, setDiffRefresh] = useState(0)
	const listController = useRef<AbortController | undefined>(undefined)
	const diffController = useRef<AbortController | undefined>(undefined)
	const mutationController = useRef<AbortController | undefined>(undefined)
	const listGeneration = useRef(0)
	const diffGeneration = useRef(0)
	const revision = useRef(options.workflow.draftRevision)
	const lockChange = useRef(options.onLockChange)
	const checkpointEditSerial = useRef<number | undefined>(undefined)

	revision.current = options.workflow.draftRevision > revision.current ? options.workflow.draftRevision : revision.current
	lockChange.current = options.onLockChange

	useEffect(() => {
		setVersions([])
		setNextCursor(undefined)
		setCheckpoint(undefined)
		checkpointEditSerial.current = undefined
		setBase(options.workflow.publishedVersion ? { kind: 'version', version: options.workflow.publishedVersion } : undefined)
		setCompare({ kind: 'draft', draftRevision: options.workflow.draftRevision })
		setDiff(undefined)
		revision.current = options.workflow.draftRevision
	}, [options.workflow.id])

	useEffect(() => {
		setBase((current) => current ?? (options.workflow.publishedVersion ? { kind: 'version', version: options.workflow.publishedVersion } : undefined))
	}, [options.workflow.publishedVersion])

	useEffect(() => {
		setBase((current) => current?.kind === 'draft' ? { kind: 'draft', draftRevision: options.workflow.draftRevision } : current)
		setCompare((current) => current.kind === 'draft' ? { kind: 'draft', draftRevision: options.workflow.draftRevision } : current)
	}, [options.workflow.draftRevision])

	useEffect(() => {
		const mine = ++listGeneration.current
		listController.current?.abort()
		const controller = new AbortController()
		listController.current = controller
		setLoading(true)
		setError('')
		api.listWorkflowVersions(options.workflow.id, { limit: 20 }, controller.signal).then((page) => {
			if (mine !== listGeneration.current || controller.signal.aborted) return
			setVersions(sortAndDeduplicate(page.items))
			setNextCursor(page.nextCursor ?? undefined)
			setCheckpoint(page.rollbackCheckpoint ?? undefined)
			checkpointEditSerial.current = page.rollbackCheckpoint ? options.editSerial : undefined
		}).catch((cause: unknown) => {
			if (mine === listGeneration.current && !isAbort(cause)) setError(publicError(cause, '加载版本记录失败，请稍后重试'))
		}).finally(() => {
			if (mine === listGeneration.current && !controller.signal.aborted) setLoading(false)
		})
		return () => controller.abort()
	}, [options.workflow.id, listRefresh])

	useEffect(() => {
		if (!checkpoint || checkpointEditSerial.current === undefined || checkpointEditSerial.current === options.editSerial) return
		setCheckpoint(undefined)
		checkpointEditSerial.current = undefined
		setNotice('草稿已修改，回滚撤销已失效')
	}, [checkpoint, options.editSerial])

	useEffect(() => {
		diffController.current?.abort()
		setDiff(undefined)
		if (!base || !compare) {
			setDiffLoading(false)
			return
		}
		const mine = ++diffGeneration.current
		const controller = new AbortController()
		diffController.current = controller
		setDiffLoading(true)
		setError('')
		api.diffWorkflowVersions(options.workflow.id, { base, compare }, controller.signal).then((nextDiff) => {
			if (mine === diffGeneration.current && !controller.signal.aborted) setDiff(nextDiff)
		}).catch((cause: unknown) => {
			if (mine === diffGeneration.current && !isAbort(cause)) setError(publicError(cause, '加载版本差异失败，请稍后重试'))
		}).finally(() => {
			if (mine === diffGeneration.current && !controller.signal.aborted) setDiffLoading(false)
		})
		return () => controller.abort()
	}, [options.workflow.id, snapshotKey(base), snapshotKey(compare), diffRefresh])

	const locked = mutating || rollbackTarget !== undefined
	useEffect(() => {
		lockChange.current(locked)
		return () => lockChange.current(false)
	}, [locked])

	useEffect(() => () => {
		listController.current?.abort()
		diffController.current?.abort()
		mutationController.current?.abort()
		lockChange.current(false)
	}, [])

	const loadMore = useCallback(async () => {
		if (!nextCursor || loadingMore) return
		const mine = ++listGeneration.current
		listController.current?.abort()
		const controller = new AbortController()
		listController.current = controller
		setLoadingMore(true)
		setError('')
		try {
			const page = await api.listWorkflowVersions(options.workflow.id, { limit: 20, cursor: nextCursor }, controller.signal)
			if (mine !== listGeneration.current || controller.signal.aborted) return
			setVersions((current) => sortAndDeduplicate([...current, ...page.items]))
			setNextCursor(page.nextCursor ?? undefined)
			setCheckpoint(page.rollbackCheckpoint ?? undefined)
			checkpointEditSerial.current = page.rollbackCheckpoint ? options.editSerial : undefined
		} catch (cause) {
			if (mine === listGeneration.current && !isAbort(cause)) setError(publicError(cause, '加载更多版本失败，请稍后重试'))
		} finally {
			if (mine === listGeneration.current && !controller.signal.aborted) setLoadingMore(false)
		}
	}, [loadingMore, nextCursor, options.workflow.id])

	const refresh = useCallback(async () => {
		setListRefresh((value) => value + 1)
		setDiffRefresh((value) => value + 1)
	}, [])

	const confirmRollback = useCallback(async () => {
		if (rollbackTarget === undefined || mutating) return
		mutationController.current?.abort()
		const controller = new AbortController()
		mutationController.current = controller
		setMutating(true)
		setError('')
		setNotice('')
		const submittedRevision = revision.current
		try {
			const result = await api.rollbackWorkflow(options.workflow.id, { targetVersion: rollbackTarget, expectedDraftRevision: submittedRevision }, controller.signal)
			if (controller.signal.aborted) return
			await options.onApplyWorkflow(result.workflow)
			revision.current = result.workflow.draftRevision
			setBase({ kind: 'version', version: rollbackTarget })
			setCompare({ kind: 'draft', draftRevision: result.workflow.draftRevision })
			setCheckpoint(result.rollbackCheckpoint)
			checkpointEditSerial.current = options.editSerial
			setRollbackTarget(undefined)
			setNotice(`已回滚到版本 ${rollbackTarget}`)
			setDiffRefresh((value) => value + 1)
		} catch (cause) {
			if (isAbort(cause)) return
			let recovered = false
			if (!(cause instanceof APIError)) {
				try {
					const [freshWorkflow, freshPage] = await Promise.all([
						api.getWorkflow(options.workflow.id, controller.signal),
						api.listWorkflowVersions(options.workflow.id, { limit: 20 }, controller.signal),
					])
					const freshCheckpoint = freshPage.rollbackCheckpoint
					recovered = freshWorkflow.draftRevision > submittedRevision
						&& freshCheckpoint?.restoredRevision === freshWorkflow.draftRevision
						&& freshCheckpoint.restoredFromVersion === rollbackTarget
					if (recovered && freshCheckpoint) {
						await options.onApplyWorkflow(freshWorkflow)
						revision.current = freshWorkflow.draftRevision
						setVersions(sortAndDeduplicate(freshPage.items))
						setNextCursor(freshPage.nextCursor ?? undefined)
						setBase({ kind: 'version', version: rollbackTarget })
						setCompare({ kind: 'draft', draftRevision: freshWorkflow.draftRevision })
						setCheckpoint(freshCheckpoint)
						checkpointEditSerial.current = options.editSerial
						setRollbackTarget(undefined)
						setNotice('回滚已完成，状态已刷新')
						setDiffRefresh((value) => value + 1)
					}
				} catch (recoveryCause) {
					if (isAbort(recoveryCause)) return
				}
			}
			if (!recovered) setError(publicError(cause, '回滚失败，请稍后重试'))
		} finally {
			if (!controller.signal.aborted) setMutating(false)
		}
	}, [mutating, options.editSerial, options.onApplyWorkflow, options.workflow.id, rollbackTarget])

	const undoRollback = useCallback(async () => {
		if (!checkpoint || mutating) return
		mutationController.current?.abort()
		const controller = new AbortController()
		mutationController.current = controller
		setMutating(true)
		setError('')
		setNotice('')
		try {
			const nextWorkflow = await api.undoWorkflowRollback(options.workflow.id, { expectedDraftRevision: revision.current }, controller.signal)
			if (controller.signal.aborted) return
			await options.onApplyWorkflow(nextWorkflow)
			revision.current = nextWorkflow.draftRevision
			setCompare({ kind: 'draft', draftRevision: nextWorkflow.draftRevision })
			setCheckpoint(undefined)
			checkpointEditSerial.current = undefined
			setNotice('已撤销回滚')
			setListRefresh((value) => value + 1)
			setDiffRefresh((value) => value + 1)
		} catch (cause) {
			if (!isAbort(cause)) setError(publicError(cause, '撤销回滚失败，请稍后重试'))
		} finally {
			if (!controller.signal.aborted) setMutating(false)
		}
	}, [checkpoint, mutating, options.onApplyWorkflow, options.workflow.id])

	return useMemo(() => ({
		versions, nextCursor, checkpoint, base, compare, diff, loading, loadingMore, diffLoading, mutating, locked, error, notice, rollbackTarget,
		setBase, setCompare, loadMore, refresh,
		openRollback: (version: number) => { if (!mutating) setRollbackTarget(version) },
		closeRollback: () => { if (!mutating) setRollbackTarget(undefined) },
		confirmRollback, undoRollback,
	}), [versions, nextCursor, checkpoint, base, compare, diff, loading, loadingMore, diffLoading, mutating, locked, error, notice, rollbackTarget, loadMore, refresh, confirmRollback, undoRollback])
}

function sortAndDeduplicate(items: WorkflowVersionSummary[]) {
	return [...new Map(items.map((item) => [item.id, item])).values()].sort((left, right) => right.version - left.version)
}

function snapshotKey(ref?: WorkflowSnapshotRef) {
	if (!ref) return ''
	return ref.kind === 'draft' ? `draft:${ref.draftRevision}` : `version:${ref.version}`
}

function isAbort(error: unknown) {
	return error instanceof DOMException && error.name === 'AbortError'
}

function publicError(error: unknown, fallback: string) {
	if (error instanceof APIError) return `${error.message}${error.requestId ? ` · Request ID: ${error.requestId}` : ''}`
	return fallback
}
