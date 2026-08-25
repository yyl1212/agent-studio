import type { components } from './generated'

export type Graph = components['schemas']['Graph']
export type NodeDefinition = components['schemas']['NodeDefinition']
export type ResolvedPorts = components['schemas']['ResolvedPorts']
export type Workflow = components['schemas']['Workflow']
export type WorkflowVersion = components['schemas']['WorkflowVersion']
export type AgentManifest = components['schemas']['AgentManifest']
export type Run = components['schemas']['Run']
export type NodeRun = components['schemas']['NodeRun']
export type DebugOverview = components['schemas']['DebugOverview']
export type RunEventPage = components['schemas']['RunEventPage']
export type RerunPreview = components['schemas']['RerunPreview']
export type RerunRequest = components['schemas']['RerunRequest']
export type ErrorResponse = components['schemas']['ErrorResponse']
export type ValidationIssue = components['schemas']['ValidationIssue']
export type CreateWorkflowRequest = components['schemas']['CreateWorkflowRequest']
export type SaveDraftRequest = components['schemas']['SaveDraftRequest']
export type DraftRunRequest = components['schemas']['DraftRunRequest']
export type AgentRunRequest = components['schemas']['AgentRunRequest']
export type WorkflowTemplate = components['schemas']['WorkflowTemplateV1Alpha1'] | components['schemas']['WorkflowTemplateV1Alpha2']
export type WorkflowTemplatePreview = components['schemas']['WorkflowTemplatePreview']
export type ImportWorkflowTemplateRequest = components['schemas']['ImportWorkflowTemplateRequest']
export type NodeIndexStatus = components['schemas']['NodeIndexStatus']
export type NodePackageSearchResult = components['schemas']['NodePackageSearchResult']
export type IndexedNodePackageSummary = components['schemas']['IndexedNodePackageSummary']
export type NodePackageDetail = components['schemas']['NodePackageDetail']
export type WorkflowSummary = components['schemas']['WorkflowSummary']
export type WorkflowSummaryPage = components['schemas']['WorkflowSummaryPage']
export type RunSummary = components['schemas']['RunSummary']
export type RunSummaryPage = components['schemas']['RunSummaryPage']
export type RunRetryPreview = components['schemas']['RunRetryPreview']
export type RunRetryRequest = components['schemas']['RunRetryRequest']
export type UpdateWorkflowRequest = components['schemas']['UpdateWorkflowRequest']
export type CopyWorkflowRequest = components['schemas']['CopyWorkflowRequest']

export type WorkflowSummaryQuery = {
  q: string
  state: 'active' | 'archived' | 'all'
  cursor?: string
  limit: number
}

export type RunSummaryQuery = {
  workflowId?: string
  statuses: Run['status'][]
  modes: Run['mode'][]
  startedAfter?: string
  startedBefore?: string
  runId?: string
  cursor?: string
  limit: number
}

export type NodePackageQuery = {
  q: string
  categories: string[]
  compatible: boolean
  limit: number
  offset: number
}

export class APIError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
    readonly requestId?: string,
    readonly issues?: ValidationIssue[],
    readonly details?: Readonly<{ runId?: string }>,
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
  throw new APIError(response.status, body.code ?? 'INTERNAL_ERROR', body.message ?? '请求失败', body.requestId, body.issues, safeErrorDetails(body.details))
}

const canonicalUUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/

function safeErrorDetails(value: unknown): Readonly<{ runId?: string }> | undefined {
  if (typeof value !== 'object' || value === null) return undefined
  const runId = (value as Record<string, unknown>).runId
  return typeof runId === 'string' && canonicalUUID.test(runId) ? Object.freeze({ runId }) : undefined
}

const jsonBody = (value: unknown) => JSON.stringify(value)
const rawWorkflowTemplates = new WeakMap<object, string>()

export function parseWorkflowTemplateJSON(source: string): WorkflowTemplate {
  const value: unknown = JSON.parse(source)
  if (typeof value === 'object' && value !== null) rawWorkflowTemplates.set(value, source)
  return value as WorkflowTemplate
}

export function serializePreviewWorkflowTemplateRequest(template: WorkflowTemplate) {
  const raw = rawWorkflowTemplates.get(template)
  return raw === undefined ? jsonBody({ template }) : `{"template":${raw}}`
}

export function serializeImportWorkflowTemplateRequest(body: ImportWorkflowTemplateRequest) {
  const raw = rawWorkflowTemplates.get(body.template)
  if (raw === undefined) return jsonBody(body)
  return `{"template":${raw},"name":${JSON.stringify(body.name)},"slug":${JSON.stringify(body.slug)},"description":${JSON.stringify(body.description)}}`
}

export const api = {
  listNodeTypes: (signal?: AbortSignal) => request<NodeDefinition[]>('/api/node-types', { signal }),
  resolveNodeType: (type: string, version: string, config: Record<string, unknown>, signal?: AbortSignal) =>
    request<ResolvedPorts>(`/api/node-types/${encodeURIComponent(type)}/${encodeURIComponent(version)}/resolve`, {
      method: 'POST', body: jsonBody({ config }), signal,
    }),
  getNodeIndexStatus: (signal?: AbortSignal) => request<NodeIndexStatus>('/api/node-index/status', { signal }),
  listNodePackages: (query: NodePackageQuery, signal?: AbortSignal) => {
    const params = new URLSearchParams()
    if (query.q) params.set('q', query.q)
    for (const category of query.categories) params.append('category', category)
    params.set('compatible', String(query.compatible))
    params.set('limit', String(query.limit))
    params.set('offset', String(query.offset))
    return request<NodePackageSearchResult>(`/api/node-packages?${params}`, { signal })
  },
  getNodePackage: (name: string, signal?: AbortSignal) => {
    const params = new URLSearchParams({ name })
    return request<NodePackageDetail>(`/api/node-package?${params}`, { signal })
  },
  listWorkflows: (signal?: AbortSignal) => request<Workflow[]>('/api/workflows', { signal }),
  listWorkflowSummaries: (query: WorkflowSummaryQuery, signal?: AbortSignal) => {
    const params = new URLSearchParams()
    if (query.q) params.set('q', query.q)
    params.set('state', query.state)
    if (query.cursor) params.set('cursor', query.cursor)
    params.set('limit', String(query.limit))
    return request<WorkflowSummaryPage>(appendSearch('/api/workflow-summaries', params), { signal })
  },
  createWorkflow: (body: CreateWorkflowRequest) => request<Workflow>('/api/workflows', { method: 'POST', body: jsonBody(body) }),
  previewWorkflowTemplate: (template: WorkflowTemplate, signal?: AbortSignal) =>
    request<WorkflowTemplatePreview>('/api/workflow-templates/preview', { method: 'POST', body: serializePreviewWorkflowTemplateRequest(template), signal }),
  importWorkflowTemplate: (body: ImportWorkflowTemplateRequest, signal?: AbortSignal) =>
    request<Workflow>('/api/workflow-templates/import', { method: 'POST', body: serializeImportWorkflowTemplateRequest(body), signal }),
  exportWorkflowTemplate: (id: string, draftRevision: number, signal?: AbortSignal) =>
    requestBlob(`/api/workflows/${encodeURIComponent(id)}/template?draftRevision=${encodeURIComponent(String(draftRevision))}`, signal),
  getWorkflow: (id: string, signal?: AbortSignal) => request<Workflow>(`/api/workflows/${encodeURIComponent(id)}`, { signal }),
  updateWorkflow: (id: string, body: UpdateWorkflowRequest, signal?: AbortSignal) =>
    request<Workflow>(`/api/workflows/${encodeURIComponent(id)}`, { method: 'PATCH', body: jsonBody(body), signal }),
  copyWorkflow: (id: string, body: CopyWorkflowRequest, signal?: AbortSignal) =>
    request<Workflow>(`/api/workflows/${encodeURIComponent(id)}/copies`, { method: 'POST', body: jsonBody(body), signal }),
  archiveWorkflow: (id: string, signal?: AbortSignal) =>
    request<Workflow>(`/api/workflows/${encodeURIComponent(id)}/archive`, { method: 'POST', signal }),
  restoreWorkflow: (id: string, signal?: AbortSignal) =>
    request<Workflow>(`/api/workflows/${encodeURIComponent(id)}/restore`, { method: 'POST', signal }),
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
  listRunSummaries: (query: RunSummaryQuery, signal?: AbortSignal) => {
    const params = new URLSearchParams()
    if (query.workflowId) params.set('workflowId', query.workflowId)
    for (const status of query.statuses) params.append('status', status)
    for (const mode of query.modes) params.append('mode', mode)
    if (query.startedAfter) params.set('startedAfter', query.startedAfter)
    if (query.startedBefore) params.set('startedBefore', query.startedBefore)
    if (query.runId) params.set('runId', query.runId)
    if (query.cursor) params.set('cursor', query.cursor)
    params.set('limit', String(query.limit))
    return request<RunSummaryPage>(appendSearch('/api/runs', params), { signal })
  },
  previewRunRetry: (id: string, signal?: AbortSignal) =>
    request<RunRetryPreview>(`/api/runs/${encodeURIComponent(id)}/retry-preview`, { signal }),
  retryRun: (id: string, idempotencyKey: string, body: RunRetryRequest, signal?: AbortSignal) =>
    streamRequest(`/api/runs/${encodeURIComponent(id)}/retries`, {
      method: 'POST', headers: { 'Idempotency-Key': idempotencyKey }, body: jsonBody(body), signal,
    }),
  getDebugOverview: (id: string, signal?: AbortSignal) =>
    request<DebugOverview>(`/api/runs/${encodeURIComponent(id)}/debug`, { signal }),
  listRunEvents: (id: string, afterSequence = 0, signal?: AbortSignal) =>
    request<RunEventPage>(`/api/runs/${encodeURIComponent(id)}/events?afterSequence=${afterSequence}`, { signal }),
  previewRerun: (runID: string, nodeID: string, signal?: AbortSignal) =>
    request<RerunPreview>(`/api/runs/${encodeURIComponent(runID)}/nodes/${encodeURIComponent(nodeID)}/rerun-preview`, { signal }),
  rerunFromNode: (runID: string, nodeID: string, body: RerunRequest, signal?: AbortSignal) =>
    streamRequest(`/api/runs/${encodeURIComponent(runID)}/nodes/${encodeURIComponent(nodeID)}/reruns`, {
      method: 'POST', body: jsonBody(body), signal,
    }),
  getAgentManifest: (slug: string, signal?: AbortSignal) =>
    request<AgentManifest>(`/api/agents/${encodeURIComponent(slug)}`, { signal }),
  runAgent: (slug: string, body: AgentRunRequest, signal?: AbortSignal) =>
    streamRequest(`/api/agents/${encodeURIComponent(slug)}/runs`, { method: 'POST', body: jsonBody(body), signal }),
}

function appendSearch(path: string, params: URLSearchParams): string {
  const search = params.toString()
  return search ? `${path}?${search}` : path
}
