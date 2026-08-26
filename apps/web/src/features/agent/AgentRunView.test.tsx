import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import type { AgentRunPublicView } from '../../lib/api/client'
import { AgentRunView } from './AgentRunView'

const presentation = { title: '助手', description: '', accent: 'indigo' as const, submitLabel: '运行', resultMode: 'auto' as const }
function publicView(status: AgentRunPublicView['run']['status'], output: unknown = null): AgentRunPublicView {
  return {
    run: { runId: 'run-1', workflowVersionId: 'version-secret', version: 1, status, startedAt: '2026-08-26T00:00:00Z', endedAt: status === 'running' ? null : '2026-08-26T00:01:00Z', output, error: status === 'failed' ? { code: 'RUN_FAILED', message: '执行失败' } : null },
    presentation,
    events: [
      { sequence: 1, type: 'node.started', status: 'running', timestamp: 't', nodeId: 'secret-node', output: 'secret-output' } as never,
      { sequence: 2, type: 'node.completed', status: 'completed', timestamp: 't' },
      { sequence: 3, type: 'log', timestamp: 't', input: 'secret-input' } as never,
    ], nextSequence: 3, hasMore: false,
  }
}

describe('AgentRunView', () => {
  it('运行中只显示聚合进度并允许取消', () => {
    render(<AgentRunView phase="running" view={publicView('running')} events={publicView('running').events} onCancel={vi.fn()} onRestart={vi.fn()} />)
    expect(screen.getByText('正在运行')).toBeInTheDocument()
    expect(screen.getByText('已结束 1 / 已开始 1')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '取消运行' })).toBeEnabled()
    expect(document.body.textContent).not.toContain('secret-node')
    expect(document.body.textContent).not.toContain('secret-output')
    expect(document.body.textContent).not.toContain('secret-input')
  })

  it('取消中禁用操作，终态提供再次运行和结果', () => {
    const { rerender } = render(<AgentRunView phase="cancelling" view={publicView('cancelling')} events={[]} onCancel={vi.fn()} onRestart={vi.fn()} />)
    expect(screen.getByRole('button', { name: '正在取消…' })).toBeDisabled()
    rerender(<AgentRunView phase="completed" view={publicView('completed', { answer: 42 })} events={[]} onCancel={vi.fn()} onRestart={vi.fn()} />)
    expect(screen.getByText('运行完成')).toBeInTheDocument()
    expect(screen.getByRole('region', { name: '运行结果' })).toHaveTextContent('"answer": 42')
    expect(screen.getByRole('button', { name: '再次运行' })).toBeInTheDocument()
  })

  it.each([['failed', '运行失败'], ['cancelled', '运行已取消']] as const)('%s 显示明确终态', (phase, label) => {
    render(<AgentRunView phase={phase} view={publicView(phase)} events={[]} error={phase === 'failed' ? '执行失败' : undefined} onCancel={vi.fn()} onRestart={vi.fn()} />)
    expect(screen.getByText(label)).toBeInTheDocument()
    expect(screen.getByText(/运行 ID：/)).toHaveTextContent('run-1')
    expect(screen.getByRole('button', { name: '再次运行' })).toBeInTheDocument()
  })
})
