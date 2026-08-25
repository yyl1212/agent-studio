import { expect, test } from '@playwright/test'

import { configureStartTextField, connectPorts, createWorkflow } from './helpers'

test('工作流与运行管理闭环在三档宽度下可用', async ({ page }) => {
  const suffix = Date.now().toString(36)
  const originalName = `管理助手 ${suffix}`
  const originalSlug = `management-${suffix}`
  const workflowURL = await createWorkflow(page, originalSlug, originalName)
  const workflowID = workflowURL.split('/').at(-1)
  if (!workflowID) throw new Error('创建后未获得工作流 ID')

  await page.getByRole('link', { name: '返回工作流列表' }).click()
  await page.getByLabel('搜索工作流').fill(originalName)
  await expect(page.getByRole('link', { name: originalName })).toBeVisible()
  await expect(page.getByLabel('工作流状态')).toHaveValue('active')

  await page.getByRole('button', { name: `${originalName} 的操作` }).click()
  await page.getByRole('button', { name: '复制', exact: true }).click()
  const copyName = `管理副本 ${suffix}`
  const copySlug = `management-copy-${suffix}`
  await page.getByLabel('名称').fill(copyName)
  await page.getByLabel('Agent 地址标识').fill(copySlug)
  await page.getByRole('button', { name: '创建副本' }).click()
  await expect(page).toHaveURL(/\/workflows\/[0-9a-f-]+$/)

  await page.getByRole('link', { name: '返回工作流列表' }).click()
  await page.getByLabel('搜索工作流').fill(copySlug)
  const copyRow = page.getByRole('row').filter({ hasText: copyName })
  await expect(copyRow).toContainText('r1')
  await expect(copyRow).toContainText('未发布')

  await page.getByRole('button', { name: `${copyName} 的操作` }).click()
  await page.getByRole('button', { name: '重命名' }).click()
  const renamedCopy = `已重命名副本 ${suffix}`
  await page.getByLabel('名称').fill(renamedCopy)
  await page.getByRole('button', { name: '保存修改' }).click()
  await expect(page.getByRole('link', { name: renamedCopy })).toBeVisible()

  await page.getByRole('button', { name: `${renamedCopy} 的操作` }).click()
  await page.getByRole('button', { name: '归档' }).click()
  await page.getByRole('button', { name: '确认归档' }).click()
  await expect(page.getByRole('status')).toHaveText('已归档')
  await page.getByLabel('工作流状态').selectOption('archived')
  await expect(page.getByRole('link', { name: renamedCopy })).toBeVisible()
  await page.getByRole('link', { name: renamedCopy }).click()
  await expect(page.getByRole('status')).toContainText('已归档，只读模式')

  await page.getByRole('link', { name: '返回工作流列表' }).click()
  await page.getByLabel('工作流状态').selectOption('archived')
  await page.getByLabel('搜索工作流').fill(copySlug)
  await page.getByRole('button', { name: `${renamedCopy} 的操作` }).click()
  await page.getByRole('button', { name: '恢复' }).click()
  await expect(page.getByRole('status')).toContainText('已恢复')

  await page.goto(workflowURL)
  await configureStartTextField(page, 'topic', '主题')
  await connectPorts(page, [['start', 'topic', 'end', 'result']])
  await page.getByRole('button', { name: '测试运行' }).click()
  await page.getByLabel('主题', { exact: true }).fill(`输出 ${suffix}`)
  await page.getByRole('button', { name: '运行', exact: true }).click()
  await expect(page.locator('.run-output')).toContainText(`输出 ${suffix}`)
  await page.getByRole('link', { name: '运行记录' }).click()
  await expect(page).toHaveURL(`/runs?workflowId=${workflowID}`)

  await page.getByLabel('已完成').click()
  await expect(page.getByLabel('已完成')).toBeChecked()
  await page.getByLabel('草稿测试').click()
  await expect(page.getByLabel('草稿测试')).toBeChecked()
  await page.getByLabel('开始时间下限').fill(new Date(Date.now() - 60_000).toISOString())
  await page.getByLabel('开始时间上限').fill(new Date(Date.now() + 60_000).toISOString())
  const runButton = page.getByRole('button', { name: /查看运行/ }).first()
  await expect(runButton).toBeVisible()
  await runButton.click()
  const detail = page.getByRole('dialog', { name: '运行详情' })
  await expect(detail).toContainText(`输出 ${suffix}`)
  await expect(detail.getByRole('link', { name: '调试回放' })).toBeVisible()
  await expect(detail.getByRole('button', { name: /取消|重新运行/ })).toHaveCount(0)

  await page.getByRole('button', { name: '关闭工作台' }).click()
  for (const viewport of [{ width: 1440, height: 900 }, { width: 1024, height: 768 }, { width: 768, height: 900 }]) {
    await page.setViewportSize(viewport)
    await expect(page.getByRole('heading', { name: '运行' })).toBeVisible()
    await expect(page.locator('.management-table-scroll')).toHaveCSS('overflow-x', 'auto')
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  }
})
