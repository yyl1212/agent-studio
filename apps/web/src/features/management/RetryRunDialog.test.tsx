import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { APIError, api, type RunRetryPreview } from '../../lib/api/client'
import { RetryRunDialog } from './RetryRunDialog'

describe('RetryRunDialog', () => {
  afterEach(() => vi.restoreAllMocks())

  it('只提交预览秘密路径，失败保留秘密和幂等键，成功后清空并切换运行', async () => {
    vi.spyOn(api, 'previewRunRetry').mockResolvedValue(preview())
    const retry = vi.spyOn(api, 'retryRun')
      .mockRejectedValueOnce(new APIError(500, 'INTERNAL_ERROR', '暂时失败', 'req-retry'))
      .mockResolvedValueOnce(new Response('{"sequence":1,"type":"run.started","runId":"22222222-2222-4222-8222-222222222222"}\n'))
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('33333333-3333-4333-8333-333333333333')
    const onRetryCreated = vi.fn()
    render(<RetryRunDialog sourceRunID="11111111-1111-4111-8111-111111111111" onRequestClose={vi.fn()} onRetryCreated={onRetryCreated} />)

    const secret = await screen.findByLabelText('令牌')
    expect(secret).toHaveFocus()
    expect(secret).toHaveValue('')
    expect(screen.getByLabelText('主题')).toHaveValue('历史主题')
    expect(screen.getByLabelText('主题')).toHaveAttribute('readonly')
    await userEvent.type(secret, 'retry-secret-value')
    await userEvent.click(screen.getByRole('button', { name: '重新运行' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('req-retry')
    expect(secret).toHaveValue('retry-secret-value')
    await userEvent.click(screen.getByRole('button', { name: '重新运行' }))

    await waitFor(() => expect(onRetryCreated).toHaveBeenCalledWith('22222222-2222-4222-8222-222222222222'))
    expect(retry).toHaveBeenCalledTimes(2)
    expect(retry.mock.calls[0]?.[1]).toBe('33333333-3333-4333-8333-333333333333')
    expect(retry.mock.calls[1]?.[1]).toBe('33333333-3333-4333-8333-333333333333')
    expect(retry.mock.calls[1]?.[2]).toEqual({ secretValues: { '/credentials/token': 'retry-secret-value' } })
    expect(JSON.stringify(retry.mock.calls[1]?.[2])).not.toContain('历史主题')
  })

  it('409 已创建只采用白名单 runId', async () => {
    vi.spyOn(api, 'previewRunRetry').mockResolvedValue(preview())
    vi.spyOn(api, 'retryRun').mockRejectedValue(new APIError(409, 'RUN_RETRY_ALREADY_CREATED', '已创建', undefined, undefined, { runId: '44444444-4444-4444-8444-444444444444' }))
    const onRetryCreated = vi.fn()
    render(<RetryRunDialog sourceRunID="11111111-1111-4111-8111-111111111111" onRequestClose={vi.fn()} onRetryCreated={onRetryCreated} />)
    await userEvent.type(await screen.findByLabelText('令牌'), 'retry-secret-value')
    await userEvent.click(screen.getByRole('button', { name: '重新运行' }))
    await waitFor(() => expect(onRetryCreated).toHaveBeenCalledWith('44444444-4444-4444-8444-444444444444'))
  })

  it('收到 run.started 后关闭对话框不取消新运行的流', async () => {
    vi.spyOn(api, 'previewRunRetry').mockResolvedValue(preview())
    let requestSignal: AbortSignal | undefined
    vi.spyOn(api, 'retryRun').mockImplementation(async (_runID, _key, _body, signal) => {
      requestSignal = signal
      return new Response(new ReadableStream({
        start(controller) {
          controller.enqueue(new TextEncoder().encode('{"sequence":1,"type":"run.started","runId":"55555555-5555-4555-8555-555555555555"}\n'))
        },
      }))
    })
    const onRetryCreated = vi.fn()
    const rendered = render(<RetryRunDialog sourceRunID="11111111-1111-4111-8111-111111111111" onRequestClose={() => rendered.unmount()} onRetryCreated={onRetryCreated} />)
    await userEvent.type(await screen.findByLabelText('令牌'), 'retry-secret-value')
    await userEvent.click(screen.getByRole('button', { name: '重新运行' }))

    await waitFor(() => expect(onRetryCreated).toHaveBeenCalledWith('55555555-5555-4555-8555-555555555555'))
    expect(requestSignal?.aborted).toBe(false)
  })
})

function preview(): RunRetryPreview {
  return {
    source: { id: '11111111-1111-4111-8111-111111111111', workflowId: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', workflowName: '演示', workflowSlug: 'demo', mode: 'test', status: 'failed', startedAt: '2026-08-26T00:00:00Z' },
    retryOfRunId: '11111111-1111-4111-8111-111111111111',
    input: { topic: '历史主题', credentials: { token: '[REDACTED]' } },
    inputRedactedPaths: ['/credentials/token'],
    inputSchema: { type: 'object', properties: {
      topic: { type: 'string', title: '主题', default: '新默认' },
      credentials: { type: 'object', title: '凭据', properties: { token: { type: 'string', title: '令牌' } } },
    } },
  }
}
