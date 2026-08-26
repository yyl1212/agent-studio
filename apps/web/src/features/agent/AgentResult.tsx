import { useEffect, useMemo, useState } from 'react'

import type { AgentPresentation } from '../../lib/api/client'

interface AgentResultProps { value: unknown; mode: AgentPresentation['resultMode'] }

export function AgentResult({ value, mode }: AgentResultProps) {
  const content = useMemo(() => formatAgentResult(value, mode), [mode, value])
  const [copyState, setCopyState] = useState<'idle' | 'copied' | 'failed'>('idle')
  useEffect(() => setCopyState('idle'), [content])

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(content)
      setCopyState('copied')
    } catch {
      setCopyState('failed')
    }
  }

  return <section className="agent-result" role="region" aria-label="运行结果">
    <header><h3>运行结果</h3><button type="button" onClick={() => { void copy() }} aria-label="复制结果">复制</button></header>
    <pre>{content}</pre>
    {copyState === 'copied' && <span role="status">已复制</span>}
    {copyState === 'failed' && <span role="alert">复制失败</span>}
  </section>
}

export function formatAgentResult(value: unknown, mode: AgentPresentation['resultMode']): string {
  if ((mode === 'auto' || mode === 'text') && typeof value === 'string') return value
  try {
    const safe = cloneJSON(value, new Set())
    return JSON.stringify(safe, null, mode === 'text' ? undefined : 2)
  } catch {
    return '结果无法安全序列化'
  }
}

function cloneJSON(value: unknown, ancestors: Set<object>): unknown {
  if (value === null || typeof value === 'string' || typeof value === 'boolean') return value
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) throw new Error('non-json number')
    return value
  }
  if (typeof value !== 'object') throw new Error('non-json value')
  if (ancestors.has(value)) throw new Error('cycle')
  ancestors.add(value)
  try {
    if (Array.isArray(value)) return value.map((item) => cloneJSON(item, ancestors))
    const prototype = Object.getPrototypeOf(value)
    if (prototype !== Object.prototype && prototype !== null) throw new Error('non-json object')
    const source = value as Record<string, unknown>
    return Object.fromEntries(Object.keys(source).sort().map((key) => [key, cloneJSON(source[key], ancestors)]))
  } finally {
    ancestors.delete(value)
  }
}
