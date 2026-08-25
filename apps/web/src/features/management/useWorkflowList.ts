import { useCallback, useEffect, useRef, useState } from 'react'

import { APIError, api, type WorkflowSummaryPage, type WorkflowSummaryQuery } from '../../lib/api/client'

export interface WorkflowListState {
  page: WorkflowSummaryPage | null
  loading: boolean
  error: string
  reload: () => void
}

export function useWorkflowList(query: WorkflowSummaryQuery): WorkflowListState {
  const [page, setPage] = useState<WorkflowSummaryPage | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [reloadGeneration, setReloadGeneration] = useState(0)
  const generation = useRef(0)
  const controller = useRef<AbortController | null>(null)

  useEffect(() => {
    const mine = ++generation.current
    let active = true
    controller.current?.abort()
    const current = new AbortController()
    controller.current = current
    setLoading(true)
    setError('')
    api.listWorkflowSummaries(query, current.signal).then((nextPage) => {
      if (active && generation.current === mine && !current.signal.aborted) setPage(nextPage)
    }).catch((cause: unknown) => {
      if (active && generation.current === mine && !isAbort(cause)) setError(publicError(cause))
    }).finally(() => {
      if (active && generation.current === mine) setLoading(false)
    })
    return () => {
      active = false
      current.abort()
    }
  }, [query.q, query.state, query.cursor, query.limit, reloadGeneration])

  const reload = useCallback(() => setReloadGeneration((value) => value + 1), [])
  return { page, loading, error, reload }
}

function isAbort(error: unknown) {
  return error instanceof DOMException && error.name === 'AbortError'
}

function publicError(error: unknown) {
  if (error instanceof APIError) return `${error.message}${error.requestId ? ` · Request ID: ${error.requestId}` : ''}`
  return '加载工作流失败，请稍后重试'
}
