import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { APIError, api, type RunRecoveryView, type RunSummary } from '../../lib/api/client'
import { RunRecoveryDialog } from './RunRecoveryDialog'

describe('RunRecoveryDialog', () => {
  afterEach(() => vi.restoreAllMocks())

  it('展示 attempt、安全等级和副作用风险，并阻止重复提交', async () => {
    vi.spyOn(api, 'getRunRecovery').mockResolvedValue(recoveryView())
    const confirmation = deferred<RunSummary>()
    const confirm = vi.spyOn(api, 'confirmRunNodeRetry').mockReturnValue(confirmation.promise)
    const onRecovered = vi.fn()
    render(<RunRecoveryDialog runID="run-1" open onClose={vi.fn()} onRecovered={onRecovered} />)

    expect(await screen.findByText('外部副作用状态不确定')).toBeInTheDocument()
    expect(screen.getByText('Attempt').nextSibling).toHaveTextContent('2')
    expect(screen.getByText('副作用')).toBeInTheDocument()
    expect(screen.getAllByText(/可能重复调用外部服务/).length).toBeGreaterThan(0)
    const retry = screen.getByRole('button', { name: '确认重试当前节点' })
    await userEvent.click(retry)
    expect(screen.getByRole('button', { name: '正在提交…' })).toBeDisabled()
    await userEvent.click(screen.getByRole('button', { name: '正在提交…' }))
    expect(confirm).toHaveBeenCalledTimes(1)
    expect(confirm).toHaveBeenCalledWith('run-1', 'action', 2, 9)
    expect(screen.getByRole('button', { name: '终止运行' })).toBeDisabled()
    await act(async () => confirmation.resolve(summary('queued')))
    expect(onRecovered).toHaveBeenCalledTimes(1)
  })

  it('409 后刷新详情并要求重新确认', async () => {
    const get = vi.spyOn(api, 'getRunRecovery')
      .mockResolvedValueOnce(recoveryView())
      .mockResolvedValueOnce({ ...recoveryView(), sequence: 10 })
    vi.spyOn(api, 'confirmRunNodeRetry').mockRejectedValue(new APIError(409, 'RUN_RECOVERY_CONFLICT', '冲突'))
    render(<RunRecoveryDialog runID="run-1" open onClose={vi.fn()} onRecovered={vi.fn()} />)
    await userEvent.click(await screen.findByRole('button', { name: '确认重试当前节点' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('状态已变化，请重新确认')
    expect(get).toHaveBeenCalledTimes(2)
    expect(screen.getByRole('button', { name: '确认重试当前节点' })).toBeEnabled()
  })
})

function recoveryView(): RunRecoveryView {
  return { runId: 'run-1', status: 'recovery_required', reason: 'uncertain_side_effect', sequence: 9, nodes: [{
    nodeId: 'action', nodeType: 'http', nodeTitle: '调用接口', nodeAttempt: 2, safety: 'side_effect', startedAt: '2026-09-01T00:00:00Z', retryAllowed: true,
    riskMessage: '重新执行可能重复调用外部服务、产生费用或写入数据，请确认后继续。',
  }] }
}
function summary(status: RunSummary['status']): RunSummary { return { id: 'run-1', workflowId: 'w1', workflowName: '演示', workflowSlug: 'demo', mode: 'test', status, startedAt: '2026-09-01T00:00:00Z' } }
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>((done) => { resolve = done }); return { promise, resolve } }
