import { useCallback, useEffect, useRef, useState } from 'react'

import { APIError, api, type RunSummaryPage, type RunSummaryQuery } from '../../lib/api/client'

export interface RunListState {
  page: RunSummaryPage | null
  loading: boolean
  error: string
  reload: () => void
}

export function useRunList(query: RunSummaryQuery): RunListState {
  const [page, setPage] = useState<RunSummaryPage | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [reloadGeneration, setReloadGeneration] = useState(0)
  const generation = useRef(0)

  useEffect(() => {
    const mine = ++generation.current
    let active = true
    const controller = new AbortController()
    setLoading(true)
    setError('')
    api.listRunSummaries(query, controller.signal).then((nextPage) => {
      if (active && generation.current === mine && !controller.signal.aborted) setPage(nextPage)
    }).catch((cause: unknown) => {
      if (active && generation.current === mine && !isAbort(cause)) setError(publicError(cause))
    }).finally(() => {
      if (active && generation.current === mine) setLoading(false)
    })
    return () => {
      active = false
      controller.abort()
    }
  }, [query.workflowId, query.statuses.join(','), query.modes.join(','), query.startedAfter, query.startedBefore, query.runId, query.cursor, query.limit, reloadGeneration])

  return { page, loading, error, reload: useCallback(() => setReloadGeneration((value) => value + 1), []) }
}

function isAbort(error: unknown) { return error instanceof DOMException && error.name === 'AbortError' }
function publicError(error: unknown) {
  return error instanceof APIError ? `${error.message}${error.requestId ? ` · Request ID: ${error.requestId}` : ''}` : '加载运行记录失败，请稍后重试'
}
