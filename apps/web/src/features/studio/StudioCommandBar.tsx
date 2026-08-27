import type { Ref } from 'react'
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
  versionButtonRef?: Ref<HTMLButtonElement>
}

export function StudioCommandBar(props: StudioCommandBarProps) {
  const saveBlocked = props.saveState === 'error' || props.saveState === 'conflict'
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
      <details className="studio-more-actions">
        <summary>更多操作</summary>
        <div className="studio-more-menu">
          <Link to={props.runsHref}>运行记录</Link>
          <Button variant="ghost" onClick={props.onAgentPresentation} disabled={props.archived}>Agent 页面设置</Button>
          <Button ref={props.versionButtonRef} variant="ghost" onClick={props.onVersionHistory}>版本历史</Button>
          <Button variant="ghost" onClick={props.onExport} disabled={props.exporting}>{props.exporting ? '导出中…' : '导出模板'}</Button>
        </div>
      </details>
    </div>
    {props.actionError && <p className="studio-command-error" role="alert">{props.actionError}</p>}
  </header>
}

function saveLabel(state: SaveState) {
  return { saved: '已保存', pending: '等待保存', saving: '保存中…', conflict: '保存冲突，请刷新', error: '保存失败' }[state]
}
