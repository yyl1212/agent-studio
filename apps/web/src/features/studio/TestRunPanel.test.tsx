import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import type { JSONSchema } from '../../components/schema-form/types'
import type { RunEvent } from '../../lib/api/ndjson'
import { TestRunPanel } from './TestRunPanel'

const schema: JSONSchema = {
  type: 'object',
  properties: { topic: { type: 'string', title: '主题' } },
  required: ['topic'],
}

describe('TestRunPanel', () => {
  it('失败后保留输入并允许原位重试', async () => {
    const onRun = vi.fn()
    const props = { schema, events: [], running: false, error: '网络失败', onRun, onCancel: vi.fn() }
    const { rerender } = render(<TestRunPanel {...props} />)
    await userEvent.type(screen.getByLabelText('主题'), '保留输入')
    rerender(<TestRunPanel {...props} error="网络仍失败" />)
    await userEvent.click(screen.getByRole('button', { name: '重试运行' }))
    expect(onRun).toHaveBeenCalledWith({ topic: '保留输入' })
  })

  it('本地取消立即显示状态、保留事件并以中文展示进度', () => {
    render(<TestRunPanel schema={schema} events={[runEvent(1, 'node.started', 'template')]} running={false} cancelled error="" onRun={vi.fn()} onCancel={vi.fn()} />)
    expect(screen.getByRole('status')).toHaveTextContent('运行已取消')
    expect(screen.getByText('template：运行中')).toBeInTheDocument()
  })

  it('显示运行结果和可计算的耗时', () => {
    render(<TestRunPanel schema={schema} events={[
      runEvent(1, 'run.started', undefined, '2026-08-27T00:00:00.000Z'),
      { ...runEvent(2, 'run.completed', undefined, '2026-08-27T00:00:01.200Z'), output: { answer: 42 } },
    ]} running={false} error="" onRun={vi.fn()} onCancel={vi.fn()} />)
    expect(screen.getByText('耗时 1.2 秒')).toBeInTheDocument()
    expect(screen.getByText(/"answer": 42/)).toBeInTheDocument()
  })

  it('节点失败后优先显示具体的安全错误，而不是后续通用运行错误', () => {
    render(<TestRunPanel schema={schema} events={[
      { ...runEvent(1, 'node.failed', 'webhook'), error: { code: 'NODE_EXECUTION_FAILED', kind: 'input', message: '节点输入无效' } },
      { ...runEvent(2, 'run.failed'), error: { code: 'RUN_FAILED', kind: 'input', message: '运行失败' } },
    ]} running={false} error="" onRun={vi.fn()} onCancel={vi.fn()} />)

    expect(screen.getByRole('alert')).toHaveTextContent('节点输入无效')
  })

  it('作为工作台内容提交输入而不创建第二个 dialog', async () => {
    const onRun = vi.fn()
    render(<TestRunPanel schema={{ type: 'object', required: ['topic'], properties: { topic: { type: 'string', title: '主题', minLength: 1 } } }} events={[]} running={false} error="" onRun={onRun} onCancel={vi.fn()} />)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    await userEvent.type(screen.getByLabelText('主题'), 'Agent Studio')
    await userEvent.click(screen.getByRole('button', { name: '运行' }))
    expect(onRun).toHaveBeenCalledWith({ topic: 'Agent Studio' })
  })

  it('父组件重渲染时保留尚未提交的运行输入', async () => {
    const onRun = vi.fn()
    const props = { schema, events: [], running: false, error: '', onRun, onCancel: vi.fn() }
    const { rerender } = render(<TestRunPanel {...props} />)

    await userEvent.type(screen.getByLabelText('主题'), '保留输入')
    rerender(<TestRunPanel {...props} error="状态已更新" />)
    await userEvent.click(screen.getByRole('button', { name: '运行' }))

    expect(onRun).toHaveBeenCalledWith({ topic: '保留输入' })
  })
})

function runEvent(sequence: number, type: RunEvent['type'], nodeId?: string, timestamp = '2026-08-27T00:00:00Z'): RunEvent {
  return { sequence, type, runId: 'r1', ...(nodeId ? { nodeId } : {}), activePorts: [], inputRedactedPaths: [], outputRedactedPaths: [], timestamp }
}
