import type { RunEvent } from '../../lib/api/ndjson'

export function RunProgress({ events }: { events: RunEvent[] }) {
  const nodes = events.filter((event) => event.nodeId)
  if (nodes.length === 0) return null
  return (
    <section className="run-progress" aria-label="运行进度">
      <h3>运行进度</h3>
      <ol>{nodes.map((event) => (
        <li key={event.sequence} data-status={event.status ?? event.type}>
          <span>{event.nodeId}</span><small>{eventLabel(event.type)}</small>
        </li>
      ))}</ol>
    </section>
  )
}

function eventLabel(type: RunEvent['type']) {
  return ({
    'run.started': '运行开始', 'node.started': '执行中', 'node.completed': '已完成', 'node.failed': '失败',
    'node.skipped': '已跳过', 'node.cancelled': '已取消', 'run.completed': '运行完成', 'run.failed': '运行失败', 'run.cancelled': '运行取消',
  } as const)[type]
}
