import { useEffect, useState } from 'react'

import {
	api,
	type NodeIndexStatus,
	type NodePackageSearchResult,
} from '../../lib/api/client'
import './nodePackages.css'

const initialQuery = { q: '', categories: [], compatible: true, limit: 50, offset: 0 }

export function NodePackageDirectoryPage() {
	const [status, setStatus] = useState<NodeIndexStatus | null>(null)
	const [result, setResult] = useState<NodePackageSearchResult | null>(null)
	const [failed, setFailed] = useState(false)

	useEffect(() => {
		const controller = new AbortController()
		Promise.all([
			api.getNodeIndexStatus(controller.signal),
			api.listNodePackages(initialQuery, controller.signal),
		]).then(([nextStatus, nextResult]) => {
			setStatus(nextStatus)
			setResult(nextResult)
		}).catch((error: unknown) => {
			if (!controller.signal.aborted && !isAbortError(error)) setFailed(true)
		})
		return () => controller.abort()
	}, [])

	return (
		<main className="page-container node-package-directory">
			<header className="node-package-heading">
				<p className="eyebrow">NODE PACKAGES</p>
				<h2>节点包</h2>
				<p>查看当前 Agent Studio 本地索引中经过审核的节点扩展。</p>
			</header>

			{failed ? <p className="node-package-error" role="alert">节点包目录加载失败，请稍后重试。</p> : null}
			{!failed && status === null ? <p className="node-index-banner" role="status">正在加载节点包索引…</p> : null}
			{status ? <NodeIndexBanner status={status} /> : null}

			{!failed && status && result ? (
				<>
					<form className="node-package-search" role="search">
						<label htmlFor="node-package-query">搜索节点包</label>
						<input id="node-package-query" name="q" type="search" placeholder="名称、描述或节点类型" />
						<fieldset>
							<legend>分类</legend>
							<label><input type="checkbox" name="category" value="integration" />集成</label>
							<label><input type="checkbox" name="category" value="data" />数据</label>
							<label><input type="checkbox" name="category" value="file" />文件</label>
							<label><input type="checkbox" name="category" value="utility" />工具</label>
						</fieldset>
						<label className="node-package-compatible"><input type="checkbox" defaultChecked />仅显示兼容包</label>
					</form>
					<section className="node-package-results" aria-live="polite" aria-label="节点包搜索结果">
						{result.total === 0 ? (
							<div className="state-card"><strong>索引尚无包</strong><p>可使用 CLI 更新本地索引后再查看。</p></div>
						) : result.items.map((item) => <article className="node-package-card" key={item.name}>{item.displayName}</article>)}
					</section>
				</>
			) : null}
		</main>
	)
}

function NodeIndexBanner({ status }: { status: NodeIndexStatus }) {
	const hasWarning = status.warningCode === 'INDEX_CONTENT_INVALID'
	const embedded = status.source === 'embedded'
	const title = hasWarning ? '本地索引加载异常' : embedded ? '当前使用内置索引' : '当前使用本地缓存索引'
	return (
		<section className={`node-index-banner${hasWarning ? ' node-index-banner-warning' : ''}`} role="status">
			<strong>{title}</strong>
			<p>
				Release {status.release} · {status.packageCount} 个包 · {status.compatiblePackageCount} 个兼容包
			</p>
			{embedded || hasWarning ? (
				<p>Web 页面不会联网刷新。请在终端运行 <code>agent-studio node index refresh</code> 更新本地索引。</p>
			) : null}
		</section>
	)
}

function isAbortError(error: unknown) {
	return error instanceof DOMException && error.name === 'AbortError'
}
