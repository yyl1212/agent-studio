import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { APIError, api, type AgentManifest, type AgentRunPublicView } from '../../lib/api/client'
import { AgentPage } from './AgentPage'

vi.mock('../../lib/api/client', async (importOriginal) => {
  const original = await importOriginal<typeof import('../../lib/api/client')>()
  return { ...original, api: { ...original.api } }
})

const inputSchema = { $schema: 'https://json-schema.org/draft/2020-12/schema', type: 'object', properties: { topic: { type: 'string', title: '主题' }, token: { type: 'string', title: '密钥' } }, required: ['topic'] }
const presentation = { title: '研究助手', description: '输入主题生成报告', accent: 'teal' as const, submitLabel: '开始研究', resultMode: 'json' as const }
const manifest: AgentManifest = { workflowVersionId: 'version-1', version: 1, title: '旧标题', description: '旧说明', inputSchema, presentation }

function publicView(status: AgentRunPublicView['run']['status'], overrides: Partial<AgentRunPublicView> = {}): AgentRunPublicView {
  return {
    run: { runId: 'run-1', workflowVersionId: 'version-1', version: 1, status, startedAt: '2026-08-26T00:00:00Z', endedAt: status === 'running' ? null : '2026-08-26T00:01:00Z', output: status === 'completed' ? { answer: 42 } : null, error: null },
    presentation, events: [], nextSequence: 0, hasMore: false, ...overrides,
  }
}

function LocationProbe() { const location = useLocation(); return <output aria-label="当前地址">{location.pathname}{location.search}</output> }
function renderPage(entry = '/agents/demo') {
  return render(<MemoryRouter initialEntries={[entry]}><Routes><Route path="/agents/:slug" element={<><AgentPage /><LocationProbe /></>} /></Routes></MemoryRouter>)
}

describe('AgentPage', () => {
  afterEach(() => vi.restoreAllMocks())
  beforeEach(() => vi.spyOn(api, 'getAgentManifest').mockResolvedValue(manifest))

  it('按冻结页面配置展示表单并以异步协议运行', async () => {
    vi.spyOn(api, 'startAgentRun').mockResolvedValue(publicView('running').run)
    vi.spyOn(api, 'getAgentRunView').mockResolvedValue(publicView('completed'))
    renderPage()
    expect(await screen.findByRole('heading', { name: '研究助手' })).toBeInTheDocument()
    expect(screen.getByText('输入主题生成报告')).toBeInTheDocument()
    expect(document.querySelector('.agent-shell')).toHaveClass('accent-teal')
    fireEvent.change(screen.getByLabelText('主题'), { target: { value: 'Agent' } })
    fireEvent.change(screen.getByLabelText('密钥'), { target: { value: 'top-secret' } })
    await userEvent.click(screen.getByRole('button', { name: '开始研究' }))
    expect(api.startAgentRun).toHaveBeenCalledWith('demo', { workflowVersionId: 'version-1', input: { topic: 'Agent', token: 'top-secret' } }, expect.any(String), expect.any(AbortSignal))
    await waitFor(() => expect(screen.getByLabelText('当前地址')).toHaveTextContent('/agents/demo?runId=run-1'))
    expect(await screen.findByText('运行完成')).toBeInTheDocument()
    expect(screen.getByRole('region', { name: '运行结果' })).toHaveTextContent('"answer": 42')
    expect(document.body.textContent).not.toContain('top-secret')
  })

  it('带 runId 时不加载当前 manifest，直接恢复旧运行页面', async () => {
    const oldPresentation = { ...presentation, title: '旧版本助手', accent: 'rose' as const, resultMode: 'text' as const }
    vi.spyOn(api, 'getAgentRunView').mockResolvedValue(publicView('completed', { presentation: oldPresentation }))
    renderPage('/agents/demo?runId=run-1')
    expect(await screen.findByRole('heading', { name: '旧版本助手' })).toBeInTheDocument()
    expect(api.getAgentManifest).not.toHaveBeenCalled()
    expect(document.querySelector('.agent-shell')).toHaveClass('accent-rose')
    expect(screen.queryByLabelText('主题')).not.toBeInTheDocument()
  })

  it('再次运行移除 runId、加载最新 manifest 并保留本页输入', async () => {
    vi.spyOn(api, 'startAgentRun').mockResolvedValue(publicView('running').run)
    vi.spyOn(api, 'getAgentRunView').mockResolvedValue(publicView('completed'))
    const getManifest = vi.mocked(api.getAgentManifest)
    renderPage()
    fireEvent.change(await screen.findByLabelText('主题'), { target: { value: '保留主题' } })
    await userEvent.click(screen.getByRole('button', { name: '开始研究' }))
    await userEvent.click(await screen.findByRole('button', { name: '再次运行' }))
    expect(await screen.findByLabelText('主题')).toHaveValue('保留主题')
    expect(screen.getByLabelText('当前地址')).toHaveTextContent('/agents/demo')
    expect(getManifest).toHaveBeenCalledTimes(2)
  })

  it('恢复期间只显示骨架，不闪现空表单', () => {
    vi.spyOn(api, 'getAgentRunView').mockImplementation(() => new Promise(() => {}))
    renderPage('/agents/demo?runId=run-1')
    expect(screen.getByText('正在恢复运行…')).toBeInTheDocument()
    expect(screen.queryByLabelText('主题')).not.toBeInTheDocument()
    expect(api.getAgentManifest).not.toHaveBeenCalled()
  })

  it('显示安全的 Agent 加载错误', async () => {
    vi.mocked(api.getAgentManifest).mockRejectedValueOnce(new APIError(409, 'WORKFLOW_ARCHIVED', '内部归档错误', 'req-secret'))
    renderPage()
    expect(await screen.findByRole('alert')).toHaveTextContent('该 Agent 已归档，暂时不能运行')
    expect(screen.getByRole('alert')).not.toHaveTextContent('内部归档错误')
    expect(screen.getByRole('alert')).not.toHaveTextContent('req-secret')
  })
})
