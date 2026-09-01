import { useCallback, useEffect, useRef, useState } from 'react'

import { APIError, api, type RunRecoveryView } from '../../lib/api/client'

type RunRecoveryDialogProps = {
  runID: string
  open: boolean
  onClose: () => void
  onRecovered: () => void
}

export function RunRecoveryDialog({ runID, open, onClose, onRecovered }: RunRecoveryDialogProps) {
  const [view, setView] = useState<RunRecoveryView>()
  const [loading, setLoading] = useState(false)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const [conflict, setConflict] = useState(false)
  const closeButton = useRef<HTMLButtonElement>(null)

  const load = useCallback(async (signal?: AbortSignal) => {
    setLoading(true)
    setView(undefined)
    try {
      const loaded = await api.getRunRecovery(runID, signal)
      if (!signal?.aborted) setView(loaded)
    } catch (cause) {
      if (!signal?.aborted) setError(publicRecoveryError(cause, '加载恢复详情失败'))
    } finally {
      if (!signal?.aborted) setLoading(false)
    }
  }, [runID])

  useEffect(() => {
    if (!open) return
    const controller = new AbortController()
    setView(undefined)
    setError('')
    setConflict(false)
    setPending(false)
    void load(controller.signal)
    queueMicrotask(() => closeButton.current?.focus())
    return () => controller.abort()
  }, [open, load])

  if (!open) return null
  const node = view?.nodes.find((item) => item.retryAllowed)

  const mutate = async (action: 'retry' | 'terminate') => {
    if (!view || pending || (action === 'retry' && !node)) return
    setPending(true)
    setError('')
    setConflict(false)
    try {
      const summary = action === 'retry'
        ? await api.confirmRunNodeRetry(runID, node!.nodeId, node!.nodeAttempt, view.sequence)
        : await api.terminateRunRecovery(runID, view.sequence)
      if (summary.status === 'recovery_required') await load()
      else {
        onRecovered()
        onClose()
      }
    } catch (cause) {
      if (cause instanceof APIError && cause.code === 'RUN_RECOVERY_CONFLICT') {
        setConflict(true)
        await load()
      } else setError(publicRecoveryError(cause, action === 'retry' ? '确认重试失败' : '终止运行失败'))
    } finally {
      setPending(false)
    }
  }

  return <div className="dialog-backdrop run-recovery-backdrop">
    <dialog open className="run-recovery-dialog" aria-labelledby="run-recovery-title">
      <header><div><p className="eyebrow">RECOVERY</p><h3 id="run-recovery-title">处理运行恢复</h3></div><button ref={closeButton} type="button" disabled={pending} onClick={onClose} aria-label="关闭恢复对话框">×</button></header>
      {loading && !view && <p aria-live="polite">正在加载恢复详情…</p>}
      {conflict && <p className="recovery-conflict" role="alert">状态已变化，请重新确认</p>}
      {error && <p role="alert">{error}</p>}
      {view && <div className="run-recovery-column">
        <section className="recovery-risk"><strong>{reasonLabel(view.reason)}</strong><p>{node?.riskMessage ?? '当前运行无法安全自动恢复，请终止后重新提交。'}</p></section>
        <section><h4>待处理节点</h4>{view.nodes.length === 0 ? <p>没有可确认重试的节点。</p> : view.nodes.map((item) => <article key={`${item.nodeId}:${item.nodeAttempt}`}>
          <strong>{item.nodeTitle || item.nodeId}</strong><span>{item.nodeType || item.nodeId}</span>
          <dl><div><dt>节点</dt><dd>{item.nodeId}</dd></div><div><dt>Attempt</dt><dd>{item.nodeAttempt}</dd></div><div><dt>安全等级</dt><dd>{safetyLabel(item.safety)}</dd></div></dl>
          <p>{item.riskMessage}</p>{!item.retryAllowed && <small>{item.retryBlockReason || '不可确认重试'}</small>}
        </article>)}</section>
        <footer>
          <button type="button" className="primary-button" disabled={pending || !node} onClick={() => void mutate('retry')}>{pending ? '正在提交…' : '确认重试当前节点'}</button>
          <button type="button" className="danger-button" disabled={pending} onClick={() => void mutate('terminate')}>终止运行</button>
        </footer>
      </div>}
    </dialog>
  </div>
}

function reasonLabel(reason: RunRecoveryView['reason']) {
  return ({ legacy_active_run: '旧版本活动运行', uncertain_read_only: '只读节点状态不确定', uncertain_side_effect: '外部副作用状态不确定', attempt_limit_reached: '节点重试次数已用尽', payload_unavailable: '运行负载不可用', event_history_invalid: '运行历史不完整', node_version_unavailable: '节点版本不可用' })[reason]
}
function safetyLabel(safety: RunRecoveryView['nodes'][number]['safety']) { return ({ pure: '纯计算', read_only: '只读', side_effect: '副作用' })[safety] }
function publicRecoveryError(error: unknown, fallback: string) { return error instanceof APIError ? `${error.message}${error.requestId ? ` · Request ID: ${error.requestId}` : ''}` : fallback }
