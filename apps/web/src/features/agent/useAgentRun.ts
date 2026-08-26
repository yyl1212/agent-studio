import { useCallback, useEffect, useRef, useState } from 'react'

import { APIError, api, type AgentManifest, type AgentRunPublicEvent, type AgentRunPublicView } from '../../lib/api/client'

export type AgentRunPhase = 'idle' | 'recovering' | 'starting' | 'running' | 'reconnecting' | 'cancelling' | 'completed' | 'failed' | 'cancelled'
export interface UseAgentRunOptions { slug: string; runId?: string; onAccepted(runId: string): void }
export interface AgentRunController {
  phase: AgentRunPhase
  view?: AgentRunPublicView
  events: AgentRunPublicEvent[]
  error?: string
  start(manifest: AgentManifest, input: Record<string, unknown>): Promise<void>
  cancel(): Promise<void>
}

const RETRY_DELAYS_MS = [1000, 2000, 4000, 8000, 10000] as const
const POLL_DELAY_MS = 1000
const terminal = new Set<AgentRunPhase>(['completed', 'failed', 'cancelled'])

export function useAgentRun({ slug, runId, onAccepted }: UseAgentRunOptions): AgentRunController {
  const [phase, setPhase] = useState<AgentRunPhase>(runId ? 'recovering' : 'idle')
  const [view, setView] = useState<AgentRunPublicView>()
  const [events, setEvents] = useState<AgentRunPublicEvent[]>([])
  const eventsRef = useRef<AgentRunPublicEvent[]>([])
  const [error, setError] = useState<string>()
  const generation = useRef(0)
  const controller = useRef<AbortController | undefined>(undefined)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const sequence = useRef(0)
  const activeRunID = useRef<string | undefined>(undefined)
  const retryIndex = useRef(0)
  const cancelling = useRef(false)
  const requestIdentity = useRef<{ signature: string; key: string } | undefined>(undefined)
  const onAcceptedRef = useRef(onAccepted)
  onAcceptedRef.current = onAccepted

  const stopRequest = useCallback(() => {
    if (timer.current) clearTimeout(timer.current)
    timer.current = undefined
    controller.current?.abort()
    controller.current = undefined
  }, [])

  const poll = useCallback(async (expectedGeneration: number): Promise<void> => {
    const id = activeRunID.current
    if (!id || expectedGeneration !== generation.current) return
    controller.current?.abort()
    const requestController = new AbortController()
    controller.current = requestController
    try {
      const next = await api.getAgentRunView(slug, id, sequence.current, requestController.signal)
      if (expectedGeneration !== generation.current || requestController.signal.aborted) return
      retryIndex.current = 0
      sequence.current = Math.max(sequence.current, next.nextSequence)
      const bySequence = new Map(eventsRef.current.map((event) => [event.sequence, event]))
      for (const event of next.events) if (!bySequence.has(event.sequence)) bySequence.set(event.sequence, event)
      const merged = [...bySequence.values()].sort((left, right) => left.sequence - right.sequence)
      eventsRef.current = merged
      setEvents(merged)
      setView({ ...next, events: merged })
      setError(undefined)
      const status = next.run.status as AgentRunPhase
      setPhase(status)
      cancelling.current = status === 'cancelling'
      if (terminal.has(status)) return
      const schedule = () => { timer.current = setTimeout(() => { void poll(expectedGeneration) }, POLL_DELAY_MS) }
      if (next.hasMore) queueMicrotask(() => { void poll(expectedGeneration) })
      else schedule()
    } catch (caught) {
      if (expectedGeneration !== generation.current || requestController.signal.aborted) return
      if (isPermanent(caught)) {
        setPhase('failed')
        setError(publicRunError(caught))
        return
      }
      setPhase('reconnecting')
      setError('连接暂时中断，正在重试…')
      const delay = RETRY_DELAYS_MS[Math.min(retryIndex.current, RETRY_DELAYS_MS.length - 1)]
      retryIndex.current += 1
      timer.current = setTimeout(() => { void poll(expectedGeneration) }, delay)
    }
  }, [slug])

  useEffect(() => {
    generation.current += 1
    const currentGeneration = generation.current
    stopRequest()
    sequence.current = 0
    retryIndex.current = 0
    cancelling.current = false
    activeRunID.current = runId
    setView(undefined)
    eventsRef.current = []
    setEvents([])
    setError(undefined)
    if (runId) {
      setPhase('recovering')
      void poll(currentGeneration)
    } else {
      setPhase('idle')
    }
    return () => {
      if (generation.current === currentGeneration) generation.current += 1
      stopRequest()
    }
  }, [runId, slug, poll, stopRequest])

  const start = useCallback(async (manifest: AgentManifest, input: Record<string, unknown>) => {
    const signature = `${manifest.workflowVersionId}:${stableJSONStringify(input)}`
    if (requestIdentity.current?.signature !== signature) requestIdentity.current = { signature, key: crypto.randomUUID() }
    const identity = requestIdentity.current
    const expectedGeneration = generation.current
    stopRequest()
    setPhase('starting')
    setError(undefined)
    const requestController = new AbortController()
    controller.current = requestController
    try {
      const accepted = await api.startAgentRun(slug, { workflowVersionId: manifest.workflowVersionId, input }, identity.key, requestController.signal)
      if (expectedGeneration !== generation.current || requestController.signal.aborted) return
      requestIdentity.current = undefined
      activeRunID.current = accepted.runId
      sequence.current = 0
      retryIndex.current = 0
      setPhase(accepted.status as AgentRunPhase)
      onAcceptedRef.current(accepted.runId)
      await poll(expectedGeneration)
    } catch (caught) {
      if (expectedGeneration !== generation.current || requestController.signal.aborted) return
      setPhase('failed')
      setError(publicRunError(caught))
    }
  }, [poll, slug, stopRequest])

  const cancel = useCallback(async () => {
    const id = activeRunID.current
    if (!id || cancelling.current || terminal.has(phase)) return
    cancelling.current = true
    setPhase('cancelling')
    setError(undefined)
    const expectedGeneration = generation.current
    stopRequest()
    const requestController = new AbortController()
    controller.current = requestController
    try {
      await api.cancelAgentRun(slug, id, requestController.signal)
      if (expectedGeneration !== generation.current || requestController.signal.aborted) return
      timer.current = setTimeout(() => { void poll(expectedGeneration) }, POLL_DELAY_MS)
    } catch (caught) {
      if (expectedGeneration !== generation.current || requestController.signal.aborted) return
      cancelling.current = false
      setPhase('failed')
      setError(publicRunError(caught))
    }
  }, [phase, poll, slug, stopRequest])

  return { phase, view, events, error, start, cancel }
}

function isPermanent(error: unknown) {
  return error instanceof APIError && [400, 404, 422].includes(error.status)
}

function publicRunError(error: unknown) {
  if (error instanceof APIError) {
    if (error.status === 404) return '运行不存在或不属于该 Agent'
    if (error.status === 400 || error.status === 422) return error.message || '运行请求无效'
    if (error.status === 429) return '当前运行较多，请稍后重试'
    if (error.status === 503) return '服务暂时不可用，请稍后重试'
  }
  return '运行失败，请重试'
}

function stableJSONStringify(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(stableJSONStringify).join(',')}]`
  if (value !== null && typeof value === 'object') {
    return `{${Object.keys(value as Record<string, unknown>).sort().map((key) => `${JSON.stringify(key)}:${stableJSONStringify((value as Record<string, unknown>)[key])}`).join(',')}}`
  }
  const serialized = JSON.stringify(value)
  if (serialized === undefined) throw new Error('non-json value')
  return serialized
}
