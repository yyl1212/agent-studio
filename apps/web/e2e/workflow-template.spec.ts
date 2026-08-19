import fs from 'node:fs'

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
  const exported = JSON.parse(await fs.promises.readFile(path, 'utf8'))
  expect(exported.apiVersion).toBe('agent-studio.dev/v1alpha2')
  expect(exported.spec.nodePackages).toEqual([
    expect.objectContaining({
      name: 'github.com/yyl1212/agent-studio',
      nodes: [{ type: 'extension.echo', version: '1.0.0' }],
    }),
  ])

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

test('缺失节点包只显示提示且不会自动安装', async ({ page }) => {
  let previewRequests = 0
  let unexpectedInstallRequests = 0
  page.on('request', (request) => {
    if (/install|package.*download|go-get/i.test(request.url())) unexpectedInstallRequests += 1
  })
  await page.route('**/api/workflow-templates/preview', async (route) => {
    previewRequests += 1
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        valid: false,
        metadata: { name: '缺失包模板', description: '' },
        summary: {
          nodeCount: 3,
          edgeCount: 2,
          inputSchema: { type: 'object', properties: {}, required: [] },
          nodeTypes: [{
            type: 'example.missing', version: '1.0.0', title: 'Missing', count: 1,
            available: false, capabilities: [],
          }],
        },
        issues: [{
          code: 'NODE_TYPE_NOT_FOUND', message: '节点类型或版本未注册', nodeId: 'missing',
          packageName: 'github.com/example/missing', packageVersion: 'v1.2.3',
        }],
      }),
    })
  })

  const template = {
    apiVersion: 'agent-studio.dev/v1alpha2',
    kind: 'WorkflowTemplate',
    metadata: { name: '缺失包模板', description: '' },
    spec: {
      nodePackages: [{
        name: 'github.com/example/missing', version: 'v1.2.3',
        nodes: [{ type: 'example.missing', version: '1.0.0' }],
      }],
      graph: { schemaVersion: 1, nodes: [], edges: [] },
    },
  }

  await page.goto('/workflows')
  await page.getByRole('button', { name: '导入模板' }).click()
  await page.getByLabel('选择模板文件').setInputFiles({
    name: 'missing-package.workflow.json',
    mimeType: 'application/json',
    buffer: Buffer.from(JSON.stringify(template)),
  })

  await expect(page.getByText('需要节点包 github.com/example/missing · 导出环境 v1.2.3 · 节点类型或版本未注册')).toBeVisible()
  await expect(page.getByRole('button', { name: '导入并打开' })).toBeDisabled()
  expect(previewRequests).toBe(1)
  expect(unexpectedInstallRequests).toBe(0)
})
