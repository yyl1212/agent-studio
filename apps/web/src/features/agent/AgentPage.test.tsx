import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { APIError, api } from '../../lib/api/client'
import { AgentPage } from './AgentPage'

vi.mock('../../lib/api/client', async (importOriginal) => {
  const original = await importOriginal<typeof import('../../lib/api/client')>()
  return { ...original, api: { ...original.api } }
})

const inputSchema = { $schema: 'https://json-schema.org/draft/2020-12/schema', type: 'object', properties: { topic: { type: 'string', title: '主题' } }, required: ['topic'] }

function ndjsonResponse(events: unknown[]) {
  return new Response(events.map((event) => JSON.stringify(event)).join('\n') + '\n', { status: 200 })
}

function renderPage() {
  return render(<MemoryRouter initialEntries={['/agents/demo']}><Routes><Route path="/agents/:slug" element={<AgentPage />} /></Routes></MemoryRouter>)
}

describe('AgentPage', () => {
  afterEach(() => vi.restoreAllMocks())

  beforeEach(() => {
    vi.spyOn(api, 'getAgentManifest').mockResolvedValue({
      workflowVersionId: 'version-1', version: 1, title: '知识助手', description: '回答问题', inputSchema,
      presentation: { title: '知识助手', description: '回答问题', accent: 'indigo', submitLabel: '运行 Agent', resultMode: 'auto' },
    })
  })

  it('运行时回传页面加载时的 workflowVersionId 并安全显示文本', async () => {
    vi.spyOn(api, 'runAgent').mockResolvedValue(ndjsonResponse([
      { sequence: 1, type: 'run.started', runId: 'r1', timestamp: '2026-08-17T00:00:00Z' },
      { sequence: 2, type: 'run.completed', runId: 'r1', output: '<script>ok</script>', timestamp: '2026-08-17T00:00:01Z' },
    ]))
    renderPage()
    fireEvent.change(await screen.findByLabelText('主题'), { target: { value: 'Agent' } })
    await userEvent.click(screen.getByRole('button', { name: '运行 Agent' }))
    expect(api.runAgent).toHaveBeenCalledWith('demo', { workflowVersionId: 'version-1', input: { topic: 'Agent' } }, expect.any(AbortSignal))
    expect(await screen.findByText('<script>ok</script>')).toBeInTheDocument()
    expect(document.querySelector('script')).toBeNull()
  })

  it('显示稳定 API 错误且不泄漏其他内容', async () => {
    vi.spyOn(api, 'runAgent').mockRejectedValue(new APIError(400, 'REQUEST_INVALID', '请求内容无效', 'req-1'))
    renderPage()
    fireEvent.change(await screen.findByLabelText('主题'), { target: { value: 'Agent' } })
    await userEvent.click(screen.getByRole('button', { name: '运行 Agent' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('请求内容无效（请求 ID：req-1）')
  })

  it('显示安全的服务端错误和格式化 JSON 输出', async () => {
    const runAgent = vi.spyOn(api, 'runAgent')
    runAgent.mockRejectedValueOnce(new APIError(500, 'INTERNAL_ERROR', '内部错误', 'req-500'))
    renderPage()
    fireEvent.change(await screen.findByLabelText('主题'), { target: { value: 'Agent' } })
    await userEvent.click(screen.getByRole('button', { name: '运行 Agent' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('内部错误（请求 ID：req-500）')

    runAgent.mockResolvedValueOnce(ndjsonResponse([
      { sequence: 1, type: 'run.completed', runId: 'r2', output: { answer: 'ok' }, timestamp: '2026-08-17T00:00:01Z' },
    ]))
    await userEvent.click(screen.getByRole('button', { name: '运行 Agent' }))
    expect(await screen.findByRole('region', { name: '运行结果' })).toHaveTextContent('"answer": "ok"')
  })

  it('可取消正在进行的运行', async () => {
    vi.spyOn(api, 'runAgent').mockImplementation((_slug, _body, signal) => new Promise((_resolve, reject) => {
      signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
    }))
    renderPage()
    fireEvent.change(await screen.findByLabelText('主题'), { target: { value: 'Agent' } })
    await userEvent.click(screen.getByRole('button', { name: '运行 Agent' }))
    await userEvent.click(await screen.findByRole('button', { name: '取消运行' }))
    await vi.waitFor(() => expect(screen.queryByRole('button', { name: '取消运行' })).not.toBeInTheDocument())
  })

  it('Agent 已归档时显示安全提示', async () => {
    vi.mocked(api.getAgentManifest).mockRejectedValueOnce(new APIError(409, 'WORKFLOW_ARCHIVED', '内部归档错误', 'req-archive'))
    renderPage()
    expect(await screen.findByRole('alert')).toHaveTextContent('该 Agent 已归档，暂时不能运行')
    expect(screen.getByRole('alert')).not.toHaveTextContent('内部归档错误')
  })

  it('运行期间发现 Agent 已归档时显示安全提示', async () => {
    vi.spyOn(api, 'runAgent').mockRejectedValue(new APIError(409, 'WORKFLOW_ARCHIVED', '内部归档错误', 'req-archive'))
    renderPage()
    fireEvent.change(await screen.findByLabelText('主题'), { target: { value: 'Agent' } })
    await userEvent.click(screen.getByRole('button', { name: '运行 Agent' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('该 Agent 已归档，暂时不能运行')
    expect(screen.getByRole('alert')).not.toHaveTextContent('内部归档错误')
  })
})
