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

	it('异步 Agent 运行客户端发送恢复协议所需信息', async () => {
		const controller = new AbortController()
		const summary = { runId: 'run-1', status: 'running' }
		const view = { run: summary, events: [], nextSequence: 8, hasMore: false }
		const fetchMock = vi.fn()
			.mockResolvedValueOnce(jsonResponse(summary))
			.mockResolvedValueOnce(jsonResponse(view))
			.mockResolvedValueOnce(jsonResponse({ ...summary, status: 'cancelling' }))
		vi.stubGlobal('fetch', fetchMock)

		const body = { workflowVersionId: 'v1', input: { topic: 'Agent' } }
		await api.startAgentRun('demo/agent', body, '123e4567-e89b-42d3-a456-426614174000', controller.signal)
		await api.getAgentRunView('demo/agent', 'run/1', 8, controller.signal)
		await api.cancelAgentRun('demo/agent', 'run/1', controller.signal)

		const startHeaders = fetchMock.mock.calls[0]?.[1]?.headers as Headers
		expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/agents/demo%2Fagent/runs', expect.objectContaining({
			method: 'POST', body: JSON.stringify(body), signal: controller.signal,
		}))
		expect(startHeaders.get('Prefer')).toBe('respond-async')
		expect(startHeaders.get('Idempotency-Key')).toBe('123e4567-e89b-42d3-a456-426614174000')
		expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/agents/demo%2Fagent/runs/run%2F1?afterSequence=8', expect.objectContaining({ signal: controller.signal }))
		expect(fetchMock).toHaveBeenNthCalledWith(3, '/api/agents/demo%2Fagent/runs/run%2F1/cancel', expect.objectContaining({ method: 'POST', signal: controller.signal }))
		expect(fetchMock.mock.calls[2]?.[1]?.body).toBeUndefined()
	})

	it('保存 Agent 页面设置使用独立 PUT 端点', async () => {
		const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ ...workflowFixture(), draftRevision: 5 }))
		vi.stubGlobal('fetch', fetchMock)
		const body = {
			draftRevision: 4,
			presentation: {
				title: '研究助手', description: '输入主题并生成结果', accent: 'teal' as const,
				submitLabel: '开始研究', resultMode: 'auto' as const,
			},
		}
		await api.saveAgentPresentation('w/1', body)
		expect(fetchMock).toHaveBeenCalledWith('/api/workflows/w%2F1/agent-presentation', expect.objectContaining({
			method: 'PUT', body: JSON.stringify(body),
		}))
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

	it('运行恢复客户端编码路径、幂等 Header 并保留流响应', async () => {
		const controller = new AbortController()
		const stream = new Response('{"type":"run.started"}\n', { status: 200 })
		const fetchMock = vi.fn()
			.mockResolvedValueOnce(jsonResponse({ id: 'run/1', status: 'cancelling' }))
			.mockResolvedValueOnce(jsonResponse({ source: {}, retryOfRunId: 'run/1', input: {}, inputRedactedPaths: [], inputSchema: {} }))
			.mockResolvedValueOnce(stream)
		vi.stubGlobal('fetch', fetchMock)

		await api.cancelRun('run/1', controller.signal)
		await api.previewRunRetry('run/1', controller.signal)
		await expect(api.retryRun('run/1', '33333333-3333-4333-8333-333333333333', { secretValues: { '/token': 'secret' } }, controller.signal)).resolves.toBe(stream)

		expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/runs/run%2F1/cancel', expect.objectContaining({ method: 'POST', signal: controller.signal }))
		expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/runs/run%2F1/retry-preview', expect.objectContaining({ signal: controller.signal }))
		expect(fetchMock).toHaveBeenNthCalledWith(3, '/api/runs/run%2F1/retries', expect.objectContaining({
			method: 'POST', signal: controller.signal,
			body: JSON.stringify({ secretValues: { '/token': 'secret' } }),
			headers: expect.any(Headers),
		}))
		const retryHeaders = fetchMock.mock.calls[2]?.[1]?.headers as Headers
		expect(retryHeaders.get('Idempotency-Key')).toBe('33333333-3333-4333-8333-333333333333')
		expect(String(fetchMock.mock.calls[2]?.[0])).not.toContain('33333333-3333-4333-8333-333333333333')
		expect(String(fetchMock.mock.calls[2]?.[1]?.body)).not.toContain('33333333-3333-4333-8333-333333333333')
	})

	it('人工恢复客户端携带 attempt 和乐观并发序号', async () => {
		const controller = new AbortController()
		const fetchMock = vi.fn()
			.mockResolvedValueOnce(jsonResponse({ runId: 'run/1', status: 'recovery_required', reason: 'uncertain_side_effect', sequence: 9, nodes: [] }))
			.mockResolvedValueOnce(jsonResponse({ id: 'run/1', status: 'queued' }))
			.mockResolvedValueOnce(jsonResponse({ id: 'run/1', status: 'cancelled' }))
		vi.stubGlobal('fetch', fetchMock)

		await api.getRunRecovery('run/1', controller.signal)
		await api.confirmRunNodeRetry('run/1', 'node/1', 2, 9, controller.signal)
		await api.terminateRunRecovery('run/1', 10, controller.signal)

		expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/runs/run%2F1/recovery', expect.objectContaining({ signal: controller.signal }))
		expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/runs/run%2F1/recovery/nodes/node%2F1/retry', expect.objectContaining({
			method: 'POST', signal: controller.signal, body: JSON.stringify({ nodeAttempt: 2, expectedSequence: 9 }),
		}))
		expect(fetchMock).toHaveBeenNthCalledWith(3, '/api/runs/run%2F1/recovery/terminate', expect.objectContaining({
			method: 'POST', signal: controller.signal, body: JSON.stringify({ expectedSequence: 10 }),
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

	it('版本治理客户端编码分页和严格 mutation 请求', async () => {
		const controller = new AbortController()
		const workflow = workflowFixture()
		const checkpoint = {
			sourceRevision: 7, restoredRevision: 8, restoredFromVersion: 1,
			createdAt: '2026-08-27T10:05:00Z',
		}
		const fetchMock = vi.fn()
			.mockResolvedValueOnce(jsonResponse({ items: [], nextCursor: null, rollbackCheckpoint: null }))
			.mockResolvedValueOnce(jsonResponse({
				base: { kind: 'version', version: 1, versionId: '11111111-1111-4111-8111-111111111111', createdAt: '2026-08-27T10:00:00Z' },
				compare: { kind: 'draft', draftRevision: 7 },
				summary: { total: 0, nodes: 0, startParameters: 0, connections: 0, agentPresentation: 0, layout: 0 },
				truncated: false,
				groups: { nodes: [], startParameters: [], connections: [], agentPresentation: [], layout: [] },
			}))
			.mockResolvedValueOnce(jsonResponse({ workflow, rollbackCheckpoint: checkpoint }))
			.mockResolvedValueOnce(jsonResponse(workflow))
		vi.stubGlobal('fetch', fetchMock)

		await api.listWorkflowVersions('w/1', { limit: 20, cursor: 'next cursor' }, controller.signal)
		await api.diffWorkflowVersions('w/1', {
			base: { kind: 'version', version: 1 },
			compare: { kind: 'draft', draftRevision: 7 },
		}, controller.signal)
		await api.rollbackWorkflow('w/1', { targetVersion: 1, expectedDraftRevision: 7 }, controller.signal)
		await api.undoWorkflowRollback('w/1', { expectedDraftRevision: 8 }, controller.signal)

		expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/workflows/w%2F1/versions?limit=20&cursor=next+cursor', expect.objectContaining({ signal: controller.signal }))
		expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/workflows/w%2F1/version-diffs', expect.objectContaining({
			method: 'POST', signal: controller.signal,
			body: JSON.stringify({ base: { kind: 'version', version: 1 }, compare: { kind: 'draft', draftRevision: 7 } }),
		}))
		expect(fetchMock).toHaveBeenNthCalledWith(3, '/api/workflows/w%2F1/rollbacks', expect.objectContaining({
			method: 'POST', signal: controller.signal,
			body: JSON.stringify({ targetVersion: 1, expectedDraftRevision: 7 }),
		}))
		expect(fetchMock).toHaveBeenNthCalledWith(4, '/api/workflows/w%2F1/rollback-undo', expect.objectContaining({
			method: 'POST', signal: controller.signal,
			body: JSON.stringify({ expectedDraftRevision: 8 }),
		}))
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
	agentPresentation: { title: '副本', description: '', accent: 'indigo', submitLabel: '运行 Agent', resultMode: 'auto' },
	draftGraph: { schemaVersion: 1, nodes: [], edges: [] },
	createdAt: '2026-08-19T00:00:00Z', updatedAt: '2026-08-19T00:00:00Z',
})
