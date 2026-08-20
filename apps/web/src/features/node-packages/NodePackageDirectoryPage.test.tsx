import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api, type NodeIndexStatus, type NodePackageSearchResult } from '../../lib/api/client'
import { NodePackageDirectoryPage } from './NodePackageDirectoryPage'

const embeddedStatus: NodeIndexStatus = {
	source: 'embedded',
	release: 'v0.1.0',
	generatedAt: '2026-08-20T00:00:00Z',
	packageCount: 0,
	compatiblePackageCount: 0,
	runtimeVersion: '0.3.0-dev',
	nodeAPI: 'agent-studio.dev/v1alpha1',
	stale: true,
	warningCode: 'INDEX_EMBEDDED_SNAPSHOT',
}

const emptyResult: NodePackageSearchResult = {
	release: 'v0.1.0', total: 0, offset: 0, limit: 50, items: [],
}

describe('NodePackageDirectoryPage', () => {
	afterEach(() => vi.restoreAllMocks())

	it('显示加载状态', () => {
		vi.spyOn(api, 'getNodeIndexStatus').mockReturnValue(new Promise(() => {}))
		vi.spyOn(api, 'listNodePackages').mockReturnValue(new Promise(() => {}))
		renderPage()
		expect(screen.getByRole('status')).toHaveTextContent('正在加载节点包索引')
	})

	it('显示 embedded 提示且没有刷新或安装按钮', async () => {
		vi.spyOn(api, 'getNodeIndexStatus').mockResolvedValue(embeddedStatus)
		vi.spyOn(api, 'listNodePackages').mockResolvedValue(emptyResult)
		renderPage()

		expect(await screen.findByText('当前使用内置索引')).toBeInTheDocument()
		expect(screen.getByText('agent-studio node index refresh')).toBeInTheDocument()
		expect(screen.getByText('索引尚无包')).toBeInTheDocument()
		expect(screen.queryByRole('button', { name: /刷新|安装/ })).not.toBeInTheDocument()
	})

	it('显示 cache 警告且说明 Web 不会联网刷新', async () => {
		vi.spyOn(api, 'getNodeIndexStatus').mockResolvedValue({
			...embeddedStatus,
			source: 'cache',
			packageCount: 2,
			warningCode: 'INDEX_CONTENT_INVALID',
		})
		vi.spyOn(api, 'listNodePackages').mockResolvedValue(emptyResult)
		renderPage()

		expect(await screen.findByText('本地索引加载异常')).toBeInTheDocument()
		expect(screen.getByText(/Web 页面不会联网刷新/)).toBeInTheDocument()
	})

	it('显示安全的 API 错误状态', async () => {
		vi.spyOn(api, 'getNodeIndexStatus').mockRejectedValue(new Error('private /absolute/path'))
		vi.spyOn(api, 'listNodePackages').mockResolvedValue(emptyResult)
		renderPage()

		expect(await screen.findByRole('alert')).toHaveTextContent('节点包目录加载失败')
		expect(screen.queryByText(/private|absolute/)).not.toBeInTheDocument()
	})

	it('卸载时取消状态和列表请求', () => {
		let statusSignal: AbortSignal | undefined
		let listSignal: AbortSignal | undefined
		vi.spyOn(api, 'getNodeIndexStatus').mockImplementation((signal) => {
			statusSignal = signal
			return new Promise(() => {})
		})
		vi.spyOn(api, 'listNodePackages').mockImplementation((_query, signal) => {
			listSignal = signal
			return new Promise(() => {})
		})
		const view = renderPage()

		view.unmount()
		expect(statusSignal?.aborted).toBe(true)
		expect(listSignal).toBe(statusSignal)
	})
})

function renderPage() {
	return render(
		<MemoryRouter initialEntries={['/node-packages']}>
			<NodePackageDirectoryPage />
		</MemoryRouter>,
	)
}
