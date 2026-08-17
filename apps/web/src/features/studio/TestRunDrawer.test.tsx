import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { TestRunDrawer } from './TestRunDrawer'

describe('TestRunDrawer', () => {
  it('显示节点进度、最终结果并可取消', async () => {
    const onCancel = vi.fn()
    render(
      <TestRunDrawer
        schema={{ type: 'object', properties: {} }}
        events={[
          { sequence: 1, type: 'node.started', runId: 'r1', nodeId: 'llm', timestamp: '2026-08-17T00:00:00Z' },
          { sequence: 2, type: 'run.completed', runId: 'r1', output: '<script>安全文本</script>', timestamp: '2026-08-17T00:00:01Z' },
        ]}
        running
        error=""
        onRun={vi.fn()}
        onCancel={onCancel}
        onClose={vi.fn()}
      />,
    )
    expect(screen.getByText('llm：node.started')).toBeInTheDocument()
    expect(screen.getByText('<script>安全文本</script>')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '取消运行' }))
    expect(onCancel).toHaveBeenCalled()
  })
})
