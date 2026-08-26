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
	const pageGeneration = useRef(0)
	const inFlight = useRef<AbortController | undefined>(undefined)

	const requestPage = useCallback((target: RunSummaryQuery, mine: number, background: boolean) => {
		if (inFlight.current) return
		const controller = new AbortController()
		inFlight.current = controller
		if (!background) {
			setLoading(true)
			setError('')
		}
		api.listRunSummaries(target, controller.signal).then((nextPage) => {
			if (generation.current !== mine || controller.signal.aborted) return
			pageGeneration.current = mine
			setPage(nextPage)
			setError('')
		}).catch((cause: unknown) => {
			if (generation.current === mine && !controller.signal.aborted && !isAbort(cause)) setError(publicError(cause))
		}).finally(() => {
			if (inFlight.current === controller) inFlight.current = undefined
			if (!background && generation.current === mine && !controller.signal.aborted) setLoading(false)
		})
	}, [])

  useEffect(() => {
    const mine = ++generation.current
		inFlight.current?.abort()
		inFlight.current = undefined
		requestPage(cloneQuery(query), mine, false)
    return () => {
			if (generation.current === mine) {
				inFlight.current?.abort()
				inFlight.current = undefined
			}
    }
	}, [query.workflowId, query.statuses.join(','), query.modes.join(','), query.startedAfter, query.startedBefore, query.runId, query.cursor, query.limit, reloadGeneration, requestPage])

	useEffect(() => {
		const eligible = !query.cursor && pageGeneration.current === generation.current && page?.items.some((run) => run.status === 'running' || run.status === 'cancelling')
		if (!eligible) return
		const mine = generation.current
		const refresh = () => {
			if (document.visibilityState === 'hidden') return
			requestPage({ ...cloneQuery(query), cursor: undefined }, mine, true)
		}
		const interval = window.setInterval(refresh, 3000)
		const visibilityChanged = () => {
			if (document.visibilityState === 'visible') refresh()
		}
		document.addEventListener('visibilitychange', visibilityChanged)
		return () => {
			window.clearInterval(interval)
			document.removeEventListener('visibilitychange', visibilityChanged)
		}
	}, [page, query.workflowId, query.statuses.join(','), query.modes.join(','), query.startedAfter, query.startedBefore, query.runId, query.cursor, query.limit, requestPage])

	return {
		page, loading, error,
		reload: useCallback(() => {
			inFlight.current?.abort()
			inFlight.current = undefined
			setReloadGeneration((value) => value + 1)
		}, []),
	}
}

function cloneQuery(query: RunSummaryQuery): RunSummaryQuery {
	return { ...query, statuses: [...query.statuses], modes: [...query.modes] }
}

function isAbort(error: unknown) { return error instanceof DOMException && error.name === 'AbortError' }
function publicError(error: unknown) {
  return error instanceof APIError ? `${error.message}${error.requestId ? ` · Request ID: ${error.requestId}` : ''}` : '加载运行记录失败，请稍后重试'
}
