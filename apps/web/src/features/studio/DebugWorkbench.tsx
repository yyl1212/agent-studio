import type { DebugOverview } from '../../lib/api/client'
import type { RunEvent } from '../../lib/api/ndjson'
import { NodeRunDetail } from './NodeRunDetail'
import { RunTimeline } from './RunTimeline'

export interface DebugWorkbenchProps {
	overview: DebugOverview
	events: RunEvent[]
	selectedSequence?: number
	selectedNodeID?: string
	onSelectSequence: (sequence: number) => void
	onSelectNode: (nodeID: string) => void
}

export function DebugWorkbench(props: DebugWorkbenchProps) {
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
			<NodeRunDetail nodeID={props.selectedNodeID} events={props.events} />
		</aside>
	)
}
