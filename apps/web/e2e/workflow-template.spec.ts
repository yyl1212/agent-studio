import { expect, test } from '@playwright/test'

import { configureStartTextField, connectPorts, createWorkflow } from './helpers'

test('导出草稿模板并导入为未发布新工作流', async ({ page }) => {
  const suffix = Date.now().toString(36)
  await createWorkflow(page, `template-source-${suffix}`, `模板源 ${suffix}`)
  await configureStartTextField(page, 'topic', '主题')
  await page.getByRole('button', { name: '添加节点' }).click()
  await page.getByRole('button', { name: /^Echo/ }).click()
  await page.getByLabel('前缀').fill('回答：')
  await page.getByRole('button', { name: '关闭节点配置' }).click()
  await connectPorts(page, [
    ['start', 'topic', 'extension.echo', 'text'],
    ['extension.echo', 'text', 'end', 'result'],
  ])

  const downloadPromise = page.waitForEvent('download')
  await page.getByRole('button', { name: '导出模板' }).click()
  const download = await downloadPromise
  expect(download.suggestedFilename()).toBe(`template-source-${suffix}.workflow.json`)
  const path = await download.path()
  if (!path) throw new Error('模板下载没有本地路径')

  await page.goto('/workflows')
  await page.getByRole('button', { name: '导入模板' }).click()
  await page.getByLabel('选择模板文件').setInputFiles(path)
  await expect(page.getByText('3 个节点 · 2 条连线')).toBeVisible()
  await expect(page.getByText('topic · 主题 · 必填')).toBeVisible()
  await page.getByLabel('名称').fill(`模板副本 ${suffix}`)
  await page.getByLabel('Agent 地址标识').fill(`template-copy-${suffix}`)
  await page.getByRole('button', { name: '导入并打开' }).click()
  await expect(page.getByText(`模板副本 ${suffix}`)).toBeVisible()
  await page.getByTestId('node-start').click()
  await expect(page.getByLabel('字段标识')).toHaveValue('topic')

  await page.getByRole('link', { name: '返回工作流列表' }).click()
  const card = page.getByRole('link', { name: new RegExp(`模板副本 ${suffix}`) })
  await expect(card).toContainText('未发布')
})
