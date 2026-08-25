import { render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import { RunHistoryPage } from './RunHistoryPage'

describe('RunHistoryPage', () => {
  it('把旧工作流历史入口替换到全局运行筛选', async () => {
    const id = 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa'
    render(<MemoryRouter initialEntries={[`/workflows/${id}/runs`]}><Routes><Route path="/workflows/:id/runs" element={<RunHistoryPage />} /><Route path="/runs" element={<Destination />} /></Routes></MemoryRouter>)
    expect(await screen.findByText(`?workflowId=${id}`)).toBeInTheDocument()
  })
})

function Destination() { return <p>{useLocation().search}</p> }
