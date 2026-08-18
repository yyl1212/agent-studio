import { expect, test } from '@playwright/test'

import { configureStartTextField, connectPorts, createWorkflow } from './helpers'

test('扩展 Echo 无需前端专用组件即可运行', async ({ page }) => {
  const workflowURL = await createWorkflow(page, `sdk-echo-${Date.now().toString(36)}`)
  await page.goto(workflowURL)
  await configureStartTextField(page, 'topic', '主题')
  await page.getByRole('button', { name: '添加节点' }).click()
  await page.getByRole('button', { name: 'Echo' }).click()
  await page.getByLabel('前缀').fill('回答：')
  await page.getByRole('button', { name: '关闭节点配置' }).click()
  await connectPorts(page, [
    ['start', 'topic', 'extension.echo', 'text'],
    ['extension.echo', 'text', 'end', 'result'],
  ])
  await page.getByRole('button', { name: '测试运行' }).click()
  await page.getByLabel('主题').fill('SDK')
  await page.getByRole('button', { name: '运行', exact: true }).click()
  await expect(page.getByText('回答：SDK')).toBeVisible()
})
