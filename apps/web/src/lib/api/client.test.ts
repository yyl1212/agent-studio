import { afterEach, describe, expect, it, vi } from 'vitest'

import { APIError, api, type Workflow, type WorkflowTemplate, type WorkflowTemplatePreview } from './client'

describe('API client', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('将稳定错误响应转换为 APIError', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ code: 'WORKFLOW_SLUG_CONFLICT', message: 'Agent 地址标识已存在', requestId: 'req-1' }), {
          status: 409,
          headers: { 'Content-Type': 'application/json' },
        }),
      ),
    )
    await expect(api.createWorkflow({ name: 'Demo', slug: 'demo', description: '' })).rejects.toEqual(
      expect.objectContaining<Partial<APIError>>({ status: 409, code: 'WORKFLOW_SLUG_CONFLICT', requestId: 'req-1' }),
    )
  })

  it('运行接口保留原始流式 Response', async () => {
    const response = new Response('{"type":"run.started"}\n', { status: 200 })
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response))
    await expect(api.runAgent('demo', { workflowVersionId: 'v1', input: {} })).resolves.toBe(response)
  })

	 it('预览并导入工作流模板', async () => {
		const template = templateFixture()
		const fetchMock = vi.fn()
			.mockResolvedValueOnce(new Response(JSON.stringify(previewFixture()), { status: 200, headers: { 'Content-Type': 'application/json' } }))
			.mockResolvedValueOnce(new Response(JSON.stringify(workflowFixture()), { status: 201, headers: { 'Content-Type': 'application/json' } }))
		vi.stubGlobal('fetch', fetchMock)
		await api.previewWorkflowTemplate(template)
		await api.importWorkflowTemplate({ template, name: '副本', slug: 'copy', description: '' })
		expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/workflow-templates/preview', expect.objectContaining({ method: 'POST', body: JSON.stringify({ template }) }))
		expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/workflow-templates/import', expect.objectContaining({ method: 'POST', body: JSON.stringify({ template, name: '副本', slug: 'copy', description: '' }) }))
	})

	it('保留模板导入 422 的问题列表', async () => {
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
			code: 'WORKFLOW_TEMPLATE_INVALID', message: '工作流模板校验失败',
			issues: [{ code: 'NODE_TYPE_NOT_FOUND', message: '节点类型或版本未注册', nodeId: 'missing' }],
		}), { status: 422, headers: { 'Content-Type': 'application/json' } })))
		await expect(api.importWorkflowTemplate({ template: templateFixture(), name: '副本', slug: 'copy', description: '' })).rejects.toEqual(
			expect.objectContaining<Partial<APIError>>({
				status: 422,
				code: 'WORKFLOW_TEMPLATE_INVALID',
				issues: [expect.objectContaining({ code: 'NODE_TYPE_NOT_FOUND', nodeId: 'missing' })],
			}),
		)
	})
})

const templateFixture = (): WorkflowTemplate => ({
	apiVersion: 'agent-studio.dev/v1alpha1', kind: 'WorkflowTemplate',
	metadata: { name: '演示模板', description: '前端测试' },
	spec: { graph: { schemaVersion: 1, nodes: [], edges: [] } },
})

const previewFixture = (): WorkflowTemplatePreview => ({
	valid: true,
	metadata: templateFixture().metadata,
	summary: {
		nodeCount: 3,
		edgeCount: 2,
		inputSchema: { type: 'object', properties: { topic: { type: 'string', title: '主题' } }, required: ['topic'] },
		nodeTypes: [{ type: 'extension.echo', version: '1.0.0', title: 'Echo', count: 1, available: true, capabilities: [] }],
	},
	issues: [],
})

const workflowFixture = (): Workflow => ({
	id: 'w-copy', name: '副本', slug: 'copy', description: '', draftRevision: 1,
	draftGraph: { schemaVersion: 1, nodes: [], edges: [] },
	createdAt: '2026-08-19T00:00:00Z', updatedAt: '2026-08-19T00:00:00Z',
})
