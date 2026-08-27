import { useEffect, useRef, useState } from 'react'

import { Button } from '../../components/ui/Button'
import type { StudioEdge, StudioNode } from './types'
import type {
  BoundaryDiagnosis,
  BoundaryRepairSelection,
} from './workflowBoundaries'

export interface BoundaryRepairBannerProps {
  diagnosis: BoundaryDiagnosis
  nodes: StudioNode[]
  edges: StudioEdge[]
  busy: boolean
  error: string
  onConfirm: (
    selection: BoundaryRepairSelection,
  ) => void | Promise<void>
}

export function BoundaryRepairBanner(props: BoundaryRepairBannerProps) {
  const [open, setOpen] = useState(false)
  const [selection, setSelection] = useState<BoundaryRepairSelection>({})
  const triggerRef = useRef<HTMLButtonElement>(null)
  const wasOpen = useRef(false)

  useEffect(() => {
    if (wasOpen.current && !open) triggerRef.current?.focus()
    wasOpen.current = open
  }, [open])

  useEffect(() => {
    if (!props.diagnosis.healthy) return
    setOpen(false)
    setSelection({})
  }, [props.diagnosis.healthy])

  if (props.diagnosis.healthy) return null

  const duplicateProblems = props.diagnosis.problems.filter(
    (problem) => problem.issue === 'duplicate',
  )
  const missingCount = props.diagnosis.problems.filter(
    (problem) => problem.issue === 'missing',
  ).length
  const selectionComplete = duplicateProblems.every((problem) =>
    problem.kind === 'start'
      ? Boolean(selection.keepStartId)
      : Boolean(selection.keepEndId),
  )
  const impact = repairImpact(
    props.diagnosis,
    props.edges,
    selection,
  )

  const openDialog = () => {
    setSelection({})
    setOpen(true)
  }

  return (
    <>
      <aside className="boundary-repair-banner" aria-label="工作流边界异常">
        <div>
          <strong>工作流边界需要修复</strong>
          <span>每个工作流必须且只能包含一个开始节点和一个结束节点。</span>
        </div>
        <Button ref={triggerRef} variant="primary" onClick={openDialog}>
          修复工作流边界
        </Button>
      </aside>

      {open && (
        <div className="dialog-backdrop">
          <dialog
            open
            className="boundary-repair-dialog"
            aria-labelledby="boundary-repair-title"
            aria-describedby="boundary-repair-impact"
          >
            <h3 id="boundary-repair-title">修复工作流边界</h3>
            <p>
              缺失的边界节点会添加到画布安全位置；重复节点需要选择一个保留。
            </p>

            {duplicateProblems.map((problem) => (
              <fieldset key={problem.kind}>
                <legend>
                  选择要保留的{problem.kind === 'start' ? '开始' : '结束'}节点
                </legend>
                {problem.nodeIds.map((nodeId) => (
                  <label key={nodeId}>
                    <input
                      type="radio"
                      name={`keep-${problem.kind}`}
                      value={nodeId}
                      checked={
                        problem.kind === 'start'
                          ? selection.keepStartId === nodeId
                          : selection.keepEndId === nodeId
                      }
                      onChange={() =>
                        setSelection((current) =>
                          problem.kind === 'start'
                            ? { ...current, keepStartId: nodeId }
                            : { ...current, keepEndId: nodeId },
                        )
                      }
                    />
                    保留 {nodeId}
                  </label>
                ))}
              </fieldset>
            ))}

            <p id="boundary-repair-impact">
              {impact.removedNodes > 0 &&
                `将移除 ${impact.removedNodes} 个重复节点和 ${impact.removedEdges} 条关联连线。`}
              {impact.removedNodes > 0 && missingCount > 0 && ' '}
              {missingCount > 0 && `将补充 ${missingCount} 个缺失边界节点。`}
            </p>
            {props.error && <p role="alert">{props.error}</p>}

            <div className="dialog-actions">
              <Button
                variant="primary"
                disabled={props.busy || !selectionComplete}
                aria-busy={props.busy}
                onClick={() => void props.onConfirm(selection)}
              >
                确认修复
              </Button>
              <Button
                variant="ghost"
                disabled={props.busy}
                onClick={() => setOpen(false)}
              >
                取消
              </Button>
            </div>
          </dialog>
        </div>
      )}
    </>
  )
}

function repairImpact(
  diagnosis: BoundaryDiagnosis,
  edges: StudioEdge[],
  selection: BoundaryRepairSelection,
) {
  const removedNodeIds = new Set<string>()
  for (const problem of diagnosis.problems) {
    if (problem.issue !== 'duplicate') continue
    const selectedKeeper =
      problem.kind === 'start'
        ? selection.keepStartId
        : selection.keepEndId
    const keeper = selectedKeeper ?? problem.nodeIds[0]
    for (const nodeId of problem.nodeIds) {
      if (nodeId !== keeper) removedNodeIds.add(nodeId)
    }
  }
  return {
    removedNodes: removedNodeIds.size,
    removedEdges: edges.filter(
      (edge) =>
        removedNodeIds.has(edge.source) || removedNodeIds.has(edge.target),
    ).length,
  }
}
