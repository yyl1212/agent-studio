import type { Run, RunSummaryQuery, WorkflowSummaryQuery } from '../../lib/api/client'

export interface ParsedManagementSearch<T> {
  query: T
  params: URLSearchParams
  hadInvalid: boolean
}

const workflowKeys = new Set(['q', 'state', 'cursor', 'limit'])
const runKeys = new Set(['workflowId', 'status', 'mode', 'startedAfter', 'startedBefore', 'runId', 'cursor', 'limit'])
const workflowStates = new Set<WorkflowSummaryQuery['state']>(['active', 'archived', 'all'])
const runStatuses = new Set<Run['status']>(['queued', 'running', 'recovery_required', 'cancelling', 'completed', 'failed', 'cancelled'])
const runModes = new Set<Run['mode']>(['test', 'published', 'debug'])
const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
const rfc3339Pattern = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(Z|[+-]\d{2}:\d{2})$/
const defaultLimit = 50

export function readWorkflowSearch(params: URLSearchParams): ParsedManagementSearch<WorkflowSummaryQuery> {
  let hadInvalid = hasUnknownKeys(params, workflowKeys)
  const qValue = readSingle(params, 'q')
  hadInvalid ||= qValue.invalid
  let q = qValue.value ?? ''
  if (new TextEncoder().encode(q).byteLength > 100) {
    q = ''
    hadInvalid = true
  }

  const stateValue = readSingle(params, 'state')
  hadInvalid ||= stateValue.invalid
  let state: WorkflowSummaryQuery['state'] = 'active'
  if (stateValue.value !== undefined) {
    if (workflowStates.has(stateValue.value as WorkflowSummaryQuery['state'])) state = stateValue.value as WorkflowSummaryQuery['state']
    else hadInvalid = true
  }

  const cursorValue = readSingle(params, 'cursor')
  hadInvalid ||= cursorValue.invalid
  let cursor = cursorValue.value || undefined
  if (cursor && cursor.length > 512) {
    cursor = undefined
    hadInvalid = true
  }

  const limitValue = readSingle(params, 'limit')
  hadInvalid ||= limitValue.invalid
  const parsedLimit = parseLimit(limitValue.value)
  hadInvalid ||= parsedLimit.invalid
  const query: WorkflowSummaryQuery = { q, state, limit: parsedLimit.value, ...(cursor ? { cursor } : {}) }
  return { query, params: writeWorkflowSearch(query), hadInvalid }
}

export function writeWorkflowSearch(query: WorkflowSummaryQuery): URLSearchParams {
  const params = new URLSearchParams()
  if (query.q) params.set('q', query.q)
  params.set('state', query.state)
  if (query.cursor) params.set('cursor', query.cursor)
  params.set('limit', String(query.limit))
  return params
}

export function readRunSearch(params: URLSearchParams): ParsedManagementSearch<RunSummaryQuery> {
  let hadInvalid = hasUnknownKeys(params, runKeys)
  const workflowID = readUUID(params, 'workflowId')
  const runID = readUUID(params, 'runId')
  hadInvalid ||= workflowID.invalid || runID.invalid

  const statuses = readEnums(params, 'status', runStatuses, 7)
  const modes = readEnums(params, 'mode', runModes, 3)
  hadInvalid ||= statuses.invalid || modes.invalid

  const afterValue = readSingle(params, 'startedAfter')
  const beforeValue = readSingle(params, 'startedBefore')
  hadInvalid ||= afterValue.invalid || beforeValue.invalid
  const after = parseRFC3339(afterValue.value)
  const before = parseRFC3339(beforeValue.value)
  hadInvalid ||= after.invalid || before.invalid
  let startedAfter = after.value
  let startedBefore = before.value
  if (startedAfter && startedBefore) {
    const span = Date.parse(startedBefore) - Date.parse(startedAfter)
    if (span <= 0 || span > 90 * 24 * 60 * 60 * 1000) {
      startedAfter = undefined
      startedBefore = undefined
      hadInvalid = true
    }
  }

  const cursorValue = readSingle(params, 'cursor')
  hadInvalid ||= cursorValue.invalid
  let cursor = cursorValue.value || undefined
  if (cursor && cursor.length > 512) {
    cursor = undefined
    hadInvalid = true
  }
  const limitValue = readSingle(params, 'limit')
  hadInvalid ||= limitValue.invalid
  const limit = parseLimit(limitValue.value)
  hadInvalid ||= limit.invalid

  const query: RunSummaryQuery = {
    statuses: statuses.values,
    modes: modes.values,
    limit: limit.value,
    ...(workflowID.value ? { workflowId: workflowID.value } : {}),
    ...(startedAfter ? { startedAfter } : {}),
    ...(startedBefore ? { startedBefore } : {}),
    ...(runID.value ? { runId: runID.value } : {}),
    ...(cursor ? { cursor } : {}),
  }
  return { query, params: writeRunSearch(query), hadInvalid }
}

export function writeRunSearch(query: RunSummaryQuery): URLSearchParams {
  const params = new URLSearchParams()
  if (query.workflowId) params.set('workflowId', query.workflowId.toLowerCase())
  for (const status of sortedUnique(query.statuses)) params.append('status', status)
  for (const mode of sortedUnique(query.modes)) params.append('mode', mode)
  if (query.startedAfter) params.set('startedAfter', query.startedAfter)
  if (query.startedBefore) params.set('startedBefore', query.startedBefore)
  if (query.runId) params.set('runId', query.runId.toLowerCase())
  if (query.cursor) params.set('cursor', query.cursor)
  params.set('limit', String(query.limit))
  return params
}

function hasUnknownKeys(params: URLSearchParams, allowed: Set<string>): boolean {
  for (const key of params.keys()) if (!allowed.has(key)) return true
  return false
}

function readSingle(params: URLSearchParams, key: string): { value?: string; invalid: boolean } {
  const values = params.getAll(key)
  if (values.length > 1) return { invalid: true }
  return { value: values[0], invalid: false }
}

function parseLimit(raw?: string): { value: number; invalid: boolean } {
  if (raw === undefined) return { value: defaultLimit, invalid: false }
  if (!/^[1-9]\d*$/.test(raw)) return { value: defaultLimit, invalid: true }
  const value = Number(raw)
  return value <= 100 ? { value, invalid: false } : { value: defaultLimit, invalid: true }
}

function readUUID(params: URLSearchParams, key: string): { value?: string; invalid: boolean } {
  const raw = readSingle(params, key)
  if (raw.invalid) return raw
  if (raw.value === undefined) return { invalid: false }
  if (!uuidPattern.test(raw.value)) return { invalid: true }
  const value = raw.value.toLowerCase()
  return { value, invalid: value !== raw.value }
}

function readEnums<T extends string>(params: URLSearchParams, key: string, allowed: Set<T>, maximum: number): { values: T[]; invalid: boolean } {
  const raw = params.getAll(key)
  if (raw.length > maximum) return { values: [], invalid: true }
  let invalid = false
  const values: T[] = []
  for (const value of raw) {
    if (!allowed.has(value as T)) invalid = true
    else values.push(value as T)
  }
  const normalized = sortedUnique(values)
  invalid ||= normalized.length !== values.length || normalized.some((value, index) => value !== values[index])
  return { values: normalized, invalid }
}

function parseRFC3339(raw?: string): { value?: string; invalid: boolean } {
  if (raw === undefined) return { invalid: false }
  const match = rfc3339Pattern.exec(raw)
  if (!match) return { invalid: true }
  const [, yearRaw, monthRaw, dayRaw, hourRaw, minuteRaw, secondRaw, zone] = match
  const year = Number(yearRaw)
  const month = Number(monthRaw)
  const day = Number(dayRaw)
  const hour = Number(hourRaw)
  const minute = Number(minuteRaw)
  const second = Number(secondRaw)
  const zoneHour = zone === 'Z' ? 0 : Number(zone.slice(1, 3))
  const zoneMinute = zone === 'Z' ? 0 : Number(zone.slice(4, 6))
  const daysInMonth = new Date(Date.UTC(year, month, 0)).getUTCDate()
  if (month < 1 || month > 12 || day < 1 || day > daysInMonth || hour > 23 || minute > 59 || second > 59 || zoneHour > 23 || zoneMinute > 59) return { invalid: true }
  const timestamp = Date.parse(raw)
  if (!Number.isFinite(timestamp)) return { invalid: true }
  const value = new Date(timestamp).toISOString()
  return { value, invalid: value !== raw }
}

function sortedUnique<T extends string>(values: T[]): T[] {
  return [...new Set(values)].sort()
}
