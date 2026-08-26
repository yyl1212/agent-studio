import { useEffect, useRef } from 'react'

import type { WorkflowDiff } from '../../lib/api/client'

interface RollbackDialogProps {
	open: boolean
	targetVersion: number
	draftRevision: number
	summary: WorkflowDiff['summary']
	submitting: boolean
	error: string
	onConfirm: () => void
	onCancel: () => void
}

export function RollbackDialog(props: RollbackDialogProps) {
	const confirmRef = useRef<HTMLButtonElement>(null)
	const restoreFocus = useRef<HTMLElement | null>(null)

	useEffect(() => {
		if (!props.open) return
		restoreFocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
		confirmRef.current?.focus()
		return () => restoreFocus.current?.focus()
	}, [props.open])

	useEffect(() => {
		if (!props.open) return
		const handleEscape = (event: KeyboardEvent) => {
			if (event.key !== 'Escape') return
			event.preventDefault()
			event.stopPropagation()
			if (!props.submitting) props.onCancel()
		}
		document.addEventListener('keydown', handleEscape, true)
		return () => document.removeEventListener('keydown', handleEscape, true)
	}, [props.open, props.submitting, props.onCancel])

	if (!props.open) return null
	return (
		<div className="dialog-backdrop">
			<dialog open aria-modal="true" aria-labelledby="rollback-dialog-title" className="rollback-dialog">
				<h2 id="rollback-dialog-title">恢复 v{props.targetVersion} 为草稿？</h2>
				<p>将用 v{props.targetVersion} 覆盖当前草稿 r{props.draftRevision}，并自动保存一个回滚前草稿检查点。</p>
				<p>此操作不会改变线上 Agent、历史版本或历史运行。</p>
				<ul aria-label="差异摘要">
					<li>节点 {props.summary.nodes}</li>
					<li>开始参数 {props.summary.startParameters}</li>
					<li>连线 {props.summary.connections}</li>
					<li>Agent 页面 {props.summary.agentPresentation}</li>
					<li>画布布局 {props.summary.layout}</li>
				</ul>
				{props.error && <p className="form-error" role="alert">{props.error}</p>}
				<div className="dialog-actions">
					<button type="button" disabled={props.submitting} onClick={props.onCancel}>取消</button>
					<button ref={confirmRef} type="button" className="danger-button" disabled={props.submitting} onClick={props.onConfirm}>{props.submitting ? '恢复中…' : '确认恢复'}</button>
				</div>
			</dialog>
		</div>
	)
}
