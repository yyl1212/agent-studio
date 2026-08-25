import { SchemaForm } from '../../components/schema-form/SchemaForm'
import type { JSONSchema } from '../../components/schema-form/types'
import type { RunEvent } from '../../lib/api/ndjson'

interface TestRunPanelProps {
  schema: JSONSchema
  events: RunEvent[]
  running: boolean
  error: string
  onRun: (input: Record<string, unknown>) => void
  onCancel: () => void
}

export function TestRunPanel({ schema, events, running, error, onRun, onCancel }: TestRunPanelProps) {
  const completed = [...events].reverse().find((event) => event.type === 'run.completed')
  const failed = [...events].reverse().find((event) => event.type === 'node.failed' && event.error)

  return (
    <div className="test-run-panel">
      <div className="test-columns">
        <SchemaForm
          schema={schema}
          value={{}}
          onChange={() => undefined}
          onSubmit={onRun}
          submitLabel={running ? '运行中…' : '运行'}
          disabled={running}
        />
        <section aria-live="polite">
          <h3>运行进度</h3>
          {events.length === 0 && <p>填写参数后开始运行。</p>}
          <ol>{events.filter((event) => event.nodeId).map((event) => <li key={event.sequence}>{event.nodeId}：{event.type}</li>)}</ol>
          {completed && <pre className="run-output">{formatOutput(completed.output)}</pre>}
          {failed?.error && <p className="form-error" role="alert">{failed.error.message}</p>}
          {error && <p className="form-error" role="alert">{error}</p>}
          {running && <button type="button" onClick={onCancel}>取消运行</button>}
        </section>
      </div>
    </div>
  )
}

function formatOutput(value: unknown) {
  return typeof value === 'string' ? value : JSON.stringify(value, null, 2)
}
