import { expect, type Locator, type Page } from '@playwright/test'

export async function createWorkflow(page: Page, slug: string, name = `SDK Echo ${slug}`) {
  await page.goto('/workflows')
  await page.getByRole('button', { name: '新建工作流' }).click()
  await page.getByLabel('名称').fill(name)
  await page.getByLabel('Agent 地址标识').fill(slug)
  await page.getByRole('button', { name: '创建' }).click()
  await expect(page.getByText(name)).toBeVisible()
  return page.url()
}

export async function configureStartTextField(page: Page, key: string, label: string) {
  await page.getByTestId('node-start').click()
  await page.getByRole('button', { name: '添加一项' }).first().click()
  await page.getByLabel('字段标识').fill(key)
  await page.getByLabel('字段标题').fill(label)
  await page.getByLabel('字段类型').selectOption('text')
  await page.getByRole('checkbox', { name: '必填' }).check()
}

export async function connectPorts(page: Page, connections: Array<[string, string, string, string]>) {
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

export async function dragHandle(page: Page, source: Locator, target: Locator) {
  await expect(source).toBeVisible()
  await expect(target).toBeVisible()
  await source.hover()
  await page.mouse.down()
  await target.hover()
  await page.mouse.up()
}
