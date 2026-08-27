import { useEffect, useRef, useState, type ChangeEvent } from 'react'

import type { WorkflowDiff, WorkflowSnapshotRef } from '../../lib/api/client'
import { RollbackDialog } from './RollbackDialog'
import { VersionDiffView } from './VersionDiffView'
import { useVersionGovernance, type UseVersionGovernanceOptions } from './useVersionGovernance'

interface VersionGovernancePanelProps extends UseVersionGovernanceOptions {
	titleId: string
}

export function VersionGovernancePanel({ titleId, ...options }: VersionGovernancePanelProps) {
	const model = useVersionGovernance(options)
	const headingRef = useRef<HTMLHeadingElement>(null)
	const [rollbackSummary, setRollbackSummary] = useState<WorkflowDiff['summary']>()

	useEffect(() => {
		headingRef.current?.focus()
	}, [])
	useEffect(() => {
		if (model.rollbackTarget === undefined) setRollbackSummary(undefined)
	}, [model.rollbackTarget])

	const rollbackVersion = model.base?.kind === 'version' ? model.base.version : undefined
	const rollbackEnabled = !options.archived && options.saveState === 'saved' && !model.diffLoading
		&& model.diff !== undefined && model.diff.summary.total > 0
		&& model.base?.kind === 'version' && model.compare?.kind === 'draft' && !model.mutating
	const controlsDisabled = model.mutating || model.rollbackTarget !== undefined
	const select = (setter: (ref: WorkflowSnapshotRef) => void) => (event: ChangeEvent<HTMLSelectElement>) => {
		const [kind, rawValue] = event.target.value.split(':')
		const value = Number(rawValue)
		setter(kind === 'draft' ? { kind: 'draft', draftRevision: value } : { kind: 'version', version: value })
	}

	return (
		<section className="version-governance version-governance-panel" aria-labelledby={titleId}>
			<header className="version-governance-heading">
				<div><span className="node-category">工作流治理</span><h2 ref={headingRef} id={titleId} tabIndex={-1}>版本历史</h2></div>
				{model.loading && <span role="status">正在加载版本…</span>}
			</header>
			{model.notice && <p className="form-success" role="status">{model.notice}</p>}
			{model.error && <div><p className="form-error" role="alert">{model.error}</p><button type="button" disabled={controlsDisabled} onClick={() => void model.refresh()}>重试</button></div>}

			<section className="version-list version-timeline" aria-label="版本时间线">
				{!model.loading && model.versions.length === 0 ? <p className="empty-state">尚未发布版本</p> : <ol>{model.versions.map((version) => (
					<li key={version.id} className={version.current ? 'current' : undefined}>
						<button type="button" disabled={controlsDisabled} aria-pressed={model.base?.kind === 'version' && model.base.version === version.version} onClick={() => model.setBase({ kind: 'version', version: version.version })}>
							<strong>v{version.version}{version.current ? <span className="version-current-badge"> · 当前发布</span> : ''}</strong>
							<time dateTime={version.createdAt}>{new Date(version.createdAt).toLocaleString('zh-CN')}</time>
						</button>
					</li>
				))}</ol>}
				{model.nextCursor && <button type="button" disabled={controlsDisabled || model.loadingMore} onClick={() => void model.loadMore()}>{model.loadingMore ? '加载中…' : '加载更多版本'}</button>}
			</section>

			<div className="version-compare-controls">
				<SnapshotSelect label="比较起点" value={model.base} versions={model.versions} draftRevision={options.workflow.draftRevision} disabled={controlsDisabled} onChange={select(model.setBase)} />
				<span aria-hidden="true">→</span>
				<SnapshotSelect label="比较终点" value={model.compare} versions={model.versions} draftRevision={options.workflow.draftRevision} disabled={controlsDisabled} onChange={select(model.setCompare)} />
			</div>

			{model.diffLoading ? <p role="status">正在计算差异…</p> : model.diff ? <VersionDiffView diff={model.diff} /> : !model.loading && model.versions.length > 0 ? <p className="empty-state">请选择两个快照进行比较</p> : null}

			<div className="version-governance-actions">
				{options.archived ? <p>请先恢复工作流</p> : rollbackVersion !== undefined && <button type="button" className="danger-button" disabled={!rollbackEnabled || controlsDisabled} onClick={() => {
					if (!model.diff) return
					setRollbackSummary(model.diff.summary)
					model.openRollback(rollbackVersion)
				}}>恢复 v{rollbackVersion} 为草稿</button>}
				{model.checkpoint && !options.archived && <button type="button" disabled={controlsDisabled} onClick={() => void model.undoRollback()}>{model.mutating ? '撤销中…' : '撤销回滚'}</button>}
			</div>

			{model.rollbackTarget !== undefined && rollbackSummary && <RollbackDialog
				open targetVersion={model.rollbackTarget} draftRevision={options.workflow.draftRevision}
				summary={rollbackSummary} submitting={model.mutating} error={model.error}
				onConfirm={() => void model.confirmRollback()} onCancel={() => {
					setRollbackSummary(undefined)
					model.closeRollback()
				}}
			/>}
		</section>
	)
}

function SnapshotSelect(props: {
	label: string
	value?: WorkflowSnapshotRef
	versions: ReturnType<typeof useVersionGovernance>['versions']
	draftRevision: number
	disabled: boolean
	onChange: (event: ChangeEvent<HTMLSelectElement>) => void
}) {
	return <label><span>{props.label}</span><select aria-label={props.label} value={snapshotValue(props.value)} disabled={props.disabled} onChange={props.onChange}>
		{!props.value && <option value="">未选择</option>}
		{props.versions.map((version) => <option key={version.id} value={`version:${version.version}`}>v{version.version}{version.current ? ' · 当前发布' : ''}</option>)}
		<option value={`draft:${props.draftRevision}`}>当前草稿 r{props.draftRevision}</option>
	</select></label>
}

function snapshotValue(ref?: WorkflowSnapshotRef) {
	if (!ref) return ''
	return ref.kind === 'draft' ? `draft:${ref.draftRevision}` : `version:${ref.version}`
}
