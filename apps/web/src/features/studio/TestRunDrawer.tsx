import { SchemaForm } from '../../components/schema-form/SchemaForm'
import type { JSONSchema } from '../../components/schema-form/types'
import type { RunEvent } from '../../lib/api/ndjson'

interface TestRunDrawerProps {
  schema: JSONSchema
  events: RunEvent[]
  running: boolean
  error: string
  onRun: (input: Record<string, unknown>) => void
  onCancel: () => void
  onClose: () => void
}

export function TestRunDrawer({ schema, events, running, error, onRun, onCancel, onClose }: TestRunDrawerProps) {
  const completed = [...events].reverse().find((event) => event.type === 'run.completed')
  return (
    <aside className="test-run-drawer" role="dialog" aria-label="测试运行">
      <div className="drawer-heading"><h2>测试运行</h2><button type="button" aria-label="关闭测试运行" onClick={onClose}>×</button></div>
      <div className="test-columns">
        <SchemaForm schema={schema} value={{}} onChange={() => undefined} onSubmit={onRun} submitLabel={running ? '运行中…' : '运行'} disabled={running} />
        <section aria-live="polite">
          <h3>运行进度</h3>
          {events.length === 0 && <p>填写参数后开始运行。</p>}
          <ol>{events.filter((event) => event.nodeId).map((event) => <li key={event.sequence}>{event.nodeId}：{event.type}</li>)}</ol>
          {completed && <pre className="run-output">{formatOutput(completed.output)}</pre>}
          {error && <p className="form-error" role="alert">{error}</p>}
          {running && <button type="button" onClick={onCancel}>取消运行</button>}
        </section>
      </div>
    </aside>
  )
}

function formatOutput(value: unknown) {
  return typeof value === 'string' ? value : JSON.stringify(value, null, 2)
}
