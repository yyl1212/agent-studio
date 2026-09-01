import type { AgentRunPublicEvent, AgentRunPublicView } from '../../lib/api/client'
import { AgentResult } from './AgentResult'
import type { AgentRunPhase, AgentServicePhase } from './useAgentRun'

interface AgentRunViewProps {
  phase: AgentRunPhase
  servicePhase?: AgentServicePhase
  view?: AgentRunPublicView
  events: AgentRunPublicEvent[]
  error?: string
  onCancel(): void
  onRestart(): void
}

export function AgentRunView({ phase, servicePhase, view, events, error, onCancel, onRestart }: AgentRunViewProps) {
  const started = events.filter((event) => event.type === 'node.started').length
  const ended = events.filter((event) => ['node.completed', 'node.failed', 'node.skipped', 'node.cancelled'].includes(event.type)).length
  const status = phaseLabel(phase)
  const currentServicePhase = servicePhase ?? view?.run.status
  const cancellableService = currentServicePhase === 'queued' || currentServicePhase === 'running'
  const canCancel = cancellableService && (phase === 'queued' || phase === 'running' || phase === 'reconnecting')
  const final = ['completed', 'failed', 'cancelled'].includes(phase)

  return <section className={`agent-run-card status-${phase}`} aria-live="polite">
    <header><span className="agent-run-status">{status}</span>{started > 0 && <span>已结束 {ended} / 已开始 {started}</span>}</header>
    {phase === 'reconnecting' && <p>连接暂时中断，正在重试…</p>}
    {phase === 'recovery_required' && <p className="agent-recovery-notice">运行需要管理员确认，结果会在处理后继续更新。</p>}
    {error && <p role="alert">{error}</p>}
    {phase === 'failed' && view?.run.error && !error && <p role="alert">{view.run.error.message}（{view.run.error.code}）</p>}
    {phase === 'completed' && view && <AgentResult value={view.run.output} mode={view.presentation.resultMode} />}
    {(phase === 'failed' || phase === 'cancelled') && view && <p className="agent-run-id">运行 ID：<code>{view.run.runId}</code></p>}
    <div className="agent-run-actions">
      {canCancel && <button type="button" onClick={onCancel}>取消运行</button>}
      {phase === 'cancelling' && <button type="button" disabled>正在取消…</button>}
      {final && <button type="button" onClick={onRestart}>再次运行</button>}
    </div>
  </section>
}

function phaseLabel(phase: AgentRunPhase) {
  switch (phase) {
    case 'idle': return '等待运行'
    case 'recovering': return '正在恢复运行'
    case 'starting': return '正在启动'
    case 'reconnecting': return '正在重新连接'
    case 'queued': return '正在排队'
    case 'running': return '正在运行'
    case 'recovery_required': return '等待管理员处理'
    case 'cancelling': return '正在取消'
    case 'completed': return '运行完成'
    case 'failed': return '运行失败'
    case 'cancelled': return '运行已取消'
    default: return assertNever(phase)
  }
}

function assertNever(value: never): never {
  throw new Error(`unsupported agent run phase: ${String(value)}`)
}
