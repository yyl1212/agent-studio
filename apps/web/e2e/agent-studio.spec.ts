import { expect, test, type Locator, type Page } from '@playwright/test'

test('创建、测试、发布并运行 Agent', async ({ page }) => {
  const suffix = Date.now().toString(36)
  const { agentURL } = await buildAndPublish(page, `闭环助手 ${suffix}`, `e2e-flow-${suffix}`, '回答：{{topic}}')

  await page.goto(agentURL)
  await page.getByLabel('主题').fill('Workflow')
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
  await editor.getByLabel('模板').fill('V2：{{topic}}')
  await editor.getByRole('button', { name: '发布' }).click()
  await editor.getByRole('button', { name: '确认发布' }).click()
  await expect(editor.getByText('版本 v2 已发布。')).toBeVisible()

  await page.getByLabel('主题').fill('旧页')
  await page.getByRole('button', { name: '运行 Agent' }).click()
  await expect(page.getByText('Mock 回复：V1：旧页')).toBeVisible()

  await page.reload()
  await expect(page.getByText('Agent · v2')).toBeVisible()
  await page.getByLabel('主题').fill('新页')
  await page.getByRole('button', { name: '运行 Agent' }).click()
  await expect(page.getByText('Mock 回复：V2：新页')).toBeVisible()
  await editor.close()
})

async function buildAndPublish(page: Page, name: string, slug: string, template: string) {
  await page.goto('/workflows')
  await page.getByRole('button', { name: '新建工作流' }).click()
  await page.getByLabel('名称').fill(name)
  await page.getByLabel('Agent 地址标识').fill(slug)
  await page.getByRole('button', { name: '创建' }).click()
  await expect(page.getByText(name)).toBeVisible()
  const workflowURL = page.url()

  await page.getByTestId('node-start').click()
  await page.getByRole('button', { name: '添加一项' }).first().click()
  await page.getByLabel('字段标识').fill('topic')
  await page.getByLabel('字段标题').fill('主题')
  await page.getByLabel('字段类型').selectOption('text')
  await page.getByRole('checkbox', { name: '必填' }).check()

  await page.getByRole('button', { name: '添加节点' }).click()
  await page.getByRole('button', { name: '提示词模板' }).click()
  await page.getByLabel('模板').fill(template)
  await page.getByRole('button', { name: '添加节点' }).click()
  await page.getByRole('button', { name: 'LLM' }).click()
  await page.getByRole('button', { name: '关闭节点配置' }).click()

  await connectPorts(page, [
    ['start', 'topic', 'template', 'topic'],
    ['template', 'text', 'llm', 'prompt'],
    ['llm', 'text', 'end', 'result'],
  ])

  await page.getByRole('button', { name: '测试运行' }).click()
  await page.getByLabel('主题').fill('Agent')
  await page.getByRole('button', { name: '运行', exact: true }).click()
  await expect(page.getByText(`Mock 回复：${template.replace('{{topic}}', 'Agent')}`)).toBeVisible()
  await page.getByRole('button', { name: '关闭测试运行' }).click()

  await page.getByRole('button', { name: '发布' }).click()
  await page.getByRole('button', { name: '确认发布' }).click()
  const agentLink = page.getByRole('link', { name: '打开 Agent 页面' })
  await expect(agentLink).toBeVisible()
  const agentURL = await agentLink.getAttribute('href')
  if (!agentURL) throw new Error('发布后未返回 Agent URL')
  return { agentURL, workflowURL }
}

async function connectPorts(page: Page, connections: Array<[string, string, string, string]>) {
  for (const [sourceType, sourcePort, targetType, targetPort] of connections) {
    const edgeCount = await page.locator('.react-flow__edge').count()
    const sourceNode = page.getByTestId(`node-${sourceType}`).locator('..')
    const targetNode = page.getByTestId(`node-${targetType}`).locator('..')
    const source = sourceNode.locator(`.react-flow__handle.source[data-port$=":${sourcePort}"]`)
    const target = targetNode.locator(`.react-flow__handle.target[data-port$=":${targetPort}"]`)
    await dragHandle(page, source, target)
    await expect(page.locator('.react-flow__edge')).toHaveCount(edgeCount + 1)
  }
}

async function dragHandle(page: Page, source: Locator, target: Locator) {
  await expect(source).toBeVisible()
  await expect(target).toBeVisible()
  await source.hover()
  await page.mouse.down()
  await target.hover()
  await page.mouse.up()
}
