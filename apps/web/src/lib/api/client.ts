import type { components } from './generated'

export type Graph = components['schemas']['Graph']
export type NodeDefinition = components['schemas']['NodeDefinition']
export type ResolvedPorts = components['schemas']['ResolvedPorts']
export type Workflow = components['schemas']['Workflow']
export type WorkflowVersion = components['schemas']['WorkflowVersion']
export type AgentManifest = components['schemas']['AgentManifest']
export type Run = components['schemas']['Run']
export type NodeRun = components['schemas']['NodeRun']
export type ErrorResponse = components['schemas']['ErrorResponse']
export type ValidationIssue = components['schemas']['ValidationIssue']
export type CreateWorkflowRequest = components['schemas']['CreateWorkflowRequest']
export type SaveDraftRequest = components['schemas']['SaveDraftRequest']
export type DraftRunRequest = components['schemas']['DraftRunRequest']
export type AgentRunRequest = components['schemas']['AgentRunRequest']
export type WorkflowTemplate = components['schemas']['WorkflowTemplate']
export type WorkflowTemplatePreview = components['schemas']['WorkflowTemplatePreview']
export type ImportWorkflowTemplateRequest = components['schemas']['ImportWorkflowTemplateRequest']

export class APIError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
    readonly requestId?: string,
    readonly issues?: ValidationIssue[],
  ) {
    super(message)
    this.name = 'APIError'
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers)
  if (init?.body !== undefined) headers.set('Content-Type', 'application/json')
  const response = await fetch(path, { ...init, headers })
  if (!response.ok) await throwAPIError(response)
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

async function streamRequest(path: string, init: RequestInit): Promise<Response> {
  const headers = new Headers(init.headers)
  headers.set('Content-Type', 'application/json')
  const response = await fetch(path, { ...init, headers })
  if (!response.ok) await throwAPIError(response)
  return response
}

async function requestBlob(path: string, signal?: AbortSignal): Promise<Blob> {
  const response = await fetch(path, { signal })
  if (!response.ok) await throwAPIError(response)
  return response.blob()
}

async function throwAPIError(response: Response): Promise<never> {
  let body: Partial<ErrorResponse> = {}
  try {
    body = (await response.json()) as ErrorResponse
  } catch {
    // 非 JSON 的上游故障也必须转换成稳定、安全的客户端错误。
  }
  throw new APIError(response.status, body.code ?? 'INTERNAL_ERROR', body.message ?? '请求失败', body.requestId, body.issues)
}

const jsonBody = (value: unknown) => JSON.stringify(value)

export const api = {
  listNodeTypes: (signal?: AbortSignal) => request<NodeDefinition[]>('/api/node-types', { signal }),
  resolveNodeType: (type: string, version: string, config: Record<string, unknown>, signal?: AbortSignal) =>
    request<ResolvedPorts>(`/api/node-types/${encodeURIComponent(type)}/${encodeURIComponent(version)}/resolve`, {
      method: 'POST', body: jsonBody({ config }), signal,
    }),
  listWorkflows: (signal?: AbortSignal) => request<Workflow[]>('/api/workflows', { signal }),
  createWorkflow: (body: CreateWorkflowRequest) => request<Workflow>('/api/workflows', { method: 'POST', body: jsonBody(body) }),
  previewWorkflowTemplate: (template: WorkflowTemplate, signal?: AbortSignal) =>
    request<WorkflowTemplatePreview>('/api/workflow-templates/preview', { method: 'POST', body: jsonBody({ template }), signal }),
  importWorkflowTemplate: (body: ImportWorkflowTemplateRequest, signal?: AbortSignal) =>
    request<Workflow>('/api/workflow-templates/import', { method: 'POST', body: jsonBody(body), signal }),
  exportWorkflowTemplate: (id: string, draftRevision: number, signal?: AbortSignal) =>
    requestBlob(`/api/workflows/${encodeURIComponent(id)}/template?draftRevision=${encodeURIComponent(String(draftRevision))}`, signal),
  getWorkflow: (id: string, signal?: AbortSignal) => request<Workflow>(`/api/workflows/${encodeURIComponent(id)}`, { signal }),
  saveWorkflow: (id: string, body: SaveDraftRequest, signal?: AbortSignal) =>
    request<Workflow>(`/api/workflows/${encodeURIComponent(id)}`, { method: 'PUT', body: jsonBody(body), signal }),
  validateWorkflow: (id: string) =>
    request<{ valid: boolean; issues: ValidationIssue[] }>(`/api/workflows/${encodeURIComponent(id)}/validate`, { method: 'POST' }),
  publishWorkflow: (id: string, draftRevision: number) =>
    request<WorkflowVersion>(`/api/workflows/${encodeURIComponent(id)}/publish`, { method: 'POST', body: jsonBody({ draftRevision }) }),
  runDraft: (id: string, body: DraftRunRequest, signal?: AbortSignal) =>
    streamRequest(`/api/workflows/${encodeURIComponent(id)}/test-runs`, { method: 'POST', body: jsonBody(body), signal }),
  listRuns: (id: string, limit = 50, signal?: AbortSignal) =>
    request<Run[]>(`/api/workflows/${encodeURIComponent(id)}/runs?limit=${limit}`, { signal }),
  getRun: (id: string, signal?: AbortSignal) =>
    request<{ run: Run; nodeRuns: NodeRun[] }>(`/api/runs/${encodeURIComponent(id)}`, { signal }),
  getAgentManifest: (slug: string, signal?: AbortSignal) =>
    request<AgentManifest>(`/api/agents/${encodeURIComponent(slug)}`, { signal }),
  runAgent: (slug: string, body: AgentRunRequest, signal?: AbortSignal) =>
    streamRequest(`/api/agents/${encodeURIComponent(slug)}/runs`, { method: 'POST', body: jsonBody(body), signal }),
}
