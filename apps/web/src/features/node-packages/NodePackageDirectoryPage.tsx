import { useEffect, useMemo, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'

import {
	api,
	type NodeIndexStatus,
	type NodePackageQuery,
	type NodePackageSearchResult,
} from '../../lib/api/client'
import './nodePackages.css'
import { NodePackageCard } from './NodePackageCard'
import { nodePackageCategories } from './categories'

const pageSize = 50

export function NodePackageDirectoryPage() {
	const [searchParams, setSearchParams] = useSearchParams()
	const query = useMemo(() => parseQuery(searchParams), [searchParams])
	const categoryKey = query.categories.join('\u0000')
	const [inputQuery, setInputQuery] = useState(query.q)
	const [status, setStatus] = useState<NodeIndexStatus | null>(null)
	const [statusFailed, setStatusFailed] = useState(false)
	const [result, setResult] = useState<NodePackageSearchResult | null>(null)
	const [listFailed, setListFailed] = useState(false)
	const requestSequence = useRef(0)

	useEffect(() => {
		const controller = new AbortController()
		api.getNodeIndexStatus(controller.signal).then(setStatus).catch((error: unknown) => {
			if (!controller.signal.aborted && !isAbortError(error)) setStatusFailed(true)
		})
		return () => controller.abort()
	}, [])

	useEffect(() => {
		const controller = new AbortController()
		const sequence = ++requestSequence.current
		setResult(null)
		setListFailed(false)
		api.listNodePackages(query, controller.signal).then((nextResult) => {
			if (sequence === requestSequence.current) setResult(nextResult)
		}).catch((error: unknown) => {
			if (sequence === requestSequence.current && !controller.signal.aborted && !isAbortError(error)) setListFailed(true)
		})
		return () => controller.abort()
		// categoryKey 是重复 category 查询值的稳定标识。
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [query.q, categoryKey, query.compatible, query.offset])

	useEffect(() => setInputQuery(query.q), [query.q])

	useEffect(() => {
		if (inputQuery === query.q) return
		const timeout = window.setTimeout(() => {
			const next = new URLSearchParams(searchParams)
			if (inputQuery === '') next.delete('q')
			else next.set('q', inputQuery)
			next.delete('offset')
			setSearchParams(next, { replace: true })
		}, 250)
		return () => window.clearTimeout(timeout)
	}, [inputQuery, query.q, searchParams, setSearchParams])

	const updateCategories = (slug: string, selected: boolean) => {
		const nextCategories = selected
			? [...query.categories, slug]
			: query.categories.filter((category) => category !== slug)
		const next = new URLSearchParams(searchParams)
		next.delete('category')
		for (const category of nextCategories) next.append('category', category)
		next.delete('offset')
		setSearchParams(next)
	}

	const updateCompatibility = (compatible: boolean) => {
		const next = new URLSearchParams(searchParams)
		next.set('compatible', String(compatible))
		next.delete('offset')
		setSearchParams(next)
	}

	const updateOffset = (offset: number) => {
		const next = new URLSearchParams(searchParams)
		if (offset === 0) next.delete('offset')
		else next.set('offset', String(offset))
		setSearchParams(next)
	}

	return (
		<main className="page-container node-package-directory">
			<header className="node-package-heading">
				<p className="eyebrow">NODE PACKAGES</p>
				<h2>节点包</h2>
				<p>查看已收录且元数据经过审核的节点包。</p>
			</header>

			{statusFailed ? <p className="node-package-error" role="alert">节点包目录加载失败，请稍后重试。</p> : null}
			{!statusFailed && status === null ? <p className="node-index-banner" role="status">正在加载节点包索引…</p> : null}
			{status ? <NodeIndexBanner status={status} /> : null}

			{status ? (
				<>
					<form className="node-package-search" role="search" onSubmit={(event) => event.preventDefault()}>
						<label htmlFor="node-package-query">搜索节点包</label>
						<input
							id="node-package-query"
							name="q"
							type="search"
							maxLength={128}
							placeholder="名称、描述或节点类型"
							value={inputQuery}
							onChange={(event) => setInputQuery(event.target.value)}
						/>
						<fieldset>
							<legend>分类</legend>
							{nodePackageCategories.map(({ slug, label }) => (
								<label key={slug}>
									<input
										type="checkbox"
										name="category"
										value={slug}
										checked={query.categories.includes(slug)}
										onChange={(event) => updateCategories(slug, event.target.checked)}
									/>
									{label}
								</label>
							))}
						</fieldset>
						<label className="node-package-compatible">
							<input type="checkbox" checked={query.compatible} onChange={(event) => updateCompatibility(event.target.checked)} />
							仅显示兼容包
						</label>
					</form>
					<NodePackageResults
						status={status}
						query={query}
						result={result}
						failed={listFailed}
						onPrevious={() => updateOffset(Math.max(0, query.offset - pageSize))}
						onNext={() => updateOffset(query.offset + pageSize)}
					/>
				</>
			) : null}
		</main>
	)
}

function NodePackageResults({
	status, query, result, failed, onPrevious, onNext,
}: {
	status: NodeIndexStatus
	query: NodePackageQuery
	result: NodePackageSearchResult | null
	failed: boolean
	onPrevious: () => void
	onNext: () => void
}) {
	return (
		<section className="node-package-results" aria-live="polite" aria-label="节点包搜索结果">
			{failed ? <p className="node-package-error" role="alert">节点包搜索失败，请稍后重试。</p> : null}
			{!failed && result === null ? <div className="state-card">正在加载节点包…</div> : null}
			{result?.total === 0 ? (
				<div className="state-card">
					<strong>{emptyState(status, query).title}</strong>
					<p>{emptyState(status, query).description}</p>
				</div>
			) : null}
			<div className="node-package-list">
				{result?.items.map((item) => <NodePackageCard item={item} key={item.name} />)}
			</div>
			{result ? (
				<nav className="node-package-pagination" aria-label="节点包分页">
					<button type="button" onClick={onPrevious} disabled={result.offset === 0}>上一页</button>
					<span>第 {Math.floor(result.offset / result.limit) + 1} 页 · 共 {result.total} 个包</span>
					<button type="button" onClick={onNext} disabled={result.offset + result.items.length >= result.total}>下一页</button>
				</nav>
			) : null}
		</section>
	)
}

function emptyState(status: NodeIndexStatus, query: NodePackageQuery) {
	if (status.packageCount === 0) {
		return { title: '索引尚无包', description: '可使用 CLI 更新本地索引后再查看。' }
	}
	if (query.compatible && query.q === '' && query.categories.length === 0 && status.compatiblePackageCount === 0) {
		return { title: '当前版本无兼容包', description: '可关闭“仅显示兼容包”查看已收录的其他节点包。' }
	}
	return { title: '没有符合条件的节点包', description: '请调整搜索词或筛选条件。' }
}

function NodeIndexBanner({ status }: { status: NodeIndexStatus }) {
	const hasWarning = status.warningCode === 'INDEX_CONTENT_INVALID'
	const embedded = status.source === 'embedded'
	const title = hasWarning ? '本地索引加载异常' : embedded ? '当前使用内置索引' : '当前使用本地缓存索引'
	return (
		<section className={`node-index-banner${hasWarning ? ' node-index-banner-warning' : ''}`} role="status">
			<strong>{title}</strong>
			<p>Release {status.release} · {status.packageCount} 个包 · {status.compatiblePackageCount} 个兼容包</p>
			{embedded || hasWarning ? (
				<p>Web 页面不会联网刷新。请在终端运行 <code>agent-studio node index refresh</code> 更新本地索引。</p>
			) : null}
		</section>
	)
}

function parseQuery(searchParams: URLSearchParams): NodePackageQuery {
	const compatibleValue = searchParams.get('compatible')
	const offsetValue = searchParams.get('offset')
	const parsedOffset = offsetValue !== null && /^\d+$/.test(offsetValue) ? Number(offsetValue) : 0
	return {
		q: searchParams.get('q') ?? '',
		categories: searchParams.getAll('category'),
		compatible: compatibleValue !== 'false',
		limit: pageSize,
		offset: Number.isSafeInteger(parsedOffset) && parsedOffset <= 10000 ? parsedOffset : 0,
	}
}

function isAbortError(error: unknown) {
	return error instanceof DOMException && error.name === 'AbortError'
}
