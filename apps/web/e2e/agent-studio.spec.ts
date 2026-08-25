import { expect, test, type Page } from '@playwright/test'

import { applyNodeConfig, configureStartTextField, connectPorts, createWorkflow } from './helpers'

test('创建、测试、发布并运行 Agent', async ({ page }) => {
  const suffix = Date.now().toString(36)
  const { agentURL } = await buildAndPublish(page, `闭环助手 ${suffix}`, `e2e-flow-${suffix}`, '回答：{{topic}}')

  await page.goto(agentURL)
  await page.getByLabel('主题', { exact: true }).fill('Workflow')
  await page.getByRole('button', { name: '运行 Agent' }).click()
  await expect(page.getByText('Mock 回复：回答：Workflow')).toBeVisible()
})

test('已加载 Agent 固定旧版本，刷新后切换新版本', async ({ page, context }) => {
  const suffix = Date.now().toString(36)
  const { agentURL, workflowURL } = await buildAndPublish(page, `版本助手 ${suffix}`, `e2e-version-${suffix}`, 'V1：{{topic}}')
  await page.goto(agentURL)
  await expect(page.getByText('Agent · v1')).toBeVisible()

  const editor = await context.newPage()
  await editor.goto(workflowURL)
  await editor.getByTestId('node-template').click()
  await editor.getByLabel('模板', { exact: true }).fill('V2：{{topic}}')
  await applyNodeConfig(editor)
  await editor.getByRole('button', { name: '发布' }).click()
  await editor.getByRole('button', { name: '确认发布' }).click()
  await expect(editor.getByText('版本 v2 已发布。')).toBeVisible()

  await page.getByLabel('主题', { exact: true }).fill('旧页')
  await page.getByRole('button', { name: '运行 Agent' }).click()
  await expect(page.getByText('Mock 回复：V1：旧页')).toBeVisible()

  await page.reload()
  await expect(page.getByText('Agent · v2')).toBeVisible()
  await page.getByLabel('主题', { exact: true }).fill('新页')
  await page.getByRole('button', { name: '运行 Agent' }).click()
  await expect(page.getByText('Mock 回复：V2：新页')).toBeVisible()
  await editor.close()
})

test('LLM v2 结构化输出完成草稿、发布、Agent 与运行记录闭环', async ({ page }) => {
  const suffix = Date.now().toString(36)
  const workflowURL = await createWorkflow(page, `e2e-structured-${suffix}`, `结构化助手 ${suffix}`)
  await configureStartTextField(page, 'topic', '主题')

  await page.getByRole('button', { name: '添加节点' }).click()
  await page.getByRole('button', { name: '提示词模板' }).click()
  await page.getByLabel('模板', { exact: true }).fill('回答：{{topic}}')
  await applyNodeConfig(page)
  await page.getByRole('button', { name: '添加节点' }).click()
  await page.getByRole('button', { name: /^LLM · 结构化输出/ }).click()
  await page.getByText('可选配置').click()
  await page.getByLabel('输出模式').selectOption('structured')
  await page.getByRole('button', { name: '添加一项' }).click()
  await page.getByRole('button', { name: '添加一项' }).click()
  await page.getByLabel('字段 Key').nth(0).fill('answer')
  await page.getByLabel('字段名称').nth(0).fill('回答')
  await page.getByLabel('字段类型').nth(0).selectOption('string')
  await page.getByLabel('字段 Key').nth(1).fill('score')
  await page.getByLabel('字段名称').nth(1).fill('分数')
  await page.getByLabel('字段类型').nth(1).selectOption('integer')
  await applyNodeConfig(page)
  await page.getByRole('button', { name: '关闭工作台' }).click()

  await connectPorts(page, [
    ['start', 'topic', 'template', 'topic'],
    ['template', 'text', 'llm', 'prompt'],
    ['llm', 'json', 'end', 'result'],
  ])

  await page.getByRole('button', { name: '测试运行' }).click()
  await page.getByLabel('主题', { exact: true }).fill('结构化')
  await page.getByRole('button', { name: '运行', exact: true }).click()
  await expect(page.locator('.run-output')).toContainText('Mock 回复：回答：结构化')
  await expect(page.locator('.run-output')).toContainText('"score": 0')
  await page.getByRole('button', { name: '关闭工作台' }).click()

  await page.getByRole('button', { name: '发布' }).click()
  await page.getByRole('button', { name: '确认发布' }).click()
  const agentLink = page.getByRole('link', { name: '打开 Agent 页面' })
  await expect(agentLink).toBeVisible()
  const agentURL = await agentLink.getAttribute('href')
  if (!agentURL) throw new Error('发布后未返回 Agent URL')
  await page.goto(agentURL)
  await page.getByLabel('主题', { exact: true }).fill('发布')
  await page.getByRole('button', { name: '运行 Agent' }).click()
  const result = page.getByRole('region', { name: '运行结果' })
  await expect(result).toContainText('"answer": "Mock 回复：回答：发布"')
  await expect(result).toContainText('"score": 0')

  await page.goto(workflowURL)
  await page.getByRole('link', { name: '运行记录' }).click()
  const publishedRun = page.getByRole('button', { name: /查看运行/ }).filter({ hasText: '已发布' }).first()
  await expect(publishedRun).toBeVisible()
  await publishedRun.click()
  await expect(page.getByText('llm · completed')).toBeVisible()
  await expect(page.locator('.run-detail')).toContainText('Mock 回复：回答：发布')
})

test('脏节点配置在切换工作台前支持继续编辑和放弃', async ({ page }) => {
  const suffix = Date.now().toString(36)
  await createWorkflow(page, `dirty-config-${suffix}`, `草稿保护 ${suffix}`)
  await page.getByRole('button', { name: '添加节点' }).click()
  await page.getByRole('button', { name: '提示词模板' }).click()
  await page.getByLabel('模板', { exact: true }).fill('未应用：{{topic}}')

  await page.getByRole('button', { name: '测试运行' }).click()
  await expect(page.getByRole('dialog', { name: '保存节点配置更改？' })).toBeVisible()
  await page.getByRole('button', { name: '取消' }).click()
  await expect(page.getByLabel('模板', { exact: true })).toHaveValue('未应用：{{topic}}')

  await page.getByRole('button', { name: '测试运行' }).click()
  await page.getByRole('button', { name: '放弃更改' }).click()
  await expect(page.getByRole('dialog', { name: '测试运行' })).toBeVisible()
  await page.getByRole('button', { name: '关闭工作台' }).click()
  await page.getByTestId('node-template').click()
  await expect(page.getByLabel('模板', { exact: true })).toHaveValue('')
})

test('工作台在桌面与窄屏均保持页面无水平溢出', async ({ page }) => {
  const suffix = Date.now().toString(36)
  await createWorkflow(page, `responsive-${suffix}`, `响应式工作台 ${suffix}`)
  for (const viewport of [{ width: 1440, height: 900 }, { width: 1024, height: 768 }, { width: 768, height: 900 }]) {
    await page.setViewportSize(viewport)
    await page.getByTestId('node-start').click()
    const panel = page.getByRole('dialog', { name: '开始' })
    await expect(panel).toBeVisible()
    const box = await panel.boundingBox()
    if (!box) throw new Error('无法读取工作台尺寸')
    expect(box.x).toBeGreaterThanOrEqual(0)
    expect(box.x + box.width).toBeLessThanOrEqual(viewport.width)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  }
})

async function buildAndPublish(page: Page, name: string, slug: string, template: string) {
  const workflowURL = await createWorkflow(page, slug, name)
  await configureStartTextField(page, 'topic', '主题')

  await page.getByRole('button', { name: '添加节点' }).click()
  await page.getByRole('button', { name: '提示词模板' }).click()
  await page.getByLabel('模板', { exact: true }).fill(template)
  await applyNodeConfig(page)
  await page.getByRole('button', { name: '添加节点' }).click()
  await page.getByRole('button', { name: /^LLM 调用已配置的模型服务生成文本/ }).click()
  await page.getByRole('button', { name: '关闭工作台' }).click()

  await connectPorts(page, [
    ['start', 'topic', 'template', 'topic'],
    ['template', 'text', 'llm', 'prompt'],
    ['llm', 'text', 'end', 'result'],
  ])

  await page.getByRole('button', { name: '测试运行' }).click()
  await page.getByLabel('主题', { exact: true }).fill('Agent')
  await page.getByRole('button', { name: '运行', exact: true }).click()
  await expect(page.getByText(`Mock 回复：${template.replace('{{topic}}', 'Agent')}`)).toBeVisible()
  await page.getByRole('button', { name: '关闭工作台' }).click()

  await page.getByRole('button', { name: '发布' }).click()
  await page.getByRole('button', { name: '确认发布' }).click()
  const agentLink = page.getByRole('link', { name: '打开 Agent 页面' })
  await expect(agentLink).toBeVisible()
  const agentURL = await agentLink.getAttribute('href')
  if (!agentURL) throw new Error('发布后未返回 Agent URL')
  return { agentURL, workflowURL }
}
