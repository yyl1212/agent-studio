import type { RunEvent } from '../../lib/api/ndjson'

interface RunTimelineProps {
	events: RunEvent[]
	selectedSequence?: number
	onSelect: (event: RunEvent) => void
}

export function RunTimeline({ events, selectedSequence, onSelect }: RunTimelineProps) {
	const selectedIndex = events.findIndex((event) => event.sequence === selectedSequence)
	const previous = selectedIndex > 0 ? events[selectedIndex - 1] : undefined
	const next = selectedIndex >= 0 && selectedIndex < events.length - 1 ? events[selectedIndex + 1] : undefined
	return (
		<section className="run-timeline" aria-label="运行时间线">
			<header><h2>事件时间线</h2><span>{events.length} 项</span></header>
			<div className="timeline-navigation">
				<button type="button" disabled={!previous} onClick={() => previous && onSelect(previous)}>上一项</button>
				<button type="button" disabled={!next} onClick={() => next && onSelect(next)}>下一项</button>
			</div>
			<ol>
				{events.map((event) => (
					<li key={event.sequence}>
						<button type="button" className={event.sequence === selectedSequence ? 'current' : ''} onClick={() => onSelect(event)} aria-label={`#${event.sequence} ${event.type}${event.nodeId ? ` ${event.nodeId}` : ''}`}>
							<strong>#{event.sequence} {event.type}</strong>{event.nodeId && <span>{event.nodeId}</span>}
						</button>
					</li>
				))}
			</ol>
		</section>
	)
}
