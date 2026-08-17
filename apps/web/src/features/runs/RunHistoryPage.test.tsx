import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '../../lib/api/client'
import { RunHistoryPage } from './RunHistoryPage'

vi.mock('../../lib/api/client', async (importOriginal) => {
  const original = await importOriginal<typeof import('../../lib/api/client')>()
  return { ...original, api: { ...original.api } }
})

describe('RunHistoryPage', () => {
  beforeEach(() => {
    vi.spyOn(api, 'listRuns').mockResolvedValue([{
      id: 'r1', workflowId: 'w1', workflowVersionId: 'v1', mode: 'published', status: 'completed', input: {}, output: 'ok',
      startedAt: '2026-08-17T00:00:00Z', endedAt: '2026-08-17T00:00:02Z',
    }])
    vi.spyOn(api, 'getRun').mockResolvedValue({
      run: { id: 'r1', workflowId: 'w1', mode: 'published', status: 'completed', input: {}, startedAt: '2026-08-17T00:00:00Z' },
      nodeRuns: [{ id: 'n1', runId: 'r1', nodeId: 'llm', nodeType: 'llm', status: 'completed', output: { text: 'ok' } }],
    })
  })

  it('显示版本、状态、耗时并加载节点详情', async () => {
    render(<MemoryRouter initialEntries={['/workflows/w1/runs']}><Routes><Route path="/workflows/:id/runs" element={<RunHistoryPage />} /></Routes></MemoryRouter>)
    expect(await screen.findByText('已发布')).toBeInTheDocument()
    expect(screen.getByText('2.0 秒')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '查看运行 r1' }))
    expect(await screen.findByText('llm')).toBeInTheDocument()
  })
})
