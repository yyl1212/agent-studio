import { expect, test } from '@playwright/test'

import { configureStartTextField, connectPorts, createWorkflow, openMoreActions, saveDraftGraph } from './helpers'

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
  await openMoreActions(page)
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

test('运行中的慢 Webhook 可协作取消并收敛为唯一终态', async ({ page }) => {
  const suffix = Date.now().toString(36)
  const workflowURL = await createWorkflow(page, `cancel-${suffix}`, `取消验证 ${suffix}`)
  const workflowID = workflowURL.split('/').at(-1)
  if (!workflowID) throw new Error('创建后未获得工作流 ID')
  const workflow = await saveDraftGraph(page, workflowID, {
    schemaVersion: 1,
    nodes: [
      { id: 'start', type: 'start', typeVersion: '1', position: { x: 0, y: 0 }, config: { fields: [{ key: 'payload', label: '请求体', type: 'json', required: true }] } },
      { id: 'webhook', type: 'extension.webhook', typeVersion: '1.0.0', position: { x: 300, y: 0 }, config: { path: 'slow', timeoutMs: 30000 } },
      { id: 'end', type: 'end', typeVersion: '1', position: { x: 600, y: 0 }, config: {} },
    ],
    edges: [
      { id: 'start-webhook', source: 'start', sourcePort: 'payload', target: 'webhook', targetPort: 'body' },
      { id: 'webhook-end', source: 'webhook', sourcePort: 'body', target: 'end', targetPort: 'result' },
    ],
  })
  const runResponse = await fetch(`http://127.0.0.1:8080/api/workflows/${workflowID}/test-runs`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ draftRevision: workflow.draftRevision, input: { payload: { cancel: true } } }),
  })
  if (!runResponse.ok) throw new Error(`启动慢运行失败：${runResponse.status} ${await runResponse.text()}`)
  await page.goto(`/runs?workflowId=${workflowID}`)
  const runButton = page.getByRole('button', { name: /查看运行/ }).first()
  await expect(runButton).toBeVisible()
  const runID = (await runButton.getAttribute('aria-label'))?.replace('查看运行 ', '')
  if (!runID) throw new Error('运行列表未提供 Run ID')
  await expect.poll(async () => {
    const response = await page.request.get(`http://127.0.0.1:8080/api/runs/${runID}`)
    if (!response.ok()) return `http-${response.status()}`
    const detail = await response.json() as { run: { status: string } }
    return detail.run.status
  }).toBe('running')
  await runButton.click()
  await expect(page.getByRole('dialog', { name: '运行详情' })).toContainText('运行中')
  await page.getByRole('button', { name: '取消运行' }).click()
  await expect(page.getByText(/外部副作用可能无法撤回/)).toBeVisible()
  const cancellingResponse = page.waitForResponse((response) => response.url().endsWith(`/api/runs/${runID}/cancel`) && response.request().method() === 'POST')
  await page.getByRole('button', { name: '确认取消' }).click()
  expect((await cancellingResponse).ok()).toBe(true)
  expect(await (await cancellingResponse).json()).toMatchObject({ status: 'cancelling' })
  await expect(page.getByText('已取消').first()).toBeVisible({ timeout: 15_000 })
  await runResponse.text()
  const eventsResponse = await page.request.get(`http://127.0.0.1:8080/api/runs/${runID}/events?afterSequence=0`)
  expect(eventsResponse.ok()).toBe(true)
  const events = await eventsResponse.json() as { events: Array<{ type: string }> }
  expect(events.events.filter((event) => event.type === 'run.cancelled')).toHaveLength(1)
})

test('失败运行重试必须重填秘密且公共载荷不泄漏', async ({ page }) => {
  const suffix = Date.now().toString(36)
  const workflowURL = await createWorkflow(page, `retry-${suffix}`, `重试验证 ${suffix}`)
  const workflowID = workflowURL.split('/').at(-1)
  if (!workflowID) throw new Error('创建后未获得工作流 ID')
  const workflow = await saveDraftGraph(page, workflowID, {
    schemaVersion: 1,
    nodes: [
      { id: 'start', type: 'start', typeVersion: '1', position: { x: 0, y: 0 }, config: { fields: [{ key: 'webhookToken', label: 'Webhook Token', type: 'text', required: true }] } },
      { id: 'code', type: 'code', typeVersion: '1', position: { x: 300, y: 0 }, config: { source: 'def main(input):\n  return 1 // 0' } },
      { id: 'end', type: 'end', typeVersion: '1', position: { x: 600, y: 0 }, config: {} },
    ],
    edges: [
      { id: 'start-code', source: 'start', sourcePort: 'webhookToken', target: 'code', targetPort: 'input' },
      { id: 'code-end', source: 'code', sourcePort: 'result', target: 'end', targetPort: 'result' },
    ],
  })
  const originalSecret = 'original-e2e-secret'
  const replacementSecret = 'replacement-e2e-secret'
  const sourceResponse = await fetch(`http://127.0.0.1:8080/api/workflows/${workflowID}/test-runs`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ draftRevision: workflow.draftRevision, input: { webhookToken: originalSecret } }),
  })
  expect(sourceResponse.ok).toBe(true)
  const sourceEvents = parseNDJSON(await sourceResponse.text())
  const sourceRunID = sourceEvents.find((event) => event.type === 'run.started')?.runId
  if (typeof sourceRunID !== 'string') throw new Error('源运行未返回 run.started')

  await page.goto(`/runs?runId=${sourceRunID}`)
  const sourceRunButton = page.getByRole('button', { name: `查看运行 ${sourceRunID}` })
  await sourceRunButton.click()
  const detail = page.getByRole('dialog', { name: '运行详情' })
  await expect(detail).toContainText('[REDACTED]')
  await expect(detail).not.toContainText(originalSecret)
  for (const viewport of [{ width: 1440, height: 900 }, { width: 1024, height: 768 }, { width: 768, height: 900 }]) {
    await page.setViewportSize(viewport)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  }
  await page.keyboard.press('Escape')
  await expect(detail).toHaveCount(0)
  await expect(sourceRunButton).toBeFocused()
  await sourceRunButton.click()
  const sourceBeforeResponse = await page.request.get(`http://127.0.0.1:8080/api/runs/${sourceRunID}`)
  expect(sourceBeforeResponse.ok()).toBe(true)
  const sourceBefore = await sourceBeforeResponse.text()
  await detail.getByRole('button', { name: '重新运行' }).click()
  const retry = page.getByRole('dialog', { name: '重新运行' })
  const secret = retry.getByLabel('Webhook Token')
  await expect(secret).toBeFocused()
  await expect(secret).toHaveValue('')
  for (const viewport of [{ width: 1440, height: 900 }, { width: 1024, height: 768 }, { width: 768, height: 900 }]) {
    await page.setViewportSize(viewport)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  }
  await secret.fill(replacementSecret)
  let releaseRetry!: () => void
  const retryBarrier = new Promise<void>((resolve) => { releaseRetry = resolve })
  let observeRetry!: () => void
  const retryObserved = new Promise<void>((resolve) => { observeRetry = resolve })
  let retryRequests = 0
  await page.route(`**/api/runs/${sourceRunID}/retries`, async (route) => {
    retryRequests += 1
    observeRetry()
    await retryBarrier
    await route.continue()
  })
  const submit = retry.getByRole('button', { name: '重新运行', exact: true })
  await submit.dblclick()
  await retryObserved
  await expect(retry.getByRole('button', { name: '正在重新运行…' })).toBeDisabled()
  await page.keyboard.press('Escape')
  await expect(retry).toBeVisible()
  releaseRetry()
  await expect.poll(() => new URL(page.url()).searchParams.get('runId')).not.toBe(sourceRunID)
  expect(retryRequests).toBe(1)
  await page.unroute(`**/api/runs/${sourceRunID}/retries`)
  const retryRunID = new URL(page.url()).searchParams.get('runId')
  if (!retryRunID) throw new Error('重试后未选择新 Run')
  await expect(page.getByRole('dialog', { name: '运行详情' })).toBeVisible()
  await expect.poll(async () => {
    const response = await page.request.get(`http://127.0.0.1:8080/api/runs/${retryRunID}`)
    if (!response.ok()) return `http-${response.status()}`
    const detail = await response.json() as { run: { status: string } }
    return detail.run.status
  }).toBe('failed')

  const publicResponses = await Promise.all([
    page.request.get(`http://127.0.0.1:8080/api/runs/${sourceRunID}`),
    page.request.get(`http://127.0.0.1:8080/api/runs/${sourceRunID}/events?afterSequence=0`),
    page.request.get(`http://127.0.0.1:8080/api/runs/${retryRunID}`),
    page.request.get(`http://127.0.0.1:8080/api/runs/${retryRunID}/events?afterSequence=0`),
  ])
  for (const response of publicResponses) expect(response.ok()).toBe(true)
  const responseTexts = await Promise.all(publicResponses.map((response) => response.text()))
  const publicText = responseTexts.join('\n') + await page.locator('body').innerText()
  expect(publicText).not.toContain(originalSecret)
  expect(publicText).not.toContain(replacementSecret)
  expect(publicText).not.toContain('Authorization')
  expect(publicText).not.toContain('Idempotency-Key')
  expect(publicText).toContain('[REDACTED]')
  expect(responseTexts[0]).toBe(sourceBefore)
  const retryDetail = JSON.parse(responseTexts[2]) as { run?: { retryOfRunId?: string } }
  expect(retryDetail.run?.retryOfRunId).toBe(sourceRunID)
})

function parseNDJSON(text: string): Array<Record<string, unknown>> {
  return text.trim().split('\n').filter(Boolean).map((line) => JSON.parse(line) as Record<string, unknown>)
}
