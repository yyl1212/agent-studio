import type { DebugOverview } from '../../lib/api/client'
import type { RunEvent } from '../../lib/api/ndjson'

interface NodeRunDetailProps {
	nodeID?: string
	node?: DebugOverview['graph']['nodes'][number]
	nodeRun?: DebugOverview['nodeRuns'][number]
	events: RunEvent[]
	onRerun?: (nodeID: string) => void
}

export function NodeRunDetail({ nodeID, node, nodeRun, events, onRerun }: NodeRunDetailProps) {
	if (!nodeID) return <section className="node-run-detail"><h2>节点详情</h2><p>从画布或时间线选择节点。</p></section>
	const started = events.find((event) => event.nodeId === nodeID && event.type === 'node.started')
	const terminal = events.find((event) => event.nodeId === nodeID && ['node.completed', 'node.failed', 'node.skipped', 'node.cancelled'].includes(event.type))
	const duration = started && terminal ? (new Date(terminal.timestamp).getTime() - new Date(started.timestamp).getTime()) / 1000 : undefined
	const redactedPaths = [...(started?.inputRedactedPaths ?? []), ...(terminal?.outputRedactedPaths ?? [])]
	return (
		<section className="node-run-detail" aria-label={`节点详情 ${nodeID}`}>
			<header><h2>节点详情</h2><strong>{nodeID}</strong></header>
			{node && <p>{node.type}@{node.typeVersion}</p>}
			{nodeRun && <p>状态：{nodeStatusLabel(nodeRun.status)}</p>}
			{nodeRun?.startedAt && <p>开始：{nodeRun.startedAt}</p>}
			{nodeRun?.endedAt && <p>结束：{nodeRun.endedAt}</p>}
			{duration !== undefined && <p>耗时：<strong>{duration.toFixed(3)} 秒</strong></p>}
			{redactedPaths.length > 0 && <p className="debug-warning">以下路径已脱敏，需要重新输入：{redactedPaths.join('、')}</p>}
			{started?.input !== undefined && <DebugValue title="输入" value={started.input} />}
			{terminal?.output !== undefined && <DebugValue title="输出" value={terminal.output} />}
			{terminal && <DebugValue title="激活端口" value={terminal.activePorts} />}
			{terminal?.error && <DebugValue title="公开错误" value={terminal.error} />}
			{onRerun && <button className="primary-button inline" type="button" onClick={() => onRerun(nodeID)}>从此节点重新运行</button>}
		</section>
	)
}

function nodeStatusLabel(status: string) {
	return { pending: '待执行', running: '运行中', completed: '已完成', failed: '失败', skipped: '已跳过', cancelled: '已取消' }[status] ?? status
}

function DebugValue({ title, value }: { title: string; value: unknown }) {
	return <div className="debug-value"><h3>{title}</h3><pre>{JSON.stringify(value, null, 2)}</pre></div>
}
