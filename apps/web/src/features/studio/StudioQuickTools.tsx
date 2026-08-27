import type { Ref } from 'react'

import { Button } from '../../components/ui/Button'

export interface StudioQuickToolsProps {
  disabled: boolean
  onAdd: () => void
  onFitView: () => void
  addButtonRef?: Ref<HTMLButtonElement>
}

export function StudioQuickTools(props: StudioQuickToolsProps) {
  return <nav className="studio-quick-tools" aria-label="画布快捷工具">
    <Button ref={props.addButtonRef} onClick={props.onAdd} disabled={props.disabled}>添加节点</Button>
    <Button onClick={props.onFitView}>适配工作流</Button>
    <details className="studio-shortcut-help">
      <summary>快捷键帮助</summary>
      <div>
        <p><kbd>Ctrl/⌘ + K</kbd> 打开节点库</p>
        <p><kbd>Esc</kbd> 关闭当前浮层</p>
      </div>
    </details>
  </nav>
}
