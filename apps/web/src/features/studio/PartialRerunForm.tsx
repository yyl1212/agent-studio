import { useEffect, useRef, useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'

import type { RerunPreview } from '../../lib/api/client'
import type { RunEvent } from '../../lib/api/ndjson'

interface PartialRerunFormProps {
	preview: RerunPreview
	events: RunEvent[]
	running: boolean
	error: string
	debugRunPath?: string
	onSubmit: (entryInput: Record<string, unknown>, confirmed: boolean) => void | Promise<void>
	onCancel: () => void
	onClose: () => void
}

export function PartialRerunForm(props: PartialRerunFormProps) {
	const [source, setSource] = useState(() => JSON.stringify(props.preview.entryInput, null, 2))
	const [confirmed, setConfirmed] = useState(false)
	const [parseError, setParseError] = useState('')
	const errorRef = useRef<HTMLParagraphElement>(null)
	const knownSafety = ['pure', 'read_only', 'side_effect'].includes(props.preview.effectiveSafety)
	const confirmationRequired = props.preview.requiresConfirmation || !knownSafety
	const visibleError = parseError || props.error

	useEffect(() => {
		if (visibleError) errorRef.current?.focus()
	}, [visibleError])

	const submit = async (event: FormEvent) => {
		event.preventDefault()
		let parsed: unknown
		try {
			parsed = JSON.parse(source)
		} catch {
			setParseError('入口输入必须是合法 JSON')
			return
		}
		if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
			setParseError('入口输入必须是 JSON 对象')
			return
		}
		setParseError('')
		await props.onSubmit(parsed as Record<string, unknown>, confirmationRequired && confirmed)
	}

	return (
		<section className="partial-rerun" aria-label="局部重跑">
		<header><div><p className="eyebrow">PARTIAL RERUN</p><h2>从 {props.preview.sourceNodeId} 重新运行</h2></div><button type="button" aria-label="关闭局部重跑" disabled={props.running} onClick={props.onClose}>×</button></header>
		<p className={`safety-notice safety-${props.preview.effectiveSafety}`}>
			{props.preview.effectiveSafety === 'pure' ? '纯计算节点：不会访问外部服务。' : props.preview.effectiveSafety === 'read_only' ? '只读节点：可能产生模型调用费用，但不会写入外部系统。' : '副作用节点：可能写入或调用外部系统。'}
		</p>
		<div><h3>活动节点</h3><ul>{props.preview.activeNodes.map((node) => <li key={node.id}>{node.title} · {node.safety}</li>)}</ul></div>
		{props.preview.frozenEdges.length > 0 && <div><h3>历史冻结边</h3><ul>{props.preview.frozenEdges.map((edge) => <li key={edge.edgeId}>{edge.source}.{edge.sourcePort} → {edge.target}.{edge.targetPort} · {edge.active ? 'active' : 'inactive'} · 历史冻结</li>)}</ul></div>}
		{props.preview.entryInputRedactedPaths.length > 0 && <div className="debug-warning"><strong>以下路径需要重新输入</strong><ul>{props.preview.entryInputRedactedPaths.map((path) => <li key={path}>{path}</li>)}</ul></div>}
		<form onSubmit={submit}>
			<label>入口输入 JSON<textarea aria-label="入口输入 JSON" value={source} disabled={props.running} onChange={(event) => setSource(event.target.value)} /></label>
			{confirmationRequired && <label className="side-effect-confirmation"><input type="checkbox" checked={confirmed} disabled={props.running} onChange={(event) => setConfirmed(event.target.checked)} />我了解外部操作无法撤销</label>}
			{visibleError && <p ref={errorRef} className="form-error" role="alert" tabIndex={-1}>{visibleError}</p>}
			<div className="rerun-actions">
				<button className="primary-button" type="submit" disabled={props.running || (confirmationRequired && !confirmed)}>{props.running ? '运行中…' : '开始局部重跑'}</button>
				{props.running && <button type="button" onClick={props.onCancel}>取消运行</button>}
				<button type="button" disabled={props.running} onClick={props.onClose}>返回回放</button>
			</div>
		</form>
		<section className="rerun-progress" aria-live="polite">
			<h3>新运行进度</h3>
			{props.events.length === 0 ? <p>提交后将在这里显示实时事件。</p> : <ol>{props.events.map((event) => <li key={event.sequence}>#{event.sequence} {event.type}{event.nodeId ? ` · ${event.nodeId}` : ''}</li>)}</ol>}
			{props.debugRunPath && <Link to={props.debugRunPath}>打开新调试运行</Link>}
		</section>
	</section>
	)
}
