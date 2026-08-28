import { useRef, type Ref } from 'react'
import { Link } from 'react-router-dom'

import { Button } from '../../components/ui/Button'
import type { SaveState } from './saveQueue'

export interface StudioCommandBarProps {
  workflowName: string
  saveState: SaveState
  archived: boolean
  exporting: boolean
  runsHref: string
  actionError: string
  invalidEdgeCount?: number
  testLabel: string
  testDisabled: boolean
  onTest: () => void
  onPublish: () => void
  onAgentPresentation: () => void
  onVersionHistory: () => void
  onExport: () => void
  onRetrySave?: () => void
  onRefreshConflict?: () => void
  testButtonRef?: Ref<HTMLButtonElement>
  moreActionsTriggerRef?: Ref<HTMLElement>
  moreActionsOpen?: boolean
  onMoreActionsOpenChange?: (open: boolean) => void
}

export function StudioCommandBar(props: StudioCommandBarProps) {
  const saveBlocked = props.saveState === 'error' || props.saveState === 'conflict'
  const moreActionsRef = useRef<HTMLDetailsElement>(null)
  const runMenuAction = (action: () => void) => {
    if (moreActionsRef.current) moreActionsRef.current.open = false
    props.onMoreActionsOpenChange?.(false)
    action()
  }
  return <header className="studio-command-bar">
    <div className="studio-title">
      <Link to="/workflows" aria-label="返回工作流列表">←</Link>
      <div>
        <strong>{props.workflowName}</strong>
        <small>{saveLabel(props.saveState)}</small>
        {props.saveState === 'error' && props.onRetrySave && <button className="studio-save-recovery" type="button" onClick={props.onRetrySave}>重试保存</button>}
        {props.saveState === 'conflict' && props.onRefreshConflict && <button className="studio-save-recovery" type="button" onClick={props.onRefreshConflict}>刷新工作流</button>}
      </div>
    </div>
    <div className="studio-primary-actions">
      <Button ref={props.testButtonRef} onClick={props.onTest} disabled={props.testDisabled || saveBlocked}>{props.testLabel}</Button>
      <Button variant="primary" onClick={props.onPublish} disabled={props.archived || props.testDisabled || saveBlocked}>发布</Button>
      <details ref={moreActionsRef} className="studio-more-actions" open={props.moreActionsOpen} onToggle={(event) => props.onMoreActionsOpenChange?.(event.currentTarget.open)}>
        <summary ref={props.moreActionsTriggerRef}>更多操作</summary>
        <div className="studio-more-menu">
          <Link to={props.runsHref} onClick={() => { if (moreActionsRef.current) moreActionsRef.current.open = false; props.onMoreActionsOpenChange?.(false) }}>运行记录</Link>
          <Button variant="ghost" onClick={() => runMenuAction(props.onAgentPresentation)} disabled={props.archived}>Agent 页面设置</Button>
          <Button variant="ghost" onClick={() => runMenuAction(props.onVersionHistory)}>版本历史</Button>
          <Button variant="ghost" onClick={() => runMenuAction(props.onExport)} disabled={props.exporting}>{props.exporting ? '导出中…' : '导出模板'}</Button>
        </div>
      </details>
    </div>
    {props.actionError && <p className="studio-command-error" role="alert">{props.actionError}</p>}
    {Boolean(props.invalidEdgeCount) && <p className="studio-command-error studio-invalid-edges" role="status">存在 {props.invalidEdgeCount} 条失效连线，请修复后测试或发布</p>}
  </header>
}

function saveLabel(state: SaveState) {
  return { saved: '已保存', pending: '等待保存', saving: '保存中…', conflict: '保存冲突，请刷新', error: '保存失败' }[state]
}
