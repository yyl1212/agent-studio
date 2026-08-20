import { useEffect, useRef, useState } from 'react'

import {
	api,
	type IndexedNodePackageSummary,
	type NodePackageDetail,
} from '../../lib/api/client'
import { nodePackageCategoryLabel } from './categories'

const reasonLabels: Record<string, string> = {
	runtime_invalid: '当前运行时版本无效',
	node_api_mismatch: '节点 API 不匹配',
	runtime_too_old: '当前运行时版本过低',
	runtime_too_new: '当前运行时版本过高',
	no_active_stable_version: '没有可用的稳定版本',
}

const lifecycleLabels = {
	active: { text: '✓ 活跃（active）', className: 'node-package-lifecycle-active' },
	deprecated: { text: '⚠ 已弃用（deprecated）', className: 'node-package-lifecycle-deprecated' },
	withdrawn: { text: '⛔ 已撤回（withdrawn）', className: 'node-package-lifecycle-withdrawn' },
} as const

export function NodePackageCard({ item }: { item: IndexedNodePackageSummary }) {
	const [expanded, setExpanded] = useState(false)
	const [detail, setDetail] = useState<NodePackageDetail | null>(null)
	const [loading, setLoading] = useState(false)
	const [failed, setFailed] = useState(false)
	const controller = useRef<AbortController | null>(null)
	const requestSequence = useRef(0)

	useEffect(() => () => {
		requestSequence.current++
		controller.current?.abort()
	}, [])

	const loadDetail = () => {
		const nextController = new AbortController()
		const sequence = ++requestSequence.current
		controller.current = nextController
		setLoading(true)
		setFailed(false)
		api.getNodePackage(item.name, nextController.signal).then((nextDetail) => {
			if (sequence === requestSequence.current) setDetail(nextDetail)
		}).catch((error: unknown) => {
			if (sequence === requestSequence.current && !nextController.signal.aborted && !isAbortError(error)) setFailed(true)
		}).finally(() => {
			if (sequence === requestSequence.current) setLoading(false)
		})
	}

	const toggleExpanded = () => {
		const nextExpanded = !expanded
		setExpanded(nextExpanded)
		if (nextExpanded && detail === null && !loading && !failed) loadDetail()
	}

	const recommendation = item.recommendedVersion
	return (
		<article className="node-package-card">
			<div className="node-package-card-heading">
				<div>
					<h3>{item.displayName}</h3>
					<code className="node-package-module">{item.name}</code>
				</div>
				{recommendation ? <span className="node-package-recommendation">推荐 {recommendation.version}</span> : <span className="node-package-unavailable">暂无兼容推荐</span>}
			</div>
			<p>{item.description}</p>
			<dl className="node-package-summary-metadata">
				<div><dt>许可证</dt><dd>{item.license}</dd></div>
				<div><dt>仓库</dt><dd><ExternalRepositoryLink repository={item.repository} /></dd></div>
			</dl>
			{recommendation ? (
				<p className="node-package-compatibility">
					运行时 {recommendation.compatibility.runtime.minVersion} 至 {recommendation.compatibility.runtime.maxVersionExclusive}（不含）
				</p>
			) : (
				<ul className="node-package-reasons">
					{item.reasons.map((reason) => <li key={reason}>{reasonLabels[reason] ?? reason}</li>)}
				</ul>
			)}
			<ul className="node-package-categories" aria-label="分类">
				{item.categories.map((category) => <li key={category}>{nodePackageCategoryLabel(category)}</li>)}
			</ul>
			<button className="node-package-detail-toggle" type="button" onClick={toggleExpanded} aria-expanded={expanded}>
				{expanded ? `收起 ${item.displayName} 版本详情` : `查看 ${item.displayName} 版本详情`}
			</button>

			{expanded && loading ? <p role="status">正在加载版本详情…</p> : null}
			{expanded && failed ? (
				<div className="node-package-detail-error">
					<p role="alert">版本详情加载失败，请稍后重试。</p>
					<button type="button" onClick={loadDetail}>重试加载 {item.displayName} 版本详情</button>
				</div>
			) : null}
			{expanded && detail ? <NodePackageVersions displayName={item.displayName} detail={detail} /> : null}
		</article>
	)
}

function NodePackageVersions({ displayName, detail }: { displayName: string; detail: NodePackageDetail }) {
	const assessments = new Map(detail.assessments.map((assessment) => [assessment.version, assessment]))
	return (
		<section className="node-package-version-list" aria-label={`${displayName} 版本详情`}>
			{detail.versions.map((version) => {
				const lifecycle = lifecycleLabels[version.lifecycle.status]
				const assessment = assessments.get(version.version)
				return (
					<article className="node-package-version" key={version.version}>
						<header>
							<h4>{version.version}</h4>
							<span className={`node-package-lifecycle ${lifecycle.className}`}>{lifecycle.text}</span>
						</header>
						{version.lifecycle.message ? <p>{version.lifecycle.message}</p> : null}
						<p className="node-package-version-compatibility">
							{assessment?.compatible ? '✓ 兼容当前运行时' : `不兼容：${(assessment?.reasons ?? []).map((reason) => reasonLabels[reason] ?? reason).join('、')}`}
						</p>
						<dl className="node-package-version-metadata">
							<div><dt>来源仓库</dt><dd><ExternalRepositoryLink repository={version.source.repository} /></dd></div>
							<div><dt>Tag</dt><dd><code>{version.source.tag}</code></dd></div>
							<div><dt>模块目录</dt><dd><code>{version.source.moduleDir}</code></dd></div>
							<div><dt>源码 Commit</dt><dd><code>{version.source.commit}</code></dd></div>
							<div><dt>Manifest 摘要</dt><dd><code>{version.source.manifestDigest}</code></dd></div>
							<div><dt>审核范围</dt><dd>元数据审核：已通过 · {formatDate(version.review.reviewedAt)}</dd></div>
							<div><dt>索引 Commit</dt><dd><code>{version.review.indexCommit}</code></dd></div>
						</dl>
						<div className="node-package-node-types">
							<strong>节点类型</strong>
							<ul>
								{version.manifest.registrations.flatMap((registration) => registration.nodes).map((node) => (
									<li key={`${node.type}@${node.version}`}><code>{node.type}@{node.version}</code></li>
								))}
							</ul>
						</div>
					</article>
				)
			})}
		</section>
	)
}

function ExternalRepositoryLink({ repository }: { repository: string }) {
	return <a href={repository} target="_blank" rel="noopener noreferrer">{repositoryHost(repository)} ↗</a>
}

function repositoryHost(repository: string) {
	try {
		return new URL(repository).host
	} catch {
		return repository
	}
}

function formatDate(value: string) {
	return new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short', timeZone: 'UTC' }).format(new Date(value))
}

function isAbortError(error: unknown) {
	return error instanceof DOMException && error.name === 'AbortError'
}
