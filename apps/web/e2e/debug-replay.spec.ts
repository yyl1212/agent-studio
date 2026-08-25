import { expect, test } from '@playwright/test'

import { configureStartTextField, connectIndexedPorts, createWorkflow, moveIndexedNode } from './helpers'

test('fork/join 完整运行、回放与冻结外部分支局部重跑', async ({ page }) => {
	const suffix = Date.now().toString(36)
	await createWorkflow(page, `debug-replay-${suffix}`, `调试回放 ${suffix}`)
	await configureStartTextField(page, 'topic', '主题')

	for (const template of ['L-{{topic}}', 'R-{{topic}}', '{{left}}+{{right}}']) {
		await page.getByRole('button', { name: '添加节点' }).click()
		await page.getByRole('button', { name: '提示词模板' }).click()
		await page.getByLabel('模板').fill(template)
		await page.getByRole('button', { name: '关闭节点配置' }).click()
	}

	const templates = page.getByTestId('node-template')
	const leftID = await templates.nth(0).getAttribute('data-node-id')
	const rightID = await templates.nth(1).getAttribute('data-node-id')
	const joinID = await templates.nth(2).getAttribute('data-node-id')
	if (!leftID || !rightID || !joinID) throw new Error('模板节点缺少稳定实例 ID')
	await page.getByRole('button', { name: 'Fit View' }).click()
	await moveIndexedNode(page, 'template', 0, 0.42, 0.25)
	await moveIndexedNode(page, 'template', 1, 0.42, 0.72)
	await moveIndexedNode(page, 'template', 2, 0.64, 0.48)
	await moveIndexedNode(page, 'end', 0, 0.84, 0.48)

	await connectIndexedPorts(page, [
		['start', 0, 'topic', 'template', 0, 'topic'],
		['start', 0, 'topic', 'template', 1, 'topic'],
		['template', 0, 'text', 'template', 2, 'left'],
		['template', 1, 'text', 'template', 2, 'right'],
		['template', 2, 'text', 'end', 0, 'result'],
	])

	await page.getByRole('button', { name: '测试运行' }).click()
	await page.getByLabel('主题').fill('A')
	await page.getByRole('button', { name: '运行', exact: true }).click()
	await expect(page.locator('.run-output')).toContainText('L-A+R-A')
	await page.getByRole('button', { name: '关闭测试运行' }).click()
	await page.getByRole('link', { name: '运行记录' }).click()
	await page.getByRole('link', { name: '调试回放' }).first().click()
	await expect(page.getByText('只读回放')).toBeVisible()

	const originalURL = page.url()
	const workflowID = originalURL.match(/\/workflows\/([^/]+)/)?.[1]
	const originalRunID = originalURL.match(/\/runs\/([^/]+)\/debug$/)?.[1]
	if (!workflowID || !originalRunID) throw new Error(`无法从 URL 读取工作流或源运行 ID: ${originalURL}`)
	const timelineButtons = page.locator('.run-timeline li button')
	await expect(timelineButtons.first()).toBeVisible()
	const labels = await timelineButtons.evaluateAll((buttons) => buttons.map((button) => button.getAttribute('aria-label') ?? ''))
	expect(labels.map((label) => Number(label.match(/^#(\d+)/)?.[1]))).toEqual(labels.map((_, index) => index + 1))

	await page.getByRole('button', { name: new RegExp(`node.completed ${escapeRegExp(joinID)}$`) }).click()
	await expect(page.getByRole('region', { name: `节点详情 ${joinID}` })).toContainText('L-A+R-A')
	await page.getByRole('button', { name: new RegExp(`node.completed ${escapeRegExp(leftID)}$`) }).click()
	await page.getByRole('button', { name: '从此节点重新运行' }).click()
	await expect(page.getByText(new RegExp(`${escapeRegExp(rightID)}\\.text → ${escapeRegExp(joinID)}\\.right.*历史冻结`))).toBeVisible()
	await page.getByLabel('入口输入 JSON').fill('{"topic":["B"]}')
	await page.getByRole('button', { name: '开始局部重跑' }).click()
	await expect(page).toHaveURL(new RegExp(`/workflows/[^/]+/runs/[^/]+/debug$`))
	await expect(page).not.toHaveURL(originalURL)

	await expect(page.getByRole('link', { name: `来源运行 ${originalRunID}` })).toHaveAttribute('href', `/workflows/${workflowID}/runs/${originalRunID}/debug`)
	await page.getByRole('button', { name: '关闭局部重跑' }).click()
	await page.getByRole('button', { name: new RegExp(`node.completed ${escapeRegExp(joinID)}$`) }).click()
	await expect(page.getByRole('region', { name: `节点详情 ${joinID}` })).toContainText('L-B+R-A')
	await expect(page.getByRole('button', { name: new RegExp(`node.started ${escapeRegExp(rightID)}$`) })).toHaveCount(0)
})

function escapeRegExp(value: string) {
	return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}
