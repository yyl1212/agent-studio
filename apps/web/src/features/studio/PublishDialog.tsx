import { Link } from 'react-router-dom'

interface PublishDialogProps {
  slug: string
  version?: number
  error: string
  publishing: boolean
  onConfirm: () => void
  onClose: () => void
}

export function PublishDialog({ slug, version, error, publishing, onConfirm, onClose }: PublishDialogProps) {
  return (
    <div className="dialog-backdrop">
      <dialog open aria-labelledby="publish-title">
        <h3 id="publish-title">发布工作流</h3>
        {version ? (
          <><p>版本 v{version} 已发布。</p><Link className="primary-button inline" to={`/agents/${slug}`}>打开 Agent 页面</Link></>
        ) : <p>发布会创建不可变版本，并更新 Agent 页面。</p>}
        {error && <p className="form-error" role="alert">{error}</p>}
        <div className="dialog-actions">
          <button type="button" onClick={onClose}>{version ? '完成' : '取消'}</button>
          {!version && <button className="primary-button" type="button" disabled={publishing} onClick={onConfirm}>{publishing ? '发布中…' : '确认发布'}</button>}
        </div>
      </dialog>
    </div>
  )
}
