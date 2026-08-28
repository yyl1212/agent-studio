import { expect, test, type Page } from '@playwright/test'

import { applyNodeConfig, configureAgentPresentation, configureStartTextField, connectPorts, createWorkflow, openMoreActions, saveDraftGraph, type AgentPresentationSettings } from './helpers'

test('点击预览可取消并在确认后只创建一次', async ({ page }) => {
  await createWorkflow(page, `placement-${Date.now()}`, '放置预览')
  await page.getByRole('button', { name: '添加节点' }).click()
  await page.getByRole('button', { name: /^提示词模板/ }).click()
  await expect(page.getByText('点击画布放置，Esc 取消')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.getByTestId('node-template')).toHaveCount(0)

  await page.getByRole('button', { name: '添加节点' }).click()
  await page.getByRole('button', { name: /^提示词模板/ }).click()
  await placeNodePreview(page)
  await expect(page.getByTestId('node-template')).toHaveCount(1)
})

test('节点目录和结构化卡片在三档视口无溢出', async ({ page }) => {
  await createWorkflow(page, `visual-${Date.now()}`, '节点视觉')
  await expect(page.getByTestId('node-start')).toContainText('工作流唯一')
  for (const viewport of [{ width: 390, height: 844 }, { width: 768, height: 1024 }, { width: 1440, height: 900 }]) {
    await page.setViewportSize(viewport)
    await page.getByRole('button', { name: '添加节点' }).click()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    await page.keyboard.press('Escape')
  }
})

test('节点库键盘路径可浏览并取消预览', async ({ page }) => {
  await createWorkflow(page, `keyboard-${Date.now()}`, '键盘添加')
  await page.keyboard.press(process.platform === 'darwin' ? 'Meta+K' : 'Control+K')
  await page.getByLabel('搜索节点').fill('提示词')
  await page.keyboard.press('ArrowDown')
  await page.keyboard.press('Enter')
  await expect(page.getByText('点击画布放置，Esc 取消')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.getByTestId('node-template')).toHaveCount(0)
})

test('新建工作流保护唯一边界并显示首节点引导', async ({ page }) => {
  const suffix = Date.now().toString(36)
  await createWorkflow(page, `boundary-${suffix}`, `边界保护 ${suffix}`)

  await expect(page.getByTestId('node-start')).toHaveCount(1)
  await expect(page.getByTestId('node-end')).toHaveCount(1)
  const guide = page.getByRole('button', { name: '在这里添加第一个节点' })
  await expect(guide).toBeVisible()
  await expect(page.locator('.canvas-empty-guide')).toHaveCSS('display', 'grid')
  const guideBox = await guide.boundingBox()
  if (!guideBox) throw new Error('无法读取首节点引导按钮尺寸')
  expect(guideBox.height).toBeGreaterThanOrEqual(44)

  await page.getByTestId('node-start').click()
  await page.keyboard.press('Delete')
  await expect(page.getByTestId('node-start')).toHaveCount(1)
  await expect(page.getByText(/不可删除/)).toBeVisible()

  for (const viewport of [
    { width: 1440, height: 900 },
    { width: 768, height: 900 },
    { width: 390, height: 844 },
  ]) {
    await page.setViewportSize(viewport)
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    ).toBe(true)
  }
})

test('草稿 API 拒绝缺失开始节点且不推进 revision', async ({ page }) => {
  const suffix = Date.now().toString(36)
  const workflowURL = await createWorkflow(
    page,
    `boundary-api-${suffix}`,
    `边界 API ${suffix}`,
  )
  const workflowID = workflowURL.split('/').at(-1)
  if (!workflowID) throw new Error('创建后未获得工作流 ID')
  const endpoint = `http://127.0.0.1:8080/api/workflows/${workflowID}`
  const beforeResponse = await page.request.get(endpoint)
  expect(beforeResponse.ok()).toBe(true)
  const before = await beforeResponse.json() as {
    draftRevision: number
    draftGraph: { nodes: Array<{ type: string }>; edges: unknown[] }
  }

  const rejected = await page.request.put(endpoint, {
    data: {
      draftRevision: before.draftRevision,
      graph: {
        ...before.draftGraph,
        nodes: before.draftGraph.nodes.filter((node) => node.type !== 'start'),
      },
    },
  })
  expect(rejected.status()).toBe(422)
  const failure = await rejected.json() as {
    code: string
    issues: Array<{ code: string }>
  }
  expect(failure.code).toBe('WORKFLOW_INVALID')
  expect(failure.issues).toContainEqual(
    expect.objectContaining({ code: 'WORKFLOW_START_COUNT' }),
  )

  const after = await (await page.request.get(endpoint)).json() as {
    draftRevision: number
  }
  expect(after.draftRevision).toBe(before.draftRevision)
})

test('历史异常草稿显式修复后才替换画布', async ({ page }) => {
  const suffix = Date.now().toString(36)
  const workflowURL = await createWorkflow(
    page,
    `boundary-repair-${suffix}`,
    `历史修复 ${suffix}`,
  )
  const workflowID = workflowURL.split('/').at(-1)
  if (!workflowID) throw new Error('创建后未获得工作流 ID')
  const endpoint = `http://127.0.0.1:8080/api/workflows/${workflowID}`
  const valid = await (await page.request.get(endpoint)).json() as {
    draftRevision: number
    draftGraph: {
      schemaVersion: number
      nodes: Array<{ id: string; type: string }>
      edges: unknown[]
    }
    [key: string]: unknown
  }
  const invalid = {
    ...valid,
    draftGraph: {
      ...valid.draftGraph,
      nodes: valid.draftGraph.nodes.filter((node) => node.type !== 'start'),
    },
  }
  let repairedGraph: typeof valid.draftGraph | undefined

  await page.route(`**/api/workflows/${workflowID}`, async (route) => {
    if (route.request().method() === 'GET') {
      await route.fulfill({ status: 200, json: invalid })
      return
    }
    if (route.request().method() === 'PUT') {
      const request = route.request().postDataJSON() as {
        graph: typeof valid.draftGraph
      }
      repairedGraph = request.graph
      await route.fulfill({
        status: 200,
        json: {
          ...valid,
          draftRevision: valid.draftRevision + 1,
          draftGraph: request.graph,
        },
      })
      return
    }
    await route.continue()
  })
  await page.reload()

  await expect(page.getByTestId('node-start')).toHaveCount(0)
  await page.getByRole('button', { name: '修复工作流边界' }).click()
  await page.getByRole('button', { name: '确认修复' }).click()
  await expect(page.getByTestId('node-start')).toHaveCount(1)
  expect(repairedGraph?.nodes.filter((node) => node.type === 'start')).toHaveLength(1)
  expect(repairedGraph?.nodes.filter((node) => node.type === 'end')).toHaveLength(1)
  expect(repairedGraph?.edges).toEqual([])
})

test('全画布内应用配置并试运行最新草稿', async ({ page }) => {
  const suffix = Date.now().toString(36)
  const workflowURL = await createWorkflow(page, `studio-ux-${suffix}`, `全画布助手 ${suffix}`)
  await configureStartTextField(page, 'topic', '主题')
  await page.getByRole('button', { name: '添加节点' }).click()
  await page.getByRole('button', { name: '提示词模板' }).click()
  await placeNodePreview(page)
  await page.getByLabel('模板', { exact: true }).fill('初稿：{{topic}}')
  await applyNodeConfig(page)
  await page.getByRole('button', { name: '关闭工作台' }).click()
  await connectPorts(page, [['start', 'topic', 'template', 'topic'], ['template', 'text', 'end', 'result']])

  await page.getByTestId('node-template').click()
  await page.getByLabel('模板', { exact: true }).fill('最新：{{topic}}')
  await page.getByRole('button', { name: '应用并试运行' }).click()
  await expect(page).toHaveURL(workflowURL)
  await expect(page.getByRole('dialog', { name: '测试运行' })).toBeVisible()
  await page.getByLabel('主题', { exact: true }).fill('Agent Studio')
  await page.getByRole('button', { name: '运行', exact: true }).click()
  await expect(page.locator('.run-output')).toContainText('最新：Agent Studio')

  for (const viewport of [{ width: 1440, height: 900 }, { width: 1024, height: 768 }, { width: 390, height: 844 }]) {
    await page.setViewportSize(viewport)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  }
  await page.getByRole('button', { name: '关闭工作台' }).click()
  await expect(page.getByRole('button', { name: '添加节点' })).toBeEnabled()
  await page.getByRole('button', { name: '添加节点' }).click()
  const nodeSearch = page.getByLabel('搜索节点')
  await expect(nodeSearch).toBeVisible()
  await nodeSearch.click()
  await nodeSearch.fill('提示词')
  await page.getByRole('button', { name: /^提示词模板/ }).click()
  await placeNodePreview(page)
  await expect(page.getByRole('dialog', { name: '提示词模板' })).toBeVisible()
})

test('分层节点库支持分类、最近、拖放和移动端降级', async ({ page }) => {
  const suffix = Date.now().toString(36)
  await createWorkflow(page, `node-library-${suffix}`, `节点库体验 ${suffix}`)

  await page.getByRole('button', { name: '添加节点' }).click()
  const library = page.getByRole('dialog', { name: '节点库' })
  await expect(library).toBeVisible()
  await library.getByRole('button', { name: '文本', exact: true }).click()
  await expect(page.getByRole('button', { name: /^提示词模板/ })).toBeVisible()
  await expect(page.getByRole('button', { name: /^LLM/ })).toHaveCount(0)
  const desktopColumns = await page.locator('.node-library-grid').first().evaluate(
    (element) => getComputedStyle(element).gridTemplateColumns.split(' ').length,
  )
  expect(desktopColumns).toBe(2)

  await page.getByRole('button', { name: /^提示词模板/ }).click()
  await placeNodePreview(page)
  await expect(page.getByRole('dialog', { name: '提示词模板' })).toBeVisible()
  await page.getByRole('button', { name: '关闭工作台' }).click()
  await page.getByRole('button', { name: '添加节点' }).click()
  await library.getByRole('button', { name: '最近', exact: true }).click()
  await expect(page.getByRole('button', { name: /^提示词模板/ })).toBeVisible()

  await page.reload()
  await page.getByRole('button', { name: '添加节点' }).click()
  await page.getByRole('dialog', { name: '节点库' }).getByRole('button', { name: '最近', exact: true }).click()
  await expect(page.getByRole('button', { name: /^提示词模板/ })).toBeVisible()
  await page.getByRole('dialog', { name: '节点库' }).getByRole('button', { name: '全部', exact: true }).click()
  await page.getByRole('button', { name: /^LLM · 结构化输出/ }).dragTo(
    page.getByLabel('工作流画布'),
    { targetPosition: { x: 760, y: 280 } },
  )
  await expect(page.getByRole('dialog', { name: 'LLM · 结构化输出' })).toBeVisible()

  await page.getByRole('button', { name: '关闭工作台' }).click()
  await page.setViewportSize({ width: 390, height: 844 })
  await page.getByRole('button', { name: '添加节点' }).click()
  await expect(page.locator('.node-library-grid').first()).toHaveCSS(
    'grid-template-columns',
    /.+/,
  )
  const columns = await page.locator('.node-library-grid').first().evaluate(
    (element) => getComputedStyle(element).gridTemplateColumns.split(' ').length,
  )
  expect(columns).toBe(1)
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true)
  await page.getByRole('button', { name: /^提示词模板/ }).click()
  await placeNodePreview(page)
  await expect(page.getByRole('dialog', { name: '提示词模板' })).toBeVisible()
})

test('无效配置聚焦首错且输入时快捷键不打断编辑', async ({ page }) => {
  const suffix = Date.now().toString(36)
  await createWorkflow(page, `shortcut-guard-${suffix}`, `快捷键守卫 ${suffix}`)
  await page.getByTestId('node-start').click()
  await page.getByRole('button', { name: '添加一项' }).first().click()
  await page.getByRole('button', { name: '应用并试运行' }).click()
  await expect(page.getByLabel('字段标识')).toBeFocused()
  await page.keyboard.press('Control+K')
  await expect(page.getByRole('dialog', { name: '节点库' })).toHaveCount(0)
  await page.getByRole('button', { name: '关闭工作台' }).click()
  await page.getByRole('button', { name: '放弃更改' }).click()
  await page.keyboard.press('Control+K')
  await expect(page.getByRole('dialog', { name: '节点库' })).toBeVisible()
})

test('端口解析失败后保留配置并可重试', async ({ page }) => {
  const suffix = Date.now().toString(36)
  await createWorkflow(page, `resolve-retry-${suffix}`, `解析恢复 ${suffix}`)
  let failResolve = true
  await page.route('**/api/node-types/template/1/resolve', async (route) => {
    if (failResolve) {
      failResolve = false
      await route.fulfill({ status: 503, contentType: 'application/json', body: '{"code":"TEMPORARY_UNAVAILABLE","message":"解析服务暂不可用"}' })
      return
    }
    await route.continue()
  })
  await page.getByRole('button', { name: '添加节点' }).click()
  await page.getByRole('button', { name: '提示词模板' }).click()
  await placeNodePreview(page)
  await page.getByLabel('模板', { exact: true }).fill('回答：{{topic}}')
  await expect(page.getByRole('button', { name: '重试解析端口' })).toBeVisible()
  await page.getByRole('button', { name: '重试解析端口' }).click()
  await expect(page.getByRole('button', { name: '应用并试运行' })).toBeEnabled()
  await expect(page.getByLabel('模板', { exact: true })).toHaveValue('回答：{{topic}}')
})

test('保存失败后保留草稿并可重试', async ({ page }) => {
  const suffix = Date.now().toString(36)
  const workflowURL = await createWorkflow(page, `save-retry-${suffix}`, `保存恢复 ${suffix}`)
  const workflowID = new URL(workflowURL).pathname.split('/').at(-1)
  if (!workflowID) throw new Error('创建后未获得工作流 ID')
  let failSave = true
  await page.route(`**/api/workflows/${workflowID}`, async (route) => {
    if (route.request().method() === 'PUT' && failSave) {
      failSave = false
      await route.fulfill({ status: 503, contentType: 'application/json', body: '{"code":"TEMPORARY_UNAVAILABLE","message":"保存服务暂不可用"}' })
      return
    }
    await route.continue()
  })
  await page.getByRole('button', { name: '添加节点' }).click()
  await page.getByRole('button', { name: '提示词模板' }).click()
  await placeNodePreview(page)
  await expect(page.getByRole('button', { name: '重试保存' })).toBeVisible()
  await expect(page.getByRole('button', { name: '测试运行' })).toBeDisabled()
  await expect(page.getByRole('button', { name: '发布' })).toBeDisabled()
  await page.getByRole('button', { name: '重试保存' }).click()
  await expect(page.getByText('已保存')).toBeVisible()
  await expect(page.getByTestId('node-template')).toBeVisible()
})

test('创建、测试、发布并运行 Agent', async ({ page }) => {
  const suffix = Date.now().toString(36)
  const settings = { title: '聚焦研究助手', description: '输入主题生成答案', accent: 'teal' as const, submitLabel: '开始研究', resultMode: 'json' as const }
  const { agentURL } = await buildAndPublish(page, `闭环助手 ${suffix}`, `e2e-flow-${suffix}`, '回答：{{topic}}', settings)

  await page.goto(agentURL)
  await expect(page.getByRole('heading', { name: settings.title })).toBeVisible()
  await expect(page.locator('.agent-shell')).toHaveClass(/accent-teal/)
  await page.getByLabel('主题', { exact: true }).fill('Workflow')
  await page.getByRole('button', { name: settings.submitLabel }).click()
  await expect(page).toHaveURL(/\?runId=[0-9a-f-]+$/)
  await expect(page.getByRole('region', { name: '运行结果' })).toContainText('Mock 回复：回答：Workflow')
  const manifest = await (await page.request.get(`http://127.0.0.1:8080/api/agents/e2e-flow-${suffix}`)).json() as { workflowVersionId: string }
  const legacy = await page.request.post(`http://127.0.0.1:8080/api/agents/e2e-flow-${suffix}/runs`, {
    data: { workflowVersionId: manifest.workflowVersionId, input: { topic: 'Legacy' } },
  })
  expect(legacy.ok()).toBe(true)
  expect(legacy.headers()['content-type']).toContain('application/x-ndjson')
  expect(await legacy.text()).toContain('run.completed')
})

test('已加载 Agent 固定旧版本，刷新后切换新版本', async ({ page, context }) => {
  const suffix = Date.now().toString(36)
  const { agentURL, workflowURL } = await buildAndPublish(page, `版本助手 ${suffix}`, `e2e-version-${suffix}`, 'V1：{{topic}}')
  await page.goto(agentURL)
  await expect(page.getByText('Agent · v1')).toBeVisible()
  await page.getByLabel('主题', { exact: true }).fill('旧页')
  await page.getByRole('button', { name: '运行 Agent' }).click()
  await expect(page.getByText('Mock 回复：V1：旧页')).toBeVisible()
  await expect(page).toHaveURL(/\?runId=[0-9a-f-]+$/)

  const editor = await context.newPage()
  await editor.goto(workflowURL)
  await editor.getByTestId('node-template').click()
  await editor.getByLabel('模板', { exact: true }).fill('V2：{{topic}}')
  await applyNodeConfig(editor)
  await editor.getByRole('button', { name: '发布' }).click()
  await editor.getByRole('button', { name: '确认发布' }).click()
  await expect(editor.getByText('版本 v2 已发布。')).toBeVisible()

  await page.reload()
  await expect(page.getByText('Agent · v1')).toBeVisible()
  await expect(page.getByText('Mock 回复：V1：旧页')).toBeVisible()
  await page.getByRole('button', { name: '再次运行' }).click()
  await expect(page.getByText('Agent · v2')).toBeVisible()
  await page.getByLabel('主题', { exact: true }).fill('新页')
  await page.getByRole('button', { name: '运行 Agent' }).click()
  await expect(page.getByText('Mock 回复：V2：新页')).toBeVisible()
  await editor.close()
})

test('活动 Agent 运行刷新后继续并可取消', async ({ page }) => {
  const suffix = Date.now().toString(36)
  const slug = `agent-recover-${suffix}`
  const workflowURL = await createWorkflow(page, slug, `恢复助手 ${suffix}`)
  const workflowID = workflowURL.split('/').at(-1)
  if (!workflowID) throw new Error('创建后未获得工作流 ID')
  const saved = await saveDraftGraph(page, workflowID, {
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
  const publish = await page.request.post(`http://127.0.0.1:8080/api/workflows/${workflowID}/publish`, { data: { draftRevision: saved.draftRevision } })
  expect(publish.ok()).toBe(true)
  await page.goto(`/agents/${slug}`)
  await page.getByLabel('请求体').fill('{"slow":true}')
  await page.getByRole('button', { name: '运行 Agent' }).click()
  await expect(page).toHaveURL(/\?runId=[0-9a-f-]+$/)
  const runID = new URL(page.url()).searchParams.get('runId')
  if (!runID) throw new Error('运行 URL 未包含 runId')
  const wrongSlug = await page.request.get(`http://127.0.0.1:8080/api/agents/wrong-${slug}/runs/${runID}`)
  expect(wrongSlug.status()).toBe(404)
  await expect(page.getByText('正在运行')).toBeVisible()
  await page.reload()
  await expect(page.getByText(/正在恢复运行|正在运行/)).toBeVisible()
  await page.getByRole('button', { name: '取消运行' }).click()
  await expect(page.getByText('运行已取消')).toBeVisible({ timeout: 15_000 })
  await expect(page.locator('body')).not.toContainText('{"slow":true}')
  await page.setViewportSize({ width: 390, height: 844 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
})

test('LLM v2 结构化输出完成草稿、发布、Agent 与运行记录闭环', async ({ page }) => {
  const suffix = Date.now().toString(36)
  const workflowURL = await createWorkflow(page, `e2e-structured-${suffix}`, `结构化助手 ${suffix}`)
  await configureStartTextField(page, 'topic', '主题')

  await page.getByRole('button', { name: '添加节点' }).click()
  await page.getByRole('button', { name: '提示词模板' }).click()
  await placeNodePreview(page)
  await page.getByLabel('模板', { exact: true }).fill('回答：{{topic}}')
  await applyNodeConfig(page)
  await page.getByRole('button', { name: '添加节点' }).click()
  await page.getByRole('button', { name: /^LLM · 结构化输出/ }).click()
  await placeNodePreview(page)
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
  await openMoreActions(page)
  await page.getByRole('link', { name: '运行记录' }).click()
  const publishedRun = page.getByRole('row').filter({ hasText: '已发布' }).getByRole('button', { name: /查看运行/ }).first()
  await expect(publishedRun).toBeVisible()
  await publishedRun.click()
  await expect(page.getByText('llm · completed')).toBeVisible()
  await expect(page.locator('.run-detail-content')).toContainText('Mock 回复：回答：发布')
})

test('脏节点配置在切换工作台前支持继续编辑和放弃', async ({ page }) => {
  const suffix = Date.now().toString(36)
  await createWorkflow(page, `dirty-config-${suffix}`, `草稿保护 ${suffix}`)
  await page.getByRole('button', { name: '添加节点' }).click()
  await page.getByRole('button', { name: '提示词模板' }).click()
  await placeNodePreview(page)
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

test('版本比较、恢复草稿和撤销保持线上版本不变', async ({ page }) => {
  const suffix = Date.now().toString(36)
  const slug = `version-governance-${suffix}`
  const workflowURL = await createWorkflow(page, slug, `版本治理 ${suffix}`)
  const workflowID = workflowURL.split('/').at(-1)
  if (!workflowID) throw new Error('创建后未获得工作流 ID')
  const endpoint = `http://127.0.0.1:8080/api/workflows/${workflowID}`

  const v1Draft = await saveDraftGraph(page, workflowID, versionGraph('主题', 'V1：{{topic}}'))
  const publishV1 = await page.request.post(`${endpoint}/publish`, { data: { draftRevision: v1Draft.draftRevision } })
  expect(publishV1.ok()).toBe(true)
  const v1 = await publishV1.json() as { id: string; version: number }
  expect(v1.version).toBe(1)

  const v2Draft = await saveDraftGraph(page, workflowID, versionGraph('研究主题', 'V2：{{topic}}'))
  const presentationResponse = await page.request.put(`${endpoint}/agent-presentation`, { data: {
    draftRevision: v2Draft.draftRevision,
    presentation: { title: 'V2 研究助手', description: '版本治理', accent: 'teal', submitLabel: '开始研究', resultMode: 'text' },
  } })
  expect(presentationResponse.ok()).toBe(true)
  const presented = await presentationResponse.json() as { draftRevision: number }
  const publishV2 = await page.request.post(`${endpoint}/publish`, { data: { draftRevision: presented.draftRevision } })
  expect(publishV2.ok()).toBe(true)
  const v2 = await publishV2.json() as { id: string; version: number }
  expect(v2.version).toBe(2)

  const manifestBefore = await (await page.request.get(`http://127.0.0.1:8080/api/agents/${slug}`)).json() as { version: number; workflowVersionId: string }
  const runResponse = await page.request.post(`http://127.0.0.1:8080/api/agents/${slug}/runs`, { data: { workflowVersionId: manifestBefore.workflowVersionId, input: { topic: '历史运行' } } })
  expect(runResponse.ok()).toBe(true)
  const runEvents = (await runResponse.text()).trim().split('\n').map((line) => JSON.parse(line) as { type: string; runId: string })
  const runID = runEvents.find((event) => event.type === 'run.started')?.runId
  if (!runID) throw new Error('Agent Run 未返回 runId')

  const draft = await saveDraftGraph(page, workflowID, versionGraph('研究主题', 'Draft：{{topic}}'))
  await page.goto(workflowURL)
  await expect(page.getByText('已保存')).toBeVisible()
  await openMoreActions(page)
  await page.getByRole('button', { name: '版本历史' }).click()
  await expect(page.getByRole('heading', { name: '版本历史' })).toBeFocused()
  await page.getByLabel('比较起点').selectOption('version:1')
  await page.getByLabel('比较终点').selectOption('version:2')
  await expect(page.getByRole('button', { name: /节点 · [1-9]/ })).toBeVisible()
  await page.getByRole('button', { name: /开始参数 · [1-9]/ }).click()
  await expect(page.locator('.version-diff-view')).toContainText('topic')
  await page.getByRole('button', { name: /Agent 页面 · [1-9]/ }).click()
  await expect(page.locator('.version-diff-view')).toContainText('页面标题')
	const draftOptionValue = await page.getByLabel('比较终点').locator('option').filter({ hasText: '当前草稿' }).getAttribute('value')
	if (!draftOptionValue) throw new Error(`未找到当前草稿选项（API revision r${draft.draftRevision}）`)
  await page.getByLabel('比较终点').selectOption(draftOptionValue)
  await expect(page.locator('.version-diff-view')).toContainText('Draft：{{topic}}')

  await page.getByRole('button', { name: '恢复 v1 为草稿' }).click()
  const rollbackDialog = page.getByRole('dialog', { name: '恢复 v1 为草稿？' })
  await expect(rollbackDialog).toBeVisible()
  expect(await rollbackDialog.evaluate((element) => (element as HTMLDialogElement).matches(':modal'))).toBe(true)
  await expect(page.getByLabel('比较起点')).toBeDisabled()
  await page.getByRole('button', { name: '确认恢复' }).click()
  await expect(page.getByText('已回滚到版本 1')).toBeVisible()
  await expect(page.getByRole('button', { name: '撤销回滚' })).toBeVisible()
  const manifestAfter = await (await page.request.get(`http://127.0.0.1:8080/api/agents/${slug}`)).json() as { version: number; workflowVersionId: string }
  expect(manifestAfter.version).toBe(2)
  expect(manifestAfter.workflowVersionId).toBe(v2.id)
  const historicalRun = await (await page.request.get(`http://127.0.0.1:8080/api/runs/${runID}`)).json() as { run: { workflowVersionId: string } }
  expect(historicalRun.run.workflowVersionId).toBe(v2.id)

  const moreActionsTrigger = page.getByText('更多操作', { exact: true })
  await page.getByRole('button', { name: '关闭工作台' }).click()
  await expect(moreActionsTrigger).toBeFocused()
  await page.getByTestId('node-template').click()
  await expect(page.getByLabel('模板', { exact: true })).toHaveValue('V1：{{topic}}')
  await openMoreActions(page)
  await page.getByRole('button', { name: '版本历史' }).click()
  await page.getByRole('button', { name: '撤销回滚' }).click()
  await expect(page.getByText('已撤销回滚')).toBeVisible()
  await page.getByRole('button', { name: '关闭工作台' }).click()
  await page.getByTestId('node-template').click()
  await expect(page.getByLabel('模板', { exact: true })).toHaveValue('Draft：{{topic}}')

  await openMoreActions(page)
  await page.getByRole('button', { name: '版本历史' }).click()
  await page.getByLabel('比较起点').selectOption('version:1')
  await page.getByRole('button', { name: '恢复 v1 为草稿' }).click()
  await page.getByRole('button', { name: '确认恢复' }).click()
  await expect(page.getByRole('button', { name: '撤销回滚' })).toBeVisible()
  await page.getByRole('button', { name: '关闭工作台' }).click()
  await page.getByTestId('node-template').click()
  await page.getByLabel('模板', { exact: true }).fill('已编辑：{{topic}}')
  await applyNodeConfig(page)
  await openMoreActions(page)
  await page.getByRole('button', { name: '版本历史' }).click()
  await expect(page.getByRole('button', { name: '撤销回滚' })).toHaveCount(0)

  for (const viewport of [{ width: 1440, height: 900 }, { width: 1024, height: 768 }, { width: 768, height: 900 }]) {
    await page.setViewportSize(viewport)
    const panel = page.getByRole('dialog', { name: '版本历史' })
    await expect(panel).toBeVisible()
    const box = await panel.boundingBox()
    if (!box) throw new Error('无法读取版本工作台尺寸')
    expect(box.x).toBeGreaterThanOrEqual(0)
    expect(box.x + box.width).toBeLessThanOrEqual(viewport.width)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  }
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

async function buildAndPublish(page: Page, name: string, slug: string, template: string, presentation?: AgentPresentationSettings) {
  const workflowURL = await createWorkflow(page, slug, name)
  await configureStartTextField(page, 'topic', '主题')

  await page.getByRole('button', { name: '添加节点' }).click()
  await page.getByRole('button', { name: '提示词模板' }).click()
  await placeNodePreview(page)
  await page.getByLabel('模板', { exact: true }).fill(template)
  await applyNodeConfig(page)
  await page.getByRole('button', { name: '添加节点' }).click()
  await page.getByRole('button', { name: /^LLM llm@1 / }).click()
  await placeNodePreview(page)
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

  if (presentation) await configureAgentPresentation(page, presentation)

  await page.getByRole('button', { name: '发布' }).click()
  await page.getByRole('button', { name: '确认发布' }).click()
  const agentLink = page.getByRole('link', { name: '打开 Agent 页面' })
  await expect(agentLink).toBeVisible()
  const agentURL = await agentLink.getAttribute('href')
  if (!agentURL) throw new Error('发布后未返回 Agent URL')
  return { agentURL, workflowURL }
}

async function placeNodePreview(page: Page) {
  await expect(page.getByText('点击画布放置，Esc 取消')).toBeVisible()
  const pane = page.locator('.react-flow__pane')
  const box = await pane.boundingBox()
  if (!box) throw new Error('无法读取画布放置区域')
  await pane.click({
    position: { x: box.width / 2, y: box.height * 0.72 },
    force: true,
  })
}

function versionGraph(startLabel: string, template: string) {
  return {
    schemaVersion: 1,
    nodes: [
      { id: 'start', type: 'start', typeVersion: '1', position: { x: 0, y: 0 }, config: { fields: [{ key: 'topic', label: startLabel, type: 'text', required: true }] } },
      { id: 'template', type: 'template', typeVersion: '1', position: { x: 300, y: 0 }, config: { template } },
      { id: 'end', type: 'end', typeVersion: '1', position: { x: 600, y: 0 }, config: {} },
    ],
    edges: [
      { id: 'start-template', source: 'start', sourcePort: 'topic', target: 'template', targetPort: 'topic' },
      { id: 'template-end', source: 'template', sourcePort: 'text', target: 'end', targetPort: 'result' },
    ],
  }
}
