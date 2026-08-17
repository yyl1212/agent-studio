import { afterEach, describe, expect, it, vi } from 'vitest'

import { APIError, api } from './client'

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
})
