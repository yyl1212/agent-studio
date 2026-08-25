import { expect, type Locator, type Page } from '@playwright/test'

interface DraftGraph {
  schemaVersion: number
  nodes: Array<Record<string, unknown>>
  edges: Array<Record<string, unknown>>
}

export async function createWorkflow(page: Page, slug: string, name = `SDK Echo ${slug}`) {
  await page.goto('/workflows')
  await page.getByRole('button', { name: '新建工作流' }).click()
  await page.getByLabel('名称').fill(name)
  await page.getByLabel('Agent 地址标识').fill(slug)
  await page.getByRole('button', { name: '创建' }).click()
  await expect(page).toHaveURL(/\/workflows\/[0-9a-f-]+$/)
  await expect(page.getByText(name)).toBeVisible()
  return page.url()
}

export async function saveDraftGraph(page: Page, workflowID: string, graph: DraftGraph) {
  const endpoint = `http://127.0.0.1:8080/api/workflows/${workflowID}`
  const currentResponse = await page.request.get(endpoint)
  expect(currentResponse.ok()).toBe(true)
  const current = await currentResponse.json() as { draftRevision: number }
  const saveResponse = await page.request.put(endpoint, { data: { draftRevision: current.draftRevision, graph } })
  expect(saveResponse.ok()).toBe(true)
  return await saveResponse.json() as { draftRevision: number }
}

export async function configureStartField(page: Page, key: string, label: string, type: 'text' | 'json') {
  await page.getByTestId('node-start').click()
  await page.getByRole('button', { name: '添加一项' }).first().click()
  await page.getByLabel('字段标识').fill(key)
  await page.getByLabel('字段标题').fill(label)
  await page.getByLabel('字段类型').selectOption(type)
  await page.getByRole('checkbox', { name: '必填' }).check()
  await applyNodeConfig(page)
}

export async function configureStartTextField(page: Page, key: string, label: string) {
  await configureStartField(page, key, label, 'text')
}

export async function applyNodeConfig(page: Page) {
  const apply = page.getByRole('button', { name: '应用配置' })
  await expect(apply).toBeEnabled()
  await apply.click()
}

export async function openOptionalConfig(page: Page) {
  const summary = page.getByText('可选配置')
  if (await summary.count()) await summary.click()
}

export async function connectPorts(page: Page, connections: Array<[string, string, string, string]>) {
  await page.getByRole('button', { name: 'Fit View' }).click()
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

export async function connectIndexedPorts(
  page: Page,
  connections: Array<[string, number, string, string, number, string]>,
) {
  for (const [sourceType, sourceIndex, sourcePort, targetType, targetIndex, targetPort] of connections) {
    const edgeCount = await page.locator('.react-flow__edge').count()
    const sourceNode = page.getByTestId(`node-${sourceType}`).nth(sourceIndex).locator('..')
    const targetNode = page.getByTestId(`node-${targetType}`).nth(targetIndex).locator('..')
    await dragHandle(
      page,
      sourceNode.locator(`.react-flow__handle.source[data-port$=":${sourcePort}"]`),
      targetNode.locator(`.react-flow__handle.target[data-port$=":${targetPort}"]`),
    )
    await expect(page.locator('.react-flow__edge')).toHaveCount(edgeCount + 1)
  }
}

export async function moveIndexedNode(page: Page, nodeType: string, nodeIndex: number, xRatio: number, yRatio: number) {
  const canvas = page.getByRole('application')
  const node = page.getByTestId(`node-${nodeType}`).nth(nodeIndex).locator('..')
  await expect(node).toBeInViewport()
  const canvasBox = await canvas.boundingBox()
  const nodeBox = await node.boundingBox()
  if (!canvasBox || !nodeBox) throw new Error('无法计算节点的画布拖拽位置')

  await page.mouse.move(nodeBox.x + nodeBox.width / 2, nodeBox.y + nodeBox.height / 2)
  await page.mouse.down()
  await page.mouse.move(canvasBox.x + canvasBox.width * xRatio, canvasBox.y + canvasBox.height * yRatio, { steps: 8 })
  await page.mouse.up()
}

export async function dragHandle(page: Page, source: Locator, target: Locator) {
  await expect(source).toBeVisible()
  await expect(target).toBeVisible()
  await source.hover()
  await page.mouse.down()
  await target.hover()
  await page.mouse.up()
}
