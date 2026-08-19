import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { APIError, api, type Workflow, type WorkflowTemplate, type WorkflowTemplatePreview } from '../../lib/api/client'
import { ImportWorkflowTemplateDialog, suggestSlug } from './ImportWorkflowTemplateDialog'

describe('ImportWorkflowTemplateDialog', () => {
  afterEach(() => vi.restoreAllMocks())

  it('预览合法模板并创建新工作流', async () => {
    vi.spyOn(api, 'previewWorkflowTemplate').mockResolvedValue(previewFixture())
    vi.spyOn(api, 'importWorkflowTemplate').mockResolvedValue(workflowFixture())
    const onImported = vi.fn()
    render(<ImportWorkflowTemplateDialog onClose={vi.fn()} onImported={onImported} />)

    await userEvent.upload(screen.getByLabelText('选择模板文件'), templateFile())
    expect(await screen.findByText('3 个节点 · 2 条连线')).toBeInTheDocument()
    expect(screen.getByText('topic · 主题 · 必填')).toBeInTheDocument()
    expect(screen.getByText('Echo · 1.0.0')).toBeInTheDocument()
    expect(screen.getByText('network')).toBeInTheDocument()
    expect(screen.getByLabelText('名称')).toHaveValue('演示模板')
    expect(screen.getByLabelText('说明')).toHaveValue('前端测试')
    expect(screen.getByLabelText('Agent 地址标识')).toHaveValue('imported-workflow')

    await userEvent.clear(screen.getByLabelText('Agent 地址标识'))
    await userEvent.type(screen.getByLabelText('Agent 地址标识'), 'demo-copy')
    await userEvent.click(screen.getByRole('button', { name: '导入并打开' }))
    expect(api.importWorkflowTemplate).toHaveBeenCalledWith(
      expect.objectContaining({ slug: 'demo-copy', template: templateFixture() }),
      expect.any(AbortSignal),
    )
    expect(onImported).toHaveBeenCalledWith(workflowFixture())
  })

  it('在本地拒绝超过 2 MiB 的文件和非法 JSON', async () => {
    const preview = vi.spyOn(api, 'previewWorkflowTemplate')
    render(<ImportWorkflowTemplateDialog onClose={vi.fn()} onImported={vi.fn()} />)

    const oversized = new File(['x'.repeat((2 << 20) + 1)], 'large.json', { type: 'application/json' })
    await userEvent.upload(screen.getByLabelText('选择模板文件'), oversized)
    expect(await screen.findByRole('alert')).toHaveTextContent('不能超过 2 MiB')
    expect(preview).not.toHaveBeenCalled()

    await userEvent.upload(screen.getByLabelText('选择模板文件'), new File(['{broken'], 'broken.json', { type: 'application/json' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('JSON')
    expect(preview).not.toHaveBeenCalled()
  })

  it('拒绝不是对象的合法 JSON 且不触发预览', async () => {
    const preview = vi.spyOn(api, 'previewWorkflowTemplate')
    render(<ImportWorkflowTemplateDialog onClose={vi.fn()} onImported={vi.fn()} />)
    await userEvent.upload(screen.getByLabelText('选择模板文件'), new File(['null'], 'null.json', { type: 'application/json' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('模板对象')
    expect(preview).not.toHaveBeenCalled()
  })

  it('展示无效预览并禁止导入', async () => {
    vi.spyOn(api, 'previewWorkflowTemplate').mockResolvedValue({
      ...previewFixture(),
      valid: false,
      summary: {
        ...previewFixture().summary,
        nodeTypes: [{ type: 'extension.missing', version: '9.9.9', title: 'extension.missing', count: 1, available: false, capabilities: [] }],
      },
      issues: [{ code: 'NODE_TYPE_NOT_FOUND', message: '节点类型或版本未注册', nodeId: 'missing' }],
    })
    render(<ImportWorkflowTemplateDialog onClose={vi.fn()} onImported={vi.fn()} />)
    await userEvent.upload(screen.getByLabelText('选择模板文件'), templateFile())
    expect(await screen.findByText('节点类型或版本未注册')).toBeInTheDocument()
    expect(screen.getByText('不可用')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '导入并打开' })).toBeDisabled()
  })

  it('显示 slug 冲突且不关闭 Dialog', async () => {
    vi.spyOn(api, 'previewWorkflowTemplate').mockResolvedValue(previewFixture())
    vi.spyOn(api, 'importWorkflowTemplate').mockRejectedValue(new APIError(409, 'WORKFLOW_SLUG_CONFLICT', 'Agent 地址标识已存在'))
    const onImported = vi.fn()
    render(<ImportWorkflowTemplateDialog onClose={vi.fn()} onImported={onImported} />)
    await userEvent.upload(screen.getByLabelText('选择模板文件'), templateFile())
    await screen.findByText('3 个节点 · 2 条连线')
    await userEvent.click(screen.getByRole('button', { name: '导入并打开' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Agent 地址标识已存在')
    expect(onImported).not.toHaveBeenCalled()
  })

  it('重选文件会清理旧预览状态', async () => {
    vi.spyOn(api, 'previewWorkflowTemplate').mockResolvedValue(previewFixture())
    render(<ImportWorkflowTemplateDialog onClose={vi.fn()} onImported={vi.fn()} />)
    const input = screen.getByLabelText('选择模板文件')
    await userEvent.upload(input, templateFile())
    expect(await screen.findByText('3 个节点 · 2 条连线')).toBeInTheDocument()
    await userEvent.upload(input, new File(['not-json'], 'broken.json', { type: 'application/json' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('JSON')
    expect(screen.queryByText('3 个节点 · 2 条连线')).not.toBeInTheDocument()
  })

  it('重选后忽略旧请求的迟到错误', async () => {
    let rejectFirst!: (reason: unknown) => void
    vi.spyOn(api, 'previewWorkflowTemplate')
      .mockReturnValueOnce(new Promise<WorkflowTemplatePreview>((_resolve, reject) => { rejectFirst = reject }))
      .mockResolvedValueOnce(previewFixture())
    render(<ImportWorkflowTemplateDialog onClose={vi.fn()} onImported={vi.fn()} />)
    const input = screen.getByLabelText('选择模板文件')
    await userEvent.upload(input, templateFile())
    await waitFor(() => expect(api.previewWorkflowTemplate).toHaveBeenCalledTimes(1))
    await userEvent.upload(input, new File([JSON.stringify(templateFixture())], 'second.json', { type: 'application/json' }))
    expect(await screen.findByText('3 个节点 · 2 条连线')).toBeInTheDocument()
    await act(async () => {
      rejectFirst(new TypeError('late failure'))
      await Promise.resolve()
    })
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('关闭时中止当前预览请求', async () => {
    let signal: AbortSignal | undefined
    vi.spyOn(api, 'previewWorkflowTemplate').mockImplementation((_template, requestSignal) => {
      signal = requestSignal
      return new Promise<WorkflowTemplatePreview>(() => undefined)
    })
    const onClose = vi.fn()
    render(<ImportWorkflowTemplateDialog onClose={onClose} onImported={vi.fn()} />)
    await userEvent.upload(screen.getByLabelText('选择模板文件'), templateFile())
    await waitFor(() => expect(api.previewWorkflowTemplate).toHaveBeenCalled())
    await userEvent.click(screen.getByRole('button', { name: '取消' }))
    expect(signal?.aborted).toBe(true)
    expect(onClose).toHaveBeenCalled()
  })
})

it('生成安全的模板 slug 建议', () => {
  expect(suggestSlug('  Demo Agent  ')).toBe('demo-agent')
  expect(suggestSlug('中文模板')).toBe('imported-workflow')
})

const templateFixture = (): WorkflowTemplate => ({
  apiVersion: 'agent-studio.dev/v1alpha1',
  kind: 'WorkflowTemplate',
  metadata: { name: '演示模板', description: '前端测试' },
  spec: { graph: { schemaVersion: 1, nodes: [], edges: [] } },
})

const previewFixture = (): WorkflowTemplatePreview => ({
  valid: true,
  metadata: templateFixture().metadata,
  summary: {
    nodeCount: 3,
    edgeCount: 2,
    inputSchema: { type: 'object', properties: { topic: { type: 'string', title: '主题' } }, required: ['topic'] },
    nodeTypes: [{ type: 'extension.echo', version: '1.0.0', title: 'Echo', count: 1, available: true, capabilities: ['network'] }],
  },
  issues: [],
})

const workflowFixture = (): Workflow => ({
  id: 'w-copy', name: '副本', slug: 'copy', description: '', draftRevision: 1,
  draftGraph: { schemaVersion: 1, nodes: [], edges: [] },
  createdAt: '2026-08-19T00:00:00Z', updatedAt: '2026-08-19T00:00:00Z',
})

const templateFile = () => new File([JSON.stringify(templateFixture())], 'demo.workflow.json', { type: 'application/json' })
