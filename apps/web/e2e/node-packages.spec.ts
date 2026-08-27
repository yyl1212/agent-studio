import { expect, test } from '@playwright/test'

test('只通过本地 API 搜索并查看节点包', async ({ page }) => {
	const remoteRequests: string[] = []
	page.on('request', (request) => {
		const hostname = new URL(request.url()).hostname
		if (/github\.com$|githubusercontent\.com$/.test(hostname)) remoteRequests.push(request.url())
	})

	await page.goto('/node-packages')
	await expect(page.getByRole('heading', { name: '节点包' })).toBeVisible()
	await expect(page.getByText('当前使用本地缓存索引')).toBeVisible()
	await expect(page.getByText('Release v0.4.0 · 2 个包 · 1 个兼容包')).toBeVisible()
	await page.getByRole('searchbox', { name: '搜索节点包' }).fill('search')
	await expect.poll(() => new URL(page.url()).searchParams.get('q')).toBe('search')
	await expect(page.getByRole('heading', { name: 'Example Search Nodes' })).toBeVisible()
	await page.getByRole('button', { name: '查看 Example Search Nodes 版本详情' }).click()
	await expect(page.getByRole('heading', { name: 'v1.2.3' })).toBeVisible()
	await expect(page.getByText('123456789abcdef0123456789abcdef012345678', { exact: true })).toBeVisible()

	await page.getByRole('searchbox', { name: '搜索节点包' }).fill('')
	await expect.poll(() => new URL(page.url()).searchParams.get('q')).toBeNull()
	const compatibleOnly = page.getByRole('checkbox', { name: '仅显示兼容包' })
	await compatibleOnly.click()
	await expect.poll(() => new URL(page.url()).searchParams.get('compatible')).toBe('false')
	await expect(compatibleOnly).not.toBeChecked()
	await expect(page.getByRole('heading', { name: 'Future Integration Nodes' })).toBeVisible()
	await expect(page.getByText('暂无兼容推荐')).toBeVisible()
	await expect(page.getByText('当前运行时版本过低')).toBeVisible()
	await page.getByRole('button', { name: '查看 Future Integration Nodes 版本详情' }).click()
	const futureDetails = page.getByRole('region', { name: 'Future Integration Nodes 版本详情' })
	await expect(futureDetails.getByText('不兼容：当前运行时版本过低')).toBeVisible()

	expect(remoteRequests).toEqual([])
	await expect(page.getByRole('button', { name: /刷新|安装/ })).toHaveCount(0)
})
