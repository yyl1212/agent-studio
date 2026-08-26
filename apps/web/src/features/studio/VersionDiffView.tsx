import { useState } from 'react'

import type { WorkflowDiff } from '../../lib/api/client'

type GroupKey = keyof WorkflowDiff['groups']

const groupDefinitions: Array<{ key: GroupKey; label: string }> = [
	{ key: 'nodes', label: '节点' },
	{ key: 'startParameters', label: '开始参数' },
	{ key: 'connections', label: '连线' },
	{ key: 'agentPresentation', label: 'Agent 页面' },
	{ key: 'layout', label: '画布布局' },
]

const kindLabels = {
	added: '新增',
	removed: '删除',
	modified: '修改',
	reordered: '调整顺序',
} as const

const presentationLabels: Record<string, string> = {
	title: '页面标题',
	description: '页面描述',
	accent: '主题色',
	submitLabel: '提交按钮文案',
	resultMode: '结果展示方式',
}

const omissionLabels = {
	secret: '值已变化，内容不可查看',
	definition_unavailable: '节点定义不可用，仅展示变化摘要',
	too_large: '值过大，已省略前后内容',
} as const

type ValueDiff = WorkflowDiff['groups']['nodes'][number]['config'][number]

function formatValue(value: unknown) {
	if (typeof value === 'string') return value
	if (value === undefined) return '未设置'
	return JSON.stringify(value, null, 2)
}

function ValueChange({ change }: { change: ValueDiff }) {
	if (change.valueOmitted) return <span className="version-diff-omitted">{omissionLabels[change.valueOmitted]}</span>
	const hasBefore = Object.prototype.hasOwnProperty.call(change, 'before')
	const hasAfter = Object.prototype.hasOwnProperty.call(change, 'after')
	return (
		<span className="version-diff-values">
			<pre>{hasBefore ? formatValue(change.before) : '未设置'}</pre>
			<span aria-hidden="true">→</span>
			<pre>{hasAfter ? formatValue(change.after) : '未设置'}</pre>
		</span>
	)
}

function GroupContents({ group, diff }: { group: GroupKey; diff: WorkflowDiff }) {
	switch (group) {
		case 'nodes':
			return <ul>{diff.groups.nodes.map((node) => <li className="version-diff-entry" key={`${node.nodeId}-${node.kind}`}><strong>{node.title}</strong> · {kindLabels[node.kind]}{node.beforeType || node.afterType ? <p>{node.beforeType ? `${node.beforeType.title} ${node.beforeType.version}` : '未设置'} → {node.afterType ? `${node.afterType.title} ${node.afterType.version}` : '未设置'}</p> : null}{node.config.length > 0 && <ul>{node.config.map((change, index) => <li className="version-diff-entry" key={`${change.path}-${index}`}><code>{change.path}</code>：<ValueChange change={change} /></li>)}</ul>}</li>)}</ul>
		case 'startParameters':
			return <ul>{diff.groups.startParameters.map((parameter) => <li key={`${parameter.key}-${parameter.kind}`}><strong>{parameter.key}</strong>{parameter.kind === 'reordered' ? `：顺序 ${parameter.beforeOrder ?? '未设置'} → ${parameter.afterOrder ?? '未设置'}` : ` · ${kindLabels[parameter.kind]}`}{parameter.changes.length > 0 && <ul>{parameter.changes.map((change, index) => <li key={`${change.path}-${index}`}><code>{change.path}</code>：<ValueChange change={change} /></li>)}</ul>}</li>)}</ul>
		case 'connections':
			return <ul>{diff.groups.connections.map(({ connection, kind }, index) => <li key={`${connection.source}-${connection.sourcePort}-${connection.target}-${connection.targetPort}-${index}`}><span>{connection.source}.{connection.sourcePort} → {connection.target}.{connection.targetPort}</span> · {kindLabels[kind]}</li>)}</ul>
		case 'agentPresentation':
			return <ul>{diff.groups.agentPresentation.map(({ field, change }) => <li key={field}><strong>{presentationLabels[field] ?? field}</strong>：<ValueChange change={change} /></li>)}</ul>
		case 'layout':
			return <ul>{diff.groups.layout.map((item) => <li key={item.nodeId}>{item.title}：({item.before.x}, {item.before.y}) → ({item.after.x}, {item.after.y})</li>)}</ul>
	}
}

export function VersionDiffView({ diff }: { diff: WorkflowDiff }) {
	const defaultGroup = groupDefinitions.find(({ key }) => diff.summary[key] > 0)?.key
	const [expanded, setExpanded] = useState<Set<GroupKey>>(() => new Set(defaultGroup ? [defaultGroup] : []))
	const toggle = (key: GroupKey) => setExpanded((current) => {
		const next = new Set(current)
		if (next.has(key)) next.delete(key)
		else next.add(key)
		return next
	})

	return (
		<div className="version-diff-view" aria-label="版本差异">
			{diff.summary.total === 0 && <p className="empty-state">两个快照没有差异</p>}
			{diff.truncated && <p className="version-diff-truncated" role="status">仅展示前 500 项详细差异</p>}
			{groupDefinitions.map(({ key, label }) => {
				const open = expanded.has(key)
				const panelID = `version-diff-${key}`
				return (
					<section className="version-diff-group" key={key}>
						<button type="button" aria-expanded={open} aria-controls={panelID} onClick={() => toggle(key)}>{label} · {diff.summary[key]} 项变化</button>
						<div id={panelID} hidden={!open}>{open && (diff.groups[key].length > 0 ? <GroupContents group={key} diff={diff} /> : <p>无变化</p>)}</div>
					</section>
				)
			})}
		</div>
	)
}
