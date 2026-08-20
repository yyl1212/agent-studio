import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom'
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
	afterEach(() => {
		vi.useRealTimers()
		vi.restoreAllMocks()
	})

	it('显示加载状态', () => {
		vi.spyOn(api, 'getNodeIndexStatus').mockReturnValue(new Promise(() => {}))
		vi.spyOn(api, 'listNodePackages').mockReturnValue(new Promise(() => {}))
		renderPage()
		expect(screen.getByText('正在加载节点包索引…')).toBeInTheDocument()
	})

	it('显示 embedded 提示且没有刷新或安装按钮', async () => {
		vi.spyOn(api, 'getNodeIndexStatus').mockResolvedValue(embeddedStatus)
		vi.spyOn(api, 'listNodePackages').mockResolvedValue(emptyResult)
		renderPage()

		expect(await screen.findByText('当前使用内置索引')).toBeInTheDocument()
		expect(screen.getByText('查看已收录且元数据经过审核的节点包。')).toBeInTheDocument()
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
		expect(listSignal?.aborted).toBe(true)
		expect(listSignal).not.toBe(statusSignal)
	})

	it('从 URL 恢复查询、重复分类、兼容范围和分页', async () => {
		mockStatus()
		const list = vi.spyOn(api, 'listNodePackages').mockResolvedValue({ ...emptyResult, offset: 50 })
		renderPage('/node-packages?q=%E5%90%91%E9%87%8F&category=integration&category=file&compatible=false&offset=50')

		await waitFor(() => expect(list).toHaveBeenCalledWith({
			q: '向量', categories: ['integration', 'file'], compatible: false, limit: 50, offset: 50,
		}, expect.any(AbortSignal)))
		expect(screen.getByRole('searchbox', { name: '搜索节点包' })).toHaveValue('向量')
		expect(screen.getByRole('checkbox', { name: '集成' })).toBeChecked()
		expect(screen.getByRole('checkbox', { name: '文件' })).toBeChecked()
		expect(screen.getByRole('checkbox', { name: '仅显示兼容包' })).not.toBeChecked()
	})

	it('非法 URL 参数安全回退到默认值', async () => {
		mockStatus()
		const list = vi.spyOn(api, 'listNodePackages').mockResolvedValue(emptyResult)
		renderPage('/node-packages?compatible=maybe&offset=-4')

		await waitFor(() => expect(list).toHaveBeenCalledWith({
			q: '', categories: [], compatible: true, limit: 50, offset: 0,
		}, expect.any(AbortSignal)))
	})

	it('区分索引无包、当前版本无兼容包和筛选无结果', async () => {
		vi.spyOn(api, 'getNodeIndexStatus').mockResolvedValue({
			...embeddedStatus, packageCount: 2, compatiblePackageCount: 0,
		})
		vi.spyOn(api, 'listNodePackages').mockResolvedValue(emptyResult)
		const firstView = renderPage()
		expect(await screen.findByText('当前版本无兼容包')).toBeInTheDocument()
		firstView.unmount()

		vi.restoreAllMocks()
		vi.spyOn(api, 'getNodeIndexStatus').mockResolvedValue({
			...embeddedStatus, packageCount: 2, compatiblePackageCount: 1,
		})
		vi.spyOn(api, 'listNodePackages').mockResolvedValue(emptyResult)
		renderPage('/node-packages?q=missing')
		expect(await screen.findByText('没有符合条件的节点包')).toBeInTheDocument()
	})

	it('搜索输入防抖 250 ms 并以 replace 更新 URL', async () => {
		vi.useFakeTimers()
		mockStatus()
		const list = vi.spyOn(api, 'listNodePackages').mockResolvedValue(emptyResult)
		renderPage()
		await act(async () => Promise.resolve())
		expect(list).toHaveBeenCalledTimes(1)

		fireEvent.change(screen.getByRole('searchbox', { name: '搜索节点包' }), { target: { value: '向量 search' } })
		await act(async () => vi.advanceTimersByTime(249))
		expect(list).toHaveBeenCalledTimes(1)
		await act(async () => vi.advanceTimersByTime(1))
		await act(async () => Promise.resolve())

		expect(list).toHaveBeenLastCalledWith({
			q: '向量 search', categories: [], compatible: true, limit: 50, offset: 0,
		}, expect.any(AbortSignal))
		expect(screen.getByTestId('location')).toHaveTextContent('q=%E5%90%91%E9%87%8F+search')
	})

	it('分类使用 OR 重复参数，筛选变化重置 offset', async () => {
		mockStatus()
		const list = vi.spyOn(api, 'listNodePackages').mockResolvedValue({ ...emptyResult, offset: 50 })
		renderPage('/node-packages?offset=50')
		await screen.findByText('索引尚无包')

		fireEvent.click(screen.getByRole('checkbox', { name: '集成' }))
		fireEvent.click(screen.getByRole('checkbox', { name: '文件' }))
		await waitFor(() => expect(list).toHaveBeenLastCalledWith({
			q: '', categories: ['integration', 'file'], compatible: true, limit: 50, offset: 0,
		}, expect.any(AbortSignal)))
		const location = screen.getByTestId('location').textContent ?? ''
		expect(location).toContain('category=integration')
		expect(location).toContain('category=file')
		expect(location).not.toContain('offset=50')

		fireEvent.click(screen.getByRole('checkbox', { name: '仅显示兼容包' }))
		await waitFor(() => expect(list).toHaveBeenLastCalledWith(expect.objectContaining({ compatible: false, offset: 0 }), expect.any(AbortSignal)))
	})

	it('显示八个已知分类的中文标签并向 API 传递 slug', async () => {
		mockStatus()
		const list = vi.spyOn(api, 'listNodePackages').mockResolvedValue(emptyResult)
		renderPage()
		await screen.findByText('索引尚无包')

		for (const label of ['模型', '搜索', '检索', '向量', '文件', '集成', '数据', '工具']) {
			expect(screen.getByRole('checkbox', { name: label })).toBeInTheDocument()
		}
		fireEvent.click(screen.getByRole('checkbox', { name: '模型' }))
		fireEvent.click(screen.getByRole('checkbox', { name: '搜索' }))
		await waitFor(() => expect(list).toHaveBeenLastCalledWith(expect.objectContaining({ categories: ['model', 'search'] }), expect.any(AbortSignal)))
	})

	it('分页遵守前后边界', async () => {
		mockStatus()
		const firstPage = searchResult(120, 0, 50, 'first')
		const secondPage = searchResult(120, 50, 50, 'second')
		const list = vi.spyOn(api, 'listNodePackages').mockResolvedValueOnce(firstPage).mockResolvedValueOnce(secondPage)
		renderPage()
		await screen.findByText('first-0')

		expect(screen.getByRole('button', { name: '上一页' })).toBeDisabled()
		expect(screen.getByRole('button', { name: '下一页' })).toBeEnabled()
		fireEvent.click(screen.getByRole('button', { name: '下一页' }))
		await screen.findByText('second-0')
		expect(list).toHaveBeenLastCalledWith(expect.objectContaining({ offset: 50 }), expect.any(AbortSignal))
		expect(screen.getByRole('button', { name: '上一页' })).toBeEnabled()
	})

	it('浏览器返回时恢复先前筛选', async () => {
		mockStatus()
		const list = vi.spyOn(api, 'listNodePackages').mockResolvedValue(emptyResult)
		renderPage('/node-packages?category=data')
		await waitFor(() => expect(list).toHaveBeenCalledWith(expect.objectContaining({ categories: ['data'] }), expect.any(AbortSignal)))

		fireEvent.click(screen.getByRole('checkbox', { name: '集成' }))
		await waitFor(() => expect(list).toHaveBeenLastCalledWith(expect.objectContaining({ categories: ['data', 'integration'] }), expect.any(AbortSignal)))
		fireEvent.click(screen.getByRole('button', { name: '测试返回' }))
		await waitFor(() => expect(list).toHaveBeenLastCalledWith(expect.objectContaining({ categories: ['data'] }), expect.any(AbortSignal)))
		expect(screen.getByRole('checkbox', { name: '数据' })).toBeChecked()
		expect(screen.getByRole('checkbox', { name: '集成' })).not.toBeChecked()

		fireEvent.click(screen.getByRole('button', { name: '测试前进' }))
		await waitFor(() => expect(list).toHaveBeenLastCalledWith(expect.objectContaining({ categories: ['data', 'integration'] }), expect.any(AbortSignal)))
		expect(screen.getByRole('checkbox', { name: '集成' })).toBeChecked()
	})

	it('旧请求晚返回也不会覆盖新查询', async () => {
		vi.useFakeTimers()
		mockStatus()
		const old = deferred<NodePackageSearchResult>()
		const current = deferred<NodePackageSearchResult>()
		vi.spyOn(api, 'listNodePackages').mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
		renderPage()
		await act(async () => Promise.resolve())

		fireEvent.change(screen.getByRole('searchbox', { name: '搜索节点包' }), { target: { value: 'current' } })
		await act(async () => vi.advanceTimersByTime(250))
		await act(async () => Promise.resolve())
		expect(api.listNodePackages).toHaveBeenCalledTimes(2)
		await act(async () => {
			current.resolve(searchResult(1, 0, 50, 'current'))
			await Promise.resolve()
		})
		expect(screen.getByText('current-0')).toBeInTheDocument()
		await act(async () => {
			old.resolve(searchResult(1, 0, 50, 'old'))
			await Promise.resolve()
		})

		expect(screen.getByText('current-0')).toBeInTheDocument()
		expect(screen.queryByText('old-0')).not.toBeInTheDocument()
	})

	it('未知分类按原始 slug 展示', async () => {
		mockStatus()
		const result = searchResult(1, 0, 50, 'custom')
		result.items[0]!.categories = ['custom-ai']
		vi.spyOn(api, 'listNodePackages').mockResolvedValue(result)
		renderPage()

		const results = await screen.findByRole('region', { name: '节点包搜索结果' })
		expect(within(results).getByText('custom-ai')).toBeInTheDocument()
	})
})

function renderPage(initialEntry = '/node-packages') {
	return render(
		<MemoryRouter initialEntries={[initialEntry]}>
			<NodePackageDirectoryPage />
			<LocationProbe />
			<HistoryControls />
		</MemoryRouter>,
	)
}

function LocationProbe() {
	const location = useLocation()
	return <output data-testid="location">{location.search}</output>
}

function HistoryControls() {
	const navigate = useNavigate()
	return <><button type="button" onClick={() => navigate(-1)}>测试返回</button><button type="button" onClick={() => navigate(1)}>测试前进</button></>
}

function mockStatus() {
	vi.spyOn(api, 'getNodeIndexStatus').mockResolvedValue(embeddedStatus)
}

function searchResult(total: number, offset: number, count: number, prefix: string): NodePackageSearchResult {
	return {
		release: 'v0.1.0', total, offset, limit: 50,
		items: Array.from({ length: count }, (_, index) => ({
			name: `github.com/example/${prefix}-${index}`,
			displayName: `${prefix}-${index}`,
			description: '测试节点包',
			license: 'Apache-2.0',
			repository: `https://github.com/example/${prefix}-${index}`,
			categories: ['integration'],
			keywords: [],
			recommendedVersion: null,
			reasons: [],
		})),
	}
}

function deferred<T>() {
	let resolve!: (value: T) => void
	const promise = new Promise<T>((next) => { resolve = next })
	return { promise, resolve }
}
