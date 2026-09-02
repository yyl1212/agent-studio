import { fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import {
	api,
	type IndexedNodePackageSummary,
	type NodePackageDetail,
} from '../../lib/api/client'
import { NodePackageCard } from './NodePackageCard'

describe('NodePackageCard', () => {
	afterEach(() => vi.restoreAllMocks())

	it('显示安全的摘要、推荐版本、兼容范围、许可证和外链', () => {
		const item = summaryFixture()
		item.description = '<img src=x onerror=alert(1)>'
		item.categories = ['model', 'search', 'retrieval', 'vector', 'custom-ai']
		const view = render(<NodePackageCard item={item} />)

		expect(screen.getByText('<img src=x onerror=alert(1)>')).toBeInTheDocument()
		expect(view.container.querySelector('img')).toBeNull()
		expect(screen.getByText('推荐 v1.2.3')).toBeInTheDocument()
		expect(screen.getByText('Apache-2.0')).toBeInTheDocument()
		expect(screen.getByText('运行时 v0.3.0 至 v0.4.0（不含）')).toBeInTheDocument()
		expect(screen.getByText('模型')).toBeInTheDocument()
		expect(screen.getByText('搜索')).toBeInTheDocument()
		expect(screen.getByText('检索')).toBeInTheDocument()
		expect(screen.getByText('向量')).toBeInTheDocument()
		expect(screen.getByText('custom-ai')).toBeInTheDocument()
		const link = screen.getByRole('link', { name: /github.com/ })
		expect(link).toHaveAttribute('href', 'https://github.com/example/nodes')
		expect(link).toHaveAttribute('target', '_blank')
		expect(link).toHaveAttribute('rel', expect.stringContaining('noopener'))
		expect(link).toHaveAttribute('rel', expect.stringContaining('noreferrer'))
	})

	it('无推荐版本时显示稳定原因', () => {
		const item = summaryFixture()
		item.recommendedVersion = null
		item.reasons = ['runtime_too_old']
		render(<NodePackageCard item={item} />)

		expect(screen.getByText('当前运行时版本过低')).toBeInTheDocument()
		expect(screen.queryByText(/推荐 v/)).not.toBeInTheDocument()
	})

	it('展开详情时显示每个版本的运行时兼容范围', async () => {
		vi.spyOn(api, 'getNodePackage').mockResolvedValue(detailFixture())
		render(<NodePackageCard item={summaryFixture()} />)

		fireEvent.click(screen.getByRole('button', { name: '查看 Example Nodes 版本详情' }))
		const details = await screen.findByRole('region', { name: 'Example Nodes 版本详情' })
		expect(within(details).getAllByText('运行时 v0.3.0 至 v0.4.0（不含）')).toHaveLength(3)
	})

	it('首次展开时加载详情，折叠后复用缓存', async () => {
		const pending = deferred<NodePackageDetail>()
		const get = vi.spyOn(api, 'getNodePackage').mockReturnValue(pending.promise)
		render(<NodePackageCard item={summaryFixture()} />)

		fireEvent.click(screen.getByRole('button', { name: '查看 Example Nodes 版本详情' }))
		expect(screen.getByText('正在加载版本详情…')).toBeInTheDocument()
		expect(get).toHaveBeenCalledWith('github.com/example/nodes', expect.any(AbortSignal))
		pending.resolve(detailFixture())

		const details = await screen.findByRole('region', { name: 'Example Nodes 版本详情' })
		expect(within(details).getByText('✓ 活跃（active）')).toBeInTheDocument()
		expect(within(details).getByText('⚠ 已弃用（deprecated）')).toBeInTheDocument()
		expect(within(details).getByText('⛔ 已撤回（withdrawn）')).toBeInTheDocument()
		expect(within(details).getAllByText('release/v1.2.3').length).toBeGreaterThan(0)
		expect(within(details).getAllByText('0123456789abcdef0123456789abcdef01234567').length).toBeGreaterThan(0)
		expect(within(details).getAllByText('sha256:89abcdef89abcdef89abcdef89abcdef89abcdef89abcdef89abcdef89abcdef').length).toBeGreaterThan(0)
		expect(within(details).getAllByText('example.search@1').length).toBeGreaterThan(0)
		expect(within(details).getAllByText(/元数据审核：已通过/).length).toBeGreaterThan(0)

		fireEvent.click(screen.getByRole('button', { name: '收起 Example Nodes 版本详情' }))
		fireEvent.click(screen.getByRole('button', { name: '查看 Example Nodes 版本详情' }))
		expect(get).toHaveBeenCalledTimes(1)
		expect(screen.getByRole('region', { name: 'Example Nodes 版本详情' })).toBeInTheDocument()
	})

	it('详情失败后允许显式重试', async () => {
		const get = vi.spyOn(api, 'getNodePackage')
			.mockRejectedValueOnce(new Error('private failure'))
			.mockResolvedValueOnce(detailFixture())
		render(<NodePackageCard item={summaryFixture()} />)

		fireEvent.click(screen.getByRole('button', { name: '查看 Example Nodes 版本详情' }))
		expect(await screen.findByRole('alert')).toHaveTextContent('版本详情加载失败')
		expect(screen.queryByText('private failure')).not.toBeInTheDocument()
		fireEvent.click(screen.getByRole('button', { name: '重试加载 Example Nodes 版本详情' }))
		expect(await screen.findByRole('region', { name: 'Example Nodes 版本详情' })).toBeInTheDocument()
		expect(get).toHaveBeenCalledTimes(2)
	})

	it('折叠不取消请求，仅在卸载时取消', () => {
		let signal: AbortSignal | undefined
		vi.spyOn(api, 'getNodePackage').mockImplementation((_name, nextSignal) => {
			signal = nextSignal
			return new Promise(() => {})
		})
		const view = render(<NodePackageCard item={summaryFixture()} />)

		fireEvent.click(screen.getByRole('button', { name: '查看 Example Nodes 版本详情' }))
		fireEvent.click(screen.getByRole('button', { name: '收起 Example Nodes 版本详情' }))
		expect(signal?.aborted).toBe(false)
		view.unmount()
		expect(signal?.aborted).toBe(true)
	})
})

function summaryFixture(): IndexedNodePackageSummary {
	return {
		name: 'github.com/example/nodes',
		displayName: 'Example Nodes',
		description: 'Example search integration nodes',
		license: 'Apache-2.0',
		repository: 'https://github.com/example/nodes',
		categories: ['integration'],
		keywords: ['search'],
		recommendedVersion: {
			version: 'v1.2.3',
			source: sourceFixture(),
			lifecycle: { status: 'active', message: '' },
			compatibility: { nodeAPI: 'agent-studio.dev/v1alpha1', runtime: { minVersion: 'v0.3.0', maxVersionExclusive: 'v0.4.0' } },
		},
		reasons: [],
	}
}

function detailFixture(): NodePackageDetail {
	return {
		name: 'github.com/example/nodes',
		categories: ['integration'],
		keywords: ['search'],
		versions: [
			versionFixture('v1.2.3', 'active', ''),
			versionFixture('v1.1.0', 'deprecated', '请迁移到 v1.2.3'),
			versionFixture('v1.0.0', 'withdrawn', '上游已撤回'),
		],
		recommendedVersion: summaryFixture().recommendedVersion,
		reasons: [],
		assessments: [
			{ version: 'v1.2.3', compatible: true, reasons: [] },
			{ version: 'v1.1.0', compatible: true, reasons: [] },
			{ version: 'v1.0.0', compatible: false, reasons: ['no_active_stable_version'] },
		],
	}
}

function versionFixture(version: string, status: 'active' | 'deprecated' | 'withdrawn', message: string): NodePackageDetail['versions'][number] {
	const lifecycle = status === 'active'
		? { status, message: '' as const }
		: { status, message }
	return {
		version,
		source: sourceFixture(),
		review: { status: 'approved', reviewedAt: '2026-08-20T07:30:00Z', indexCommit: '89abcdef0123456789abcdef0123456789abcdef' },
		lifecycle,
		manifest: {
			apiVersion: 'agent-studio.dev/v1alpha1',
			kind: 'NodePackage',
			metadata: {
				name: 'github.com/example/nodes', displayName: 'Example Nodes', description: 'Search nodes',
				license: 'Apache-2.0', repository: 'https://github.com/example/nodes',
			},
			compatibility: { nodeAPI: 'agent-studio.dev/v1alpha1', runtime: { minVersion: 'v0.3.0', maxVersionExclusive: 'v0.4.0' } },
			registrations: [{ package: 'github.com/example/nodes/search', nodes: [{ type: 'example.search', version: '1' }] }],
		},
	}
}

function sourceFixture() {
	return {
		repository: 'https://github.com/example/nodes',
		moduleDir: '.',
		tag: 'release/v1.2.3',
		commit: '0123456789abcdef0123456789abcdef01234567',
		manifestDigest: 'sha256:89abcdef89abcdef89abcdef89abcdef89abcdef89abcdef89abcdef89abcdef',
	}
}

function deferred<T>() {
	let resolve!: (value: T) => void
	const promise = new Promise<T>((next) => { resolve = next })
	return { promise, resolve }
}
