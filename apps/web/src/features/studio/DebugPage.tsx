import { useEffect, useRef, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import type { Connection, EdgeChange, NodeChange } from '@xyflow/react'

import { APIError, api, type DebugOverview, type RerunPreview } from '../../lib/api/client'
import { readNDJSON, type RunEvent } from '../../lib/api/ndjson'
import { portsFromDefinition, toFlowGraph } from './graphAdapter'
import type { StudioEdge, StudioNode } from './types'
import { DebugWorkbench } from './DebugWorkbench'
import { WorkflowCanvas } from './WorkflowCanvas'
import '@xyflow/react/dist/style.css'
import './studio.css'

export function DebugPage() {
	const { id = '', runId = '' } = useParams()
	const navigate = useNavigate()
	const [overview, setOverview] = useState<DebugOverview>()
	const [nodes, setNodes] = useState<StudioNode[]>([])
	const [edges, setEdges] = useState<StudioEdge[]>([])
	const [events, setEvents] = useState<RunEvent[]>([])
	const [selectedSequence, setSelectedSequence] = useState<number>()
	const [selectedNodeID, setSelectedNodeID] = useState<string>()
	const [error, setError] = useState('')
	const [rerunPreview, setRerunPreview] = useState<RerunPreview>()
	const [rerunEvents, setRerunEvents] = useState<RunEvent[]>([])
	const [rerunRunning, setRerunRunning] = useState(false)
	const [rerunError, setRerunError] = useState('')
	const [newRunID, setNewRunID] = useState<string>()
	const rerunController = useRef<AbortController | undefined>(undefined)

	useEffect(() => () => rerunController.current?.abort(), [])

	useEffect(() => {
		const controller = new AbortController()
		const load = async () => {
			try {
				const [loadedOverview, definitions] = await Promise.all([
					api.getDebugOverview(runId, controller.signal),
					api.listNodeTypes(controller.signal),
				])
				const resolved = await Promise.all(loadedOverview.graph.nodes.map(async (node) => {
					try {
						return [node.id, await api.resolveNodeType(node.type, node.typeVersion, node.config, controller.signal)] as const
					} catch {
						if (controller.signal.aborted) throw new DOMException('操作已取消', 'AbortError')
						const definition = definitions.find((candidate) => candidate.type === node.type && candidate.version === node.typeVersion)
						return [node.id, portsFromDefinition(definition)] as const
					}
				}))
				const flow = toFlowGraph(loadedOverview.graph, definitions, Object.fromEntries(resolved))
				const statuses = new Map(loadedOverview.nodeRuns.map((nodeRun) => [nodeRun.nodeId, nodeRun.status]))
				setOverview(loadedOverview)
				setNodes(flow.nodes.map((node) => ({ ...node, data: { ...node.data, debugStatus: statuses.get(node.id) } })))
				setEdges(flow.edges)
				if (!loadedOverview.replayAvailable) return
				const loadedEvents: RunEvent[] = []
				let after = 0
				for (;;) {
					const page = await api.listRunEvents(runId, after, controller.signal)
					loadedEvents.push(...page.events)
					if (page.events.length === 0 || page.nextAfterSequence <= after) break
					after = page.nextAfterSequence
				}
				setEvents(loadedEvents)
				const first = loadedEvents[0]
				if (first) {
					setSelectedSequence(first.sequence)
					if (first.nodeId) setSelectedNodeID(first.nodeId)
				}
			} catch (failure) {
				if (!(failure instanceof DOMException && failure.name === 'AbortError')) setError(debugMessage(failure))
			}
		}
		void load()
		return () => controller.abort()
	}, [runId])

	const displayNodes = nodes.map((node) => ({ ...node, data: { ...node.data, debugCurrent: node.id === selectedNodeID } }))
	const ignoreNodes = (_changes: NodeChange<StudioNode>[]) => undefined
	const ignoreEdges = (_changes: EdgeChange<StudioEdge>[]) => undefined
	const ignoreConnect = (_connection: Connection) => undefined
	const startRerun = async (nodeID: string) => {
		rerunController.current?.abort()
		const controller = new AbortController()
		rerunController.current = controller
		setRerunError('')
		try {
			setRerunPreview(await api.previewRerun(runId, nodeID, controller.signal))
			setRerunEvents([])
			setNewRunID(undefined)
		} catch (failure) {
			if (!(failure instanceof DOMException && failure.name === 'AbortError')) setRerunError(debugMessage(failure))
		}
	}
	const submitRerun = async (entryInput: Record<string, unknown>, confirmed: boolean) => {
		if (!rerunPreview) return
		rerunController.current?.abort()
		const controller = new AbortController()
		rerunController.current = controller
		setRerunRunning(true)
		setRerunError('')
		setRerunEvents([])
		setNewRunID(undefined)
		let currentRunID = ''
		try {
			const response = await api.rerunFromNode(runId, rerunPreview.sourceNodeId, {
				entryInput,
				confirmSideEffects: rerunPreview.requiresConfirmation && confirmed,
			}, controller.signal)
			await readNDJSON(response, (event) => {
				if (!currentRunID) {
					currentRunID = event.runId
					setNewRunID(currentRunID)
				}
				setRerunEvents((current) => [...current, event])
				if (event.type === 'run.completed' && currentRunID) navigate(`/workflows/${id}/runs/${currentRunID}/debug`)
			}, controller.signal)
		} catch (failure) {
			if (failure instanceof DOMException && failure.name === 'AbortError') setRerunError('运行已取消')
			else setRerunError(debugMessage(failure))
		} finally {
			setRerunRunning(false)
		}
	}
	const closeRerun = () => {
		rerunController.current?.abort()
		setRerunPreview(undefined)
		setRerunEvents([])
		setRerunError('')
		setNewRunID(undefined)
	}

	if (error) return <main className="page-container"><p role="alert">{error}</p><Link to={`/workflows/${id}/runs`}>返回运行记录</Link></main>
	if (!overview) return <main className="page-container" aria-live="polite">正在加载调试回放…</main>
	return (
		<main className="studio-page debug-page">
			<header className="studio-toolbar debug-toolbar">
				<div className="studio-title"><Link to={`/workflows/${id}/runs`} aria-label="退出调试回放">←</Link><div><strong>只读回放</strong><small>{modeLabel(overview.run.mode)} · 当前运行 {overview.run.id}</small>{overview.sourceChain.map((source) => <Link className="debug-source-link" key={source.runId} to={`/workflows/${id}/runs/${source.runId}/debug`}>来源运行 {source.runId}</Link>)}</div></div>
				<span className={`debug-run-status status-${overview.run.status}`}>{runStatusLabel(overview.run.status)}</span>
			</header>
			<WorkflowCanvas readOnly currentNodeID={selectedNodeID} nodes={displayNodes} edges={edges} onNodesChange={ignoreNodes} onEdgesChange={ignoreEdges} onConnect={ignoreConnect} isValidConnection={() => false} onNodeClick={(node) => setSelectedNodeID(node.id)} />
			{overview.replayAvailable
				? <DebugWorkbench overview={overview} events={events} selectedSequence={selectedSequence} selectedNodeID={selectedNodeID} onSelectSequence={setSelectedSequence} onSelectNode={setSelectedNodeID} onStartRerun={startRerun} rerunPreview={rerunPreview} rerunEvents={rerunEvents} rerunRunning={rerunRunning} rerunError={rerunError} debugRunPath={newRunID ? `/workflows/${id}/runs/${newRunID}/debug` : undefined} onSubmitRerun={submitRerun} onCancelRerun={() => rerunController.current?.abort()} onCloseRerun={closeRerun} />
				: <aside className="debug-workbench debug-unavailable"><h2>运行摘要</h2><p role="status">{overview.unavailableReason || '当前运行无法精确回放'}</p><p>仍可查看画布和节点最终摘要，但不能执行局部重跑。</p></aside>}
		</main>
	)
}

function modeLabel(mode: DebugOverview['run']['mode']) { return { test: '草稿测试', published: '已发布', debug: '局部调试' }[mode] }
function runStatusLabel(status: DebugOverview['run']['status']) { return { running: '◌ 运行中', completed: '✓ 已完成', failed: '× 失败', cancelled: '— 已取消' }[status] }
function debugMessage(error: unknown) { return error instanceof APIError ? error.message : '加载调试回放失败' }
