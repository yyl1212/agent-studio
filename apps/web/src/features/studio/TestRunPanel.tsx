import { useState } from 'react'

import { SchemaForm } from '../../components/schema-form/SchemaForm'
import type { FormValue, JSONSchema } from '../../components/schema-form/types'
import type { RunEvent } from '../../lib/api/ndjson'

interface TestRunPanelProps {
  schema: JSONSchema
  events: RunEvent[]
  running: boolean
  error: string
  onRun: (input: Record<string, unknown>) => void
  onCancel: () => void
  cancelled?: boolean
}

export function TestRunPanel({ schema, events, running, error, onRun, onCancel, cancelled = false }: TestRunPanelProps) {
  const [input, setInput] = useState<FormValue>({})
  const completed = [...events].reverse().find((event) => event.type === 'run.completed')
  const reversedEvents = [...events].reverse()
  const failed = reversedEvents.find((event) => event.type === 'node.failed' && event.error)
    ?? reversedEvents.find((event) => event.type === 'run.failed' && event.error)
  const cancelledEvent = events.some((event) => event.type === 'run.cancelled')
  const canRetry = !running && Boolean(error || failed || cancelled || cancelledEvent)
  const duration = runDuration(events)

  return (
    <div className="test-run-panel">
      <div className="test-columns">
        <SchemaForm
          schema={schema}
          value={input}
          onChange={setInput}
          onSubmit={onRun}
          submitLabel={running ? '运行中…' : '运行'}
          disabled={running}
        />
        <section aria-live="polite">
          <h3>运行进度</h3>
          {events.length === 0 && <p>填写参数后开始运行。</p>}
          <ol>{events.filter((event) => event.nodeId).map((event) => <li key={event.sequence}>{event.nodeId}：{eventLabel(event.type)}</li>)}</ol>
          {duration !== undefined && <p>耗时 {formatDuration(duration)}</p>}
          {completed && <pre className="run-output">{formatOutput(completed.output)}</pre>}
          {failed?.error && <p className="form-error" role="alert">{failed.error.message}</p>}
          {error && <p className="form-error" role="alert">{error}</p>}
          {(cancelled || cancelledEvent) && <p role="status">运行已取消，已保留收到的事件。</p>}
          {canRetry && <button type="button" onClick={() => onRun(input as Record<string, unknown>)}>重试运行</button>}
          {running && <button type="button" onClick={onCancel}>取消运行</button>}
        </section>
      </div>
    </div>
  )
}

function formatOutput(value: unknown) {
  return typeof value === 'string' ? value : JSON.stringify(value, null, 2)
}

function eventLabel(type: RunEvent['type']) {
  return {
    'run.started': '运行开始',
    'node.started': '运行中',
    'node.completed': '已完成',
    'node.failed': '失败',
    'node.skipped': '已跳过',
    'node.cancelled': '已取消',
    'run.completed': '运行完成',
    'run.failed': '运行失败',
    'run.cancelled': '运行取消',
  }[type]
}

function runDuration(events: RunEvent[]) {
  const started = events.find((event) => event.type === 'run.started')
  const ended = [...events].reverse().find((event) => event.type === 'run.completed' || event.type === 'run.failed' || event.type === 'run.cancelled')
  if (!started || !ended) return undefined
  const startTime = Date.parse(started.timestamp)
  const endTime = Date.parse(ended.timestamp)
  return Number.isFinite(startTime) && Number.isFinite(endTime) && endTime >= startTime ? endTime - startTime : undefined
}

function formatDuration(milliseconds: number) {
  if (milliseconds < 1000) return `${milliseconds} 毫秒`
  return `${(milliseconds / 1000).toFixed(1)} 秒`
}
