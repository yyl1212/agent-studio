import { afterEach, describe, expect, it, vi } from 'vitest'

import { APIError, api, parseWorkflowTemplateJSON, type Workflow, type WorkflowTemplate, type WorkflowTemplatePreview } from './client'

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

	it('错误详情只保留规范 UUID runId', async () => {
		const fetchMock = vi.fn()
			.mockResolvedValueOnce(new Response(JSON.stringify({
				code: 'RUN_RETRY_ALREADY_CREATED', message: '已创建',
				details: { runId: '55555555-5555-4555-8555-555555555555', secret: 'must-drop' },
			}), { status: 409, headers: { 'Content-Type': 'application/json' } }))
			.mockResolvedValueOnce(new Response(JSON.stringify({
				code: 'RUN_RETRY_ALREADY_CREATED', message: '已创建', details: { runId: 'not-a-uuid', secret: 'must-drop' },
			}), { status: 409, headers: { 'Content-Type': 'application/json' } }))
		vi.stubGlobal('fetch', fetchMock)

		await expect(api.previewRunRetry('run/1')).rejects.toEqual(expect.objectContaining<Partial<APIError>>({
			details: { runId: '55555555-5555-4555-8555-555555555555' },
		}))
		try {
			await api.previewRunRetry('run/1')
		} catch (error) {
			expect((error as APIError).details).toBeUndefined()
			expect(JSON.stringify(error)).not.toContain('must-drop')
		}
	})

  it('运行接口保留原始流式 Response', async () => {
    const response = new Response('{"type":"run.started"}\n', { status: 200 })
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response))
    await expect(api.runAgent('demo', { workflowVersionId: 'v1', input: {} })).resolves.toBe(response)
  })

	it('调试接口编码路径、使用独占游标并透传 AbortSignal', async () => {
		const controller = new AbortController()
		const stream = new Response('{"type":"run.started"}\n', { status: 200 })
		const fetchMock = vi.fn()
			.mockResolvedValueOnce(jsonResponse({ run: {}, graph: {}, nodeRuns: [], sourceChain: [], replayAvailable: true, rerunAvailable: true }))
			.mockResolvedValueOnce(jsonResponse({ events: [], nextAfterSequence: 7 }))
			.mockResolvedValueOnce(jsonResponse({ sourceRunId: 'run/1', sourceNodeId: 'node/1', entryInput: {}, entryInputRedactedPaths: [], activeNodes: [], frozenEdges: [], effectiveSafety: 'pure', requiresConfirmation: false }))
			.mockResolvedValueOnce(stream)
		vi.stubGlobal('fetch', fetchMock)

		await api.getDebugOverview('run/1', controller.signal)
		await api.listRunEvents('run/1', 7, controller.signal)
		await api.previewRerun('run/1', 'node/1', controller.signal)
		await expect(api.rerunFromNode('run/1', 'node/1', { entryInput: { in: ['edited'] }, confirmSideEffects: true }, controller.signal)).resolves.toBe(stream)

		expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/runs/run%2F1/debug', expect.objectContaining({ signal: controller.signal }))
		expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/runs/run%2F1/events?afterSequence=7', expect.objectContaining({ signal: controller.signal }))
		expect(fetchMock).toHaveBeenNthCalledWith(3, '/api/runs/run%2F1/nodes/node%2F1/rerun-preview', expect.objectContaining({ signal: controller.signal }))
		expect(fetchMock).toHaveBeenNthCalledWith(4, '/api/runs/run%2F1/nodes/node%2F1/reruns', expect.objectContaining({
			method: 'POST', signal: controller.signal,
			body: JSON.stringify({ entryInput: { in: ['edited'] }, confirmSideEffects: true }),
		}))
	})

	it('编码节点包筛选和包含斜杠的模块名', async () => {
		const fetchMock = vi.fn()
			.mockResolvedValueOnce(jsonResponse({ release: 'v0.3.0', total: 0, offset: 40, limit: 20, items: [] }))
			.mockResolvedValueOnce(jsonResponse({
				name: 'github.com/example/nodes', categories: [], keywords: [], versions: [],
				recommendedVersion: null, reasons: [], assessments: [],
			}))
		vi.stubGlobal('fetch', fetchMock)

		await api.listNodePackages({ q: '向量 search', categories: ['integration', 'file'], compatible: false, limit: 20, offset: 40 })
		await api.getNodePackage('github.com/example/nodes')

		expect(String(fetchMock.mock.calls[0]?.[0])).toBe('/api/node-packages?q=%E5%90%91%E9%87%8F+search&category=integration&category=file&compatible=false&limit=20&offset=40')
		expect(String(fetchMock.mock.calls[1]?.[0])).toBe('/api/node-package?name=github.com%2Fexample%2Fnodes')
	})

	it('获取节点索引状态并透传 AbortSignal', async () => {
		const controller = new AbortController()
		const fetchMock = vi.fn()
			.mockResolvedValueOnce(jsonResponse({
				source: 'embedded', release: 'v0.3.0', generatedAt: '2026-08-20T00:00:00Z',
				packageCount: 0, compatiblePackageCount: 0, runtimeVersion: 'v0.3.0',
				nodeAPI: 'agent-studio.dev/v1alpha1', stale: true, warningCode: 'INDEX_EMBEDDED_SNAPSHOT',
			}))
			.mockResolvedValueOnce(jsonResponse({ release: 'v0.3.0', total: 0, offset: 0, limit: 50, items: [] }))
		vi.stubGlobal('fetch', fetchMock)

		await api.getNodeIndexStatus(controller.signal)
		await api.listNodePackages({ q: '', categories: [], compatible: true, limit: 50, offset: 0 }, controller.signal)

		expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/node-index/status', expect.objectContaining({ signal: controller.signal }))
		expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/node-packages?compatible=true&limit=50&offset=0', expect.objectContaining({ signal: controller.signal }))
	})

	it('管理接口使用稳定查询、编码路径并透传 AbortSignal', async () => {
		const controller = new AbortController()
		const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(jsonResponse({ items: [], nextCursor: null })))
		vi.stubGlobal('fetch', fetchMock)

		await api.listWorkflowSummaries({ q: 'Agent', state: 'all', limit: 50, cursor: 'next' }, controller.signal)
		await api.updateWorkflow('w/1', { name: '新名称', description: '说明' }, controller.signal)
		await api.copyWorkflow('w/1', { name: '副本', slug: 'copy' }, controller.signal)
		await api.archiveWorkflow('w/1', controller.signal)
		await api.restoreWorkflow('w/1', controller.signal)
		await api.listRunSummaries({
			workflowId: 'w1', statuses: ['failed', 'running'], modes: ['test'],
			startedAfter: '2026-08-01T00:00:00Z', startedBefore: '2026-08-25T00:00:00Z', limit: 50,
		}, controller.signal)

		expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/workflow-summaries?q=Agent&state=all&cursor=next&limit=50', expect.objectContaining({ signal: controller.signal }))
		expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/workflows/w%2F1', expect.objectContaining({ method: 'PATCH', body: JSON.stringify({ name: '新名称', description: '说明' }), signal: controller.signal }))
		expect(fetchMock).toHaveBeenNthCalledWith(3, '/api/workflows/w%2F1/copies', expect.objectContaining({ method: 'POST', body: JSON.stringify({ name: '副本', slug: 'copy' }), signal: controller.signal }))
		expect(fetchMock).toHaveBeenNthCalledWith(4, '/api/workflows/w%2F1/archive', expect.objectContaining({ method: 'POST', signal: controller.signal }))
		expect(fetchMock).toHaveBeenNthCalledWith(5, '/api/workflows/w%2F1/restore', expect.objectContaining({ method: 'POST', signal: controller.signal }))
		expect(String(fetchMock.mock.calls[5]?.[0])).toBe('/api/runs?workflowId=w1&status=failed&status=running&mode=test&startedAfter=2026-08-01T00%3A00%3A00Z&startedBefore=2026-08-25T00%3A00%3A00Z&limit=50')
		expect(fetchMock.mock.calls[5]?.[1]).toEqual(expect.objectContaining({ signal: controller.signal }))
		for (const [path] of fetchMock.mock.calls) expect(String(path)).not.toMatch(/\?$/)
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

	it('按草稿 revision 下载模板 Blob', async () => {
		const response = new Response('{"kind":"WorkflowTemplate"}\n', {
			status: 200,
			headers: { 'Content-Type': 'application/json' },
		})
		const fetchMock = vi.fn().mockResolvedValue(response)
		vi.stubGlobal('fetch', fetchMock)
		const blob = await api.exportWorkflowTemplate('w/1', 7)
		expect(await blob.text()).toContain('WorkflowTemplate')
		expect(fetchMock).toHaveBeenCalledWith('/api/workflows/w%2F1/template?draftRevision=7', expect.objectContaining({ signal: undefined }))
	})

	it('模板下载非 2xx 时抛出 APIError', async () => {
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
			code: 'WORKFLOW_TEMPLATE_INVALID', message: '工作流模板校验失败',
		}), { status: 422, headers: { 'Content-Type': 'application/json' } })))
		await expect(api.exportWorkflowTemplate('w1', 2)).rejects.toEqual(
			expect.objectContaining<Partial<APIError>>({ status: 422, code: 'WORKFLOW_TEMPLATE_INVALID' }),
		)
	})

	it('预览和导入请求保留模板中的大整数原文', async () => {
		const raw = '{"apiVersion":"agent-studio.dev/v1alpha2","kind":"WorkflowTemplate","metadata":{"name":"大整数","description":""},"spec":{"nodePackages":[],"graph":{"schemaVersion":1,"nodes":[{"id":"n","type":"custom","typeVersion":"1","position":{"x":0,"y":0},"config":{"value":9007199254740993,"exponent":1e400}}],"edges":[]}}}'
		const template = parseWorkflowTemplateJSON(raw)
		const fetchMock = vi.fn()
			.mockResolvedValueOnce(new Response(JSON.stringify(previewFixture()), { status: 200, headers: { 'Content-Type': 'application/json' } }))
			.mockResolvedValueOnce(new Response(JSON.stringify(workflowFixture()), { status: 201, headers: { 'Content-Type': 'application/json' } }))
		vi.stubGlobal('fetch', fetchMock)
		await api.previewWorkflowTemplate(template)
		await api.importWorkflowTemplate({ template, name: '副本', slug: 'copy', description: '' })
		expect(fetchMock.mock.calls[0]?.[1]?.body).toContain('9007199254740993')
		expect(fetchMock.mock.calls[1]?.[1]?.body).toContain('9007199254740993')
		expect(fetchMock.mock.calls[0]?.[1]?.body).toContain('1e400')
		expect(fetchMock.mock.calls[1]?.[1]?.body).toContain('1e400')
		expect(fetchMock.mock.calls[0]?.[1]?.body).not.toContain('Infinity')
		expect(fetchMock.mock.calls[0]?.[1]?.body).not.toContain('9007199254740992')
	})
})

const jsonResponse = (value: unknown) => new Response(JSON.stringify(value), {
	status: 200,
	headers: { 'Content-Type': 'application/json' },
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
