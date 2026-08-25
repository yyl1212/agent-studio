import type { DebugOverview, RerunPreview } from '../../lib/api/client'
import type { RunEvent } from '../../lib/api/ndjson'
import { NodeRunDetail } from './NodeRunDetail'
import { RunTimeline } from './RunTimeline'
import { PartialRerunForm } from './PartialRerunForm'

export interface DebugWorkbenchProps {
	overview: DebugOverview
	events: RunEvent[]
	selectedSequence?: number
	selectedNodeID?: string
	onSelectSequence: (sequence: number) => void
	onSelectNode: (nodeID: string) => void
	onStartRerun?: (nodeID: string) => void
	rerunPreview?: RerunPreview
	rerunEvents?: RunEvent[]
	rerunRunning?: boolean
	rerunError?: string
	debugRunPath?: string
	onSubmitRerun?: (entryInput: Record<string, unknown>, confirmed: boolean) => void | Promise<void>
	onCancelRerun?: () => void
	onCloseRerun?: () => void
}

export function DebugWorkbench(props: DebugWorkbenchProps) {
	if (props.rerunPreview && props.onSubmitRerun && props.onCancelRerun && props.onCloseRerun) {
		return (
			<aside className="debug-workbench rerun-workbench" aria-label="调试工作台">
				<PartialRerunForm preview={props.rerunPreview} events={props.rerunEvents ?? []} running={props.rerunRunning ?? false} error={props.rerunError ?? ''} debugRunPath={props.debugRunPath} onSubmit={props.onSubmitRerun} onCancel={props.onCancelRerun} onClose={props.onCloseRerun} />
			</aside>
		)
	}
	const currentEvent = props.events.find((event) => event.sequence === props.selectedSequence)
	const selectEvent = (event: RunEvent) => {
		props.onSelectSequence(event.sequence)
		if (event.nodeId) props.onSelectNode(event.nodeId)
	}
	return (
		<aside className="debug-workbench" aria-label="调试工作台">
			<p className="sr-status" aria-live="polite">
				{currentEvent ? `已定位事件 ${currentEvent.sequence}：${currentEvent.type}` : '未选择事件'}
			</p>
			<RunTimeline events={props.events} selectedSequence={props.selectedSequence} onSelect={selectEvent} />
			<div className="debug-detail-stack">
				{props.rerunError && <p className="form-error" role="alert">{props.rerunError}</p>}
				<NodeRunDetail nodeID={props.selectedNodeID} events={props.events} onRerun={props.overview.rerunAvailable ? props.onStartRerun : undefined} />
			</div>
		</aside>
	)
}
