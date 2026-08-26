import type { AgentRunPublicEvent, AgentRunPublicView } from '../../lib/api/client'
import { AgentResult } from './AgentResult'
import type { AgentRunPhase } from './useAgentRun'

interface AgentRunViewProps {
  phase: AgentRunPhase
  view?: AgentRunPublicView
  events: AgentRunPublicEvent[]
  error?: string
  onCancel(): void
  onRestart(): void
}

export function AgentRunView({ phase, view, events, error, onCancel, onRestart }: AgentRunViewProps) {
  const started = events.filter((event) => event.type === 'node.started').length
  const ended = events.filter((event) => ['node.completed', 'node.failed', 'node.skipped', 'node.cancelled'].includes(event.type)).length
  const status = phaseLabel(phase)
  const canCancel = ['recovering', 'running', 'reconnecting'].includes(phase)
  const final = ['completed', 'failed', 'cancelled'].includes(phase)

  return <section className={`agent-run-card status-${phase}`} aria-live="polite">
    <header><span className="agent-run-status">{status}</span>{started > 0 && <span>已结束 {ended} / 已开始 {started}</span>}</header>
    {phase === 'reconnecting' && <p>连接暂时中断，正在重试…</p>}
    {error && <p role="alert">{error}</p>}
    {phase === 'failed' && view?.run.error && !error && <p role="alert">{view.run.error.message}（{view.run.error.code}）</p>}
    {phase === 'completed' && view && <AgentResult value={view.run.output} mode={view.presentation.resultMode} />}
    <div className="agent-run-actions">
      {canCancel && <button type="button" onClick={onCancel}>取消运行</button>}
      {phase === 'cancelling' && <button type="button" disabled>正在取消…</button>}
      {final && <button type="button" onClick={onRestart}>再次运行</button>}
    </div>
  </section>
}

function phaseLabel(phase: AgentRunPhase) {
  if (phase === 'idle') return '等待运行'
  if (phase === 'recovering') return '正在恢复运行'
  if (phase === 'starting') return '正在启动'
  if (phase === 'running') return '正在运行'
  if (phase === 'reconnecting') return '正在重新连接'
  if (phase === 'cancelling') return '正在取消'
  if (phase === 'completed') return '运行完成'
  if (phase === 'failed') return '运行失败'
  return '运行已取消'
}
