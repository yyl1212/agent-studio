import { expect, test } from '@playwright/test'

import { applyNodeConfig, configureStartField, configureStartTextField, connectPorts, createWorkflow, openOptionalConfig } from './helpers'

test('扩展 Echo 无需前端专用组件即可运行', async ({ page }) => {
  const workflowURL = await createWorkflow(page, `sdk-echo-${Date.now().toString(36)}`)
  await page.goto(workflowURL)
  await configureStartTextField(page, 'topic', '主题')
  await page.getByRole('button', { name: '添加节点' }).click()
  await expect(page.getByRole('button', { name: /Echo.*Agent Studio 官方扩展节点/ })).toBeVisible()
  await page.getByRole('button', { name: 'Echo' }).click()
  await openOptionalConfig(page)
  await page.getByLabel('前缀').fill('回答：')
  await applyNodeConfig(page)
  await page.getByRole('button', { name: '关闭工作台' }).click()
  await connectPorts(page, [
    ['start', 'topic', 'extension.echo', 'text'],
    ['extension.echo', 'text', 'end', 'result'],
  ])
  await page.getByRole('button', { name: '测试运行' }).click()
  await page.getByLabel('主题', { exact: true }).fill('SDK')
  await page.getByRole('button', { name: '运行', exact: true }).click()
  await expect(page.getByText('回答：SDK')).toBeVisible()
})

test('Retriever 通过通用对象数组表单保存并确定性运行', async ({ page }) => {
  const workflowURL = await createWorkflow(page, `sdk-retriever-${Date.now().toString(36)}`)
  await page.goto(workflowURL)
  await configureStartTextField(page, 'query', '查询')
  await page.getByRole('button', { name: '添加节点' }).click()
  await page.getByRole('button', { name: 'Retriever' }).click()
  await page.getByRole('button', { name: '添加一项' }).click()
  await page.getByLabel('文档标识').fill('doc-1')
  await page.getByLabel('文档内容').fill('Agent Go')
  await page.getByLabel('返回数量').fill('1')
  await applyNodeConfig(page)
  await page.getByRole('button', { name: '关闭工作台' }).click()
  await connectPorts(page, [
    ['start', 'query', 'extension.retriever', 'query'],
    ['extension.retriever', 'matches', 'end', 'result'],
  ])
  await page.getByRole('button', { name: '测试运行' }).click()
  await page.getByLabel('查询', { exact: true }).fill('agent go')
  await page.getByRole('button', { name: '运行', exact: true }).click()
  await expect(page.locator('.run-output')).toContainText('doc-1')
  await expect(page.locator('.run-output')).toContainText('"score": 1')
})

test('Webhook 只显示安全公共错误且配置不能选择主机', async ({ page }) => {
  const workflowURL = await createWorkflow(page, `sdk-webhook-${Date.now().toString(36)}`)
  await page.goto(workflowURL)
  await configureStartField(page, 'payload', '请求体', 'json')
  await page.getByRole('button', { name: '添加节点' }).click()
  await page.getByRole('button', { name: 'Webhook' }).click()
  await expect(page.getByLabel('相对路径')).toBeVisible()
  await expect(page.getByLabel(/URL|Token|Header/i)).toHaveCount(0)
  await page.getByLabel('相对路径').fill('e2e-webhook-rejected')
  await openOptionalConfig(page)
  await page.getByLabel('超时毫秒').fill('5000')
  await applyNodeConfig(page)
  await page.getByRole('button', { name: '关闭工作台' }).click()
  await connectPorts(page, [
    ['start', 'payload', 'extension.webhook', 'body'],
    ['extension.webhook', 'body', 'end', 'result'],
  ])
  await page.getByRole('button', { name: '测试运行' }).click()
  await page.getByLabel('请求体', { exact: true }).fill('{"hello":"world"}')
  await page.getByRole('button', { name: '运行', exact: true }).click()
  await expect(page.getByRole('alert')).toHaveText('节点输入无效')
  await expect(page.locator('body')).not.toContainText('e2e-webhook-secret')
  await expect(page.locator('body')).not.toContainText('http://127.0.0.1:8080')
})
