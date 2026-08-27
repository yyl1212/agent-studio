import {
  addEdge,
  applyEdgeChanges,
  applyNodeChanges,
  type Connection,
  type EdgeChange,
  type NodeChange,
} from '@xyflow/react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useParams } from 'react-router-dom'

import type { JSONSchema } from '../../components/schema-form/types'
import { ConfirmDialog } from '../../components/ui/ConfirmDialog'
import { APIError, api, type AgentPresentation, type NodeDefinition, type ResolvedPorts, type Workflow } from '../../lib/api/client'
import { readNDJSON, type RunEvent } from '../../lib/api/ndjson'
import { applyNodeConfig } from './configDraft'
import { AgentPageSettingsDialog } from './AgentPageSettingsDialog'
import { fromFlowGraph, portsFromDefinition } from './graphAdapter'
import { hydrateWorkflowGraph } from './hydrateWorkflowGraph'
import { NodeConfigPanel } from './NodeConfigPanel'
import { NodeLibraryDrawer } from './NodeLibraryDrawer'
import { availableNodePosition } from './nodePlacement'
import { PublishDialog } from './PublishDialog'
import { SaveQueue, type SaveState } from './saveQueue'
import { StudioCommandBar } from './StudioCommandBar'
import { StudioQuickTools } from './StudioQuickTools'
import { StudioShell } from './StudioShell'
import { TestRunPanel } from './TestRunPanel'
import type { StudioEdge, StudioNode } from './types'
import { useNodeConfigDraft } from './useNodeConfigDraft'
import { useStudioWorkbench, type WorkbenchIntent } from './useStudioWorkbench'
import { WorkbenchPanel } from './WorkbenchPanel'
import { WorkflowCanvas, type WorkflowCanvasHandle } from './WorkflowCanvas'
import { VersionGovernancePanel } from './VersionGovernancePanel'
import '@xyflow/react/dist/style.css'
import './studio.css'

export function StudioPage() {
  const { id = '' } = useParams()
  const [workflow, setWorkflow] = useState<Workflow>()
  const [definitions, setDefinitions] = useState<NodeDefinition[]>([])
  const [nodes, setNodes] = useState<StudioNode[]>([])
  const [edges, setEdges] = useState<StudioEdge[]>([])
  const [libraryOpen, setLibraryOpen] = useState(false)
  const [activeDisclosure, setActiveDisclosure] = useState<'commands' | 'shortcuts'>()
  const [publishOpen, setPublishOpen] = useState(false)
  const [publishedVersion, setPublishedVersion] = useState<number>()
  const [publishError, setPublishError] = useState('')
  const [publishing, setPublishing] = useState(false)
  const [exporting, setExporting] = useState(false)
  const [exportError, setExportError] = useState('')
  const [agentSettingsOpen, setAgentSettingsOpen] = useState(false)
  const [agentSettingsSaving, setAgentSettingsSaving] = useState(false)
  const [agentSettingsError, setAgentSettingsError] = useState('')
  const [saveState, setSaveState] = useState<SaveState>('saved')
  const [loadError, setLoadError] = useState('')
  const [events, setEvents] = useState<RunEvent[]>([])
  const [runError, setRunError] = useState('')
  const [running, setRunning] = useState(false)
	const [runCancelled, setRunCancelled] = useState(false)
	const [connectionStatus, setConnectionStatus] = useState<{ message: string; clientX: number; clientY: number }>()
	const [draftEditSerial, setDraftEditSerial] = useState(0)
	const [versionLocked, setVersionLocked] = useState(false)
	const [versionError, setVersionError] = useState('')
	const [fitRequest, setFitRequest] = useState(0)
  const saveQueue = useRef<SaveQueue | undefined>(undefined)
  const runController = useRef<AbortController | undefined>(undefined)
  const exportController = useRef<AbortController | undefined>(undefined)
	const hydrateController = useRef<AbortController | undefined>(undefined)
  const moreActionsTriggerRef = useRef<HTMLElement>(null)
  const shortcutHelpTriggerRef = useRef<HTMLElement>(null)
  const canvasRef = useRef<WorkflowCanvasHandle>(null)
  const addButtonRef = useRef<HTMLButtonElement>(null)
  const testButtonRef = useRef<HTMLButtonElement>(null)
  const lastTriggerRef = useRef<HTMLElement | null>(null)
  const workbench = useStudioWorkbench()

  useEffect(() => () => {
    exportController.current?.abort()
    runController.current?.abort()
		hydrateController.current?.abort()
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    Promise.all([api.getWorkflow(id, controller.signal), api.listNodeTypes(controller.signal)])
      .then(async ([loadedWorkflow, loadedDefinitions]) => {
		const flow = await hydrateWorkflowGraph(loadedWorkflow, loadedDefinitions, api.resolveNodeType, controller.signal)
        setWorkflow(loadedWorkflow)
        setDefinitions(loadedDefinitions)
        setNodes(flow.nodes)
        setEdges(flow.edges)
        saveQueue.current = loadedWorkflow.archivedAt ? undefined : new SaveQueue(
          loadedWorkflow.draftRevision,
          async (request) => {
            const saved = await api.saveWorkflow(id, request)
            setWorkflow(saved)
            return saved
          },
          800,
          setSaveState,
        )
      })
      .catch((error: unknown) => {
        if (!(error instanceof DOMException && error.name === 'AbortError')) setLoadError('加载工作流失败，请返回列表重试')
      })
    return () => controller.abort()
  }, [id])

  const selectedID = workbench.mode.kind === 'config' ? workbench.mode.nodeId : undefined
  const selectedNode = nodes.find((node) => node.id === selectedID)
  const archived = Boolean(workflow?.archivedAt)
  const saveBlocked = saveState === 'error' || saveState === 'conflict'
  const resolveNodePorts = useCallback((type: string, version: string, config: Record<string, unknown>, signal: AbortSignal) => api.resolveNodeType(type, version, config, signal), [])
  const configDraft = useNodeConfigDraft({ node: selectedNode, edges, resolve: resolveNodePorts })

  const commit = (nextNodes: StudioNode[], nextEdges: StudioEdge[]) => {
		if (!archived && saveQueue.current) {
			setDraftEditSerial((value) => value + 1)
			saveQueue.current.enqueue(fromFlowGraph(nextNodes, nextEdges))
		}
  }
  const handleNodesChange = (changes: NodeChange<StudioNode>[]) => {
    if (archived) return
    setNodes((current) => {
      const next = applyNodeChanges(changes, current)
		  if (changes.some((change) => change.type !== 'remove' && isPersistentNodeChange(change))) commit(next, edges)
      return next
    })
  }
  const handleEdgesChange = (changes: EdgeChange<StudioEdge>[]) => {
    if (archived) return
    setEdges((current) => {
      const next = applyEdgeChanges(changes, current)
		  if (changes.some((change) => change.type !== 'remove' && isPersistentEdgeChange(change))) commit(nodes, next)
      return next
    })
  }
  const isValidConnection = (connection: Connection | StudioEdge) => validateConnection(connection, nodes, edges)
  const handleConnect = (connection: Connection) => {
    if (archived || !isValidConnection(connection)) return
    setConnectionStatus(undefined)
    setEdges((current) => {
      const next = addEdge({ ...connection, id: createID('edge'), data: {} }, current)
      commit(nodes, next)
      return next
    })
  }
  const handleDelete = (deleted: { nodes: StudioNode[]; edges: StudioEdge[] }) => {
    if (archived) return
    const next = graphAfterDelete(nodes, edges, deleted.nodes, deleted.edges)
    setNodes(next.nodes)
    setEdges(next.edges)
    commit(next.nodes, next.edges)
  }

  const addNode = (definition: NodeDefinition) => {
    if (archived) return
    const position = availableNodePosition(canvasRef.current?.getViewportCenter() ?? { x: 320, y: 260 }, nodes)
    const node: StudioNode = {
      id: createID(definition.type), type: 'studio',
      position,
      data: {
        nodeType: definition.type,
        typeVersion: definition.version,
        config: defaultValue(definition.configSchema as JSONSchema) as Record<string, unknown>,
        definition,
        ports: portsFromDefinition(definition),
        issues: [],
      },
    }
    const next = [...nodes, node]
    setNodes(next)
    setLibraryOpen(false)
    workbench.request({ kind: 'config', nodeId: node.id }, false)
    commit(next, edges)
  }

  const applySelectedConfig = (config: Record<string, unknown>, ports: ResolvedPorts) => {
    if (archived || !selectedID) return false
    const applied = applyNodeConfig(nodes, edges, selectedID, config, ports)
    setNodes(applied.nodes)
    setEdges(applied.edges)
    commit(applied.nodes, applied.edges)
    configDraft.markApplied(config, ports)
    return true
  }

  const applyAndOpenTest = async (config: Record<string, unknown>, ports: ResolvedPorts) => {
    if (!applySelectedConfig(config, ports)) return
    try {
      await saveQueue.current?.flush()
      executeIntent({ kind: 'test' })
    } catch {
      // SaveQueue exposes the failure through saveState; keep the configuration context open.
    }
  }

  const startSchema = useMemo(() => deriveStartSchema(nodes), [nodes])
  const runDraft = async (input: Record<string, unknown>) => {
    if (!workflow || archived) return
    runController.current?.abort()
    const controller = new AbortController()
    runController.current = controller
    setEvents([])
    setRunError('')
    setRunCancelled(false)
    setRunning(true)
    try {
      await saveQueue.current?.flush()
      const response = await api.runDraft(workflow.id, { draftRevision: saveQueue.current?.getRevision() ?? workflow.draftRevision, input }, controller.signal)
      await readNDJSON(response, (event) => {
        setEvents((current) => [...current, event])
        if (event.type === 'run.cancelled') setRunCancelled(true)
      }, controller.signal)
    } catch (error) {
      if (!(error instanceof DOMException && error.name === 'AbortError')) setRunError(publicMessage(error))
    } finally {
      if (runController.current === controller) {
        runController.current = undefined
        setRunning(false)
      }
    }
  }

  const cancelRun = () => {
    setRunCancelled(true)
    runController.current?.abort()
  }

  const publish = async () => {
    if (!workflow || archived) return
    setPublishing(true)
    setPublishError('')
    try {
      await saveQueue.current?.flush()
      const validation = await api.validateWorkflow(workflow.id)
      if (!validation.valid) {
        setNodes((current) => current.map((node) => ({ ...node, data: { ...node.data, issues: validation.issues.filter((issue) => issue.nodeId === node.id) } })))
        const issueNodeID = validation.issues.find((issue) => issue.nodeId)?.nodeId
        if (issueNodeID && nodes.some((node) => node.id === issueNodeID)) {
          workbench.request({ kind: 'config', nodeId: issueNodeID }, false)
          setPublishOpen(false)
        }
        setPublishError(validation.issues[0]?.message ?? '工作流校验失败')
        return
      }
      const version = await api.publishWorkflow(workflow.id, saveQueue.current?.getRevision() ?? workflow.draftRevision)
      setPublishedVersion(version.version)
    } catch (error) {
      setPublishError(publicMessage(error))
    } finally {
      setPublishing(false)
    }
  }

  const exportTemplate = async () => {
    if (!workflow || exporting) return
    exportController.current?.abort()
    const controller = new AbortController()
    exportController.current = controller
    setExporting(true)
    setExportError('')
    try {
      if (!archived) await saveQueue.current?.flush()
      const revision = archived ? workflow.draftRevision : (saveQueue.current?.getRevision() ?? workflow.draftRevision)
      const blob = await api.exportWorkflowTemplate(workflow.id, revision, controller.signal)
      const url = URL.createObjectURL(blob)
      try {
        const anchor = document.createElement('a')
        anchor.href = url
        anchor.download = `${workflow.slug}.workflow.json`
        document.body.append(anchor)
        try {
          anchor.click()
        } finally {
          anchor.remove()
        }
      } finally {
        URL.revokeObjectURL(url)
      }
    } catch (error) {
      if (!controller.signal.aborted && !(error instanceof DOMException && error.name === 'AbortError')) {
        setExportError(publicMessage(error))
      }
    } finally {
      if (!controller.signal.aborted) setExporting(false)
    }
  }

  const openAgentSettings = async () => {
    if (!workflow || archived) return
    setAgentSettingsError('')
    try {
      await saveQueue.current?.flush()
      setAgentSettingsOpen(true)
    } catch (error) {
      setAgentSettingsError(publicMessage(error))
    }
  }

  const saveAgentSettings = async (presentation: AgentPresentation) => {
    if (!workflow || archived || !saveQueue.current) return
    setAgentSettingsSaving(true)
    setAgentSettingsError('')
    try {
      const updated = await api.saveAgentPresentation(workflow.id, {
        draftRevision: saveQueue.current.getRevision(), presentation,
      })
      saveQueue.current.adoptRevision(updated.draftRevision)
      setWorkflow(updated)
		setDraftEditSerial((value) => value + 1)
      setAgentSettingsOpen(false)
    } catch (error) {
      if (error instanceof APIError && error.status === 409) {
        setAgentSettingsError('草稿已在其他页面更新，请刷新后重试')
      } else {
        setAgentSettingsError(publicMessage(error))
      }
    } finally {
      setAgentSettingsSaving(false)
    }
  }

  const applyVersionWorkflow = async (updated: Workflow) => {
		await saveQueue.current?.flush()
		hydrateController.current?.abort()
		const controller = new AbortController()
		hydrateController.current = controller
		const flow = await hydrateWorkflowGraph(updated, definitions, api.resolveNodeType, controller.signal)
		if (controller.signal.aborted) return
		saveQueue.current?.adoptRevision(updated.draftRevision)
		setWorkflow(updated)
		setNodes(flow.nodes)
		setEdges(flow.edges)
		setEvents([])
		setRunError('')
		setRunCancelled(false)
		setRunning(false)
		setFitRequest((value) => value + 1)
	}

  const executeIntent = (intent: WorkbenchIntent) => {
    setActiveDisclosure(undefined)
    if (workbench.mode.kind === 'test' && intent.kind !== 'test') runController.current?.abort()
		if (intent.kind === 'version-history') {
			setVersionError('')
			void Promise.resolve(saveQueue.current?.flush()).then(() => {
				setLibraryOpen(false)
				workbench.request(intent, false)
			}).catch((error: unknown) => setVersionError(publicMessage(error)))
			return
		}
    if (intent.kind === 'open-library') {
      workbench.request({ kind: 'close' }, false)
      setActiveDisclosure(undefined)
      setLibraryOpen(true)
      return
    }
    if (intent.kind === 'publish') {
      setPublishOpen(true)
      setPublishedVersion(undefined)
      setPublishError('')
      return
    }
    if (intent.kind === 'export') {
      void exportTemplate()
      return
    }
    if (intent.kind === 'agent-presentation') {
      void openAgentSettings()
      return
    }
		if (intent.kind === 'close' && workbench.mode.kind === 'versions') {
			workbench.request(intent, false)
			window.requestAnimationFrame(() => moreActionsTriggerRef.current?.focus())
			return
		}
    setLibraryOpen(false)
    workbench.request(intent, false)
  }

  const requestIntent = (intent: WorkbenchIntent) => {
    if (archived && intent.kind !== 'export' && intent.kind !== 'close' && intent.kind !== 'version-history') return
    if (activeDisclosure) setActiveDisclosure(undefined)
    if (intent.kind === 'config' && workbench.mode.kind === 'config' && intent.nodeId === workbench.mode.nodeId) return
    if (configDraft.dirty && workbench.mode.kind === 'config') workbench.request(intent, true)
    else executeIntent(intent)
  }

  const rememberTrigger = (trigger: HTMLElement | null) => {
    lastTriggerRef.current = trigger
  }

  const disclosureTrigger = (disclosure = activeDisclosure) => disclosure === 'commands'
    ? moreActionsTriggerRef.current
    : disclosure === 'shortcuts' ? shortcutHelpTriggerRef.current : null

  const closeTopLayer = () => {
    if (activeDisclosure) {
      const trigger = disclosureTrigger()
      setActiveDisclosure(undefined)
      window.requestAnimationFrame(() => trigger?.focus())
      return
    }
    if (libraryOpen) {
      setLibraryOpen(false)
      return
    }
    if (workbench.mode.kind !== 'closed' && !(workbench.mode.kind === 'versions' && versionLocked)) {
      requestIntent({ kind: 'close' })
    }
  }

  const openLibraryFromQuickTools = () => {
    rememberTrigger(addButtonRef.current)
    setActiveDisclosure(undefined)
    requestIntent({ kind: 'open-library' })
  }

  const retrySave = () => {
    if (saveState === 'error') commit(nodes, edges)
  }

  const continuePendingIntent = (choice: 'apply' | 'discard') => {
    if (choice === 'apply') {
      if (!configDraft.normalized || !configDraft.preview || configDraft.status !== 'ready') return
      applySelectedConfig(configDraft.normalized, configDraft.preview.ports)
    } else {
      configDraft.reset()
    }
    const intent = workbench.resolveDirty(choice)
    if (intent) executeIntent(intent)
  }

  if (loadError) return <main className="page-container"><p role="alert">{loadError}</p></main>
  if (!workflow) return <main className="page-container" aria-live="polite">正在加载编辑器…</main>

  return (
    <main className="studio-page">
      <StudioShell
        layer={activeDisclosure ?? (libraryOpen ? 'library' : workbench.mode.kind !== 'closed' ? 'workbench' : 'none')}
        returnFocusRef={lastTriggerRef}
        onOpenNodeLibrary={() => {
          if (archived || versionLocked) return
          rememberTrigger(disclosureTrigger() ?? (document.activeElement instanceof HTMLElement ? document.activeElement : addButtonRef.current))
          setActiveDisclosure(undefined)
          requestIntent({ kind: 'open-library' })
        }}
        onRequestCloseTopLayer={closeTopLayer}
        commandBar={<StudioCommandBar
          workflowName={workflow.name}
          saveState={saveState}
          archived={archived}
          exporting={exporting}
          runsHref={`/runs?workflowId=${encodeURIComponent(workflow.id)}`}
          actionError={exportError || versionError || (!agentSettingsOpen && agentSettingsError) || ''}
          testLabel="测试运行"
          testDisabled={archived || versionLocked || saveBlocked}
          testButtonRef={testButtonRef}
          moreActionsTriggerRef={moreActionsTriggerRef}
          moreActionsOpen={activeDisclosure === 'commands'}
          onMoreActionsOpenChange={(open) => {
            if (open) {
              rememberTrigger(moreActionsTriggerRef.current)
              setLibraryOpen(false)
              setActiveDisclosure('commands')
            } else {
              setActiveDisclosure((current) => current === 'commands' ? undefined : current)
            }
          }}
          onTest={() => { rememberTrigger(testButtonRef.current); requestIntent({ kind: 'test' }) }}
          onPublish={() => requestIntent({ kind: 'publish' })}
          onAgentPresentation={() => requestIntent({ kind: 'agent-presentation' })}
          onVersionHistory={() => { rememberTrigger(moreActionsTriggerRef.current); requestIntent({ kind: 'version-history' }) }}
          onExport={() => requestIntent({ kind: 'export' })}
          onRetrySave={retrySave}
          onRefreshConflict={() => window.location.reload()}
        />}
        quickTools={<StudioQuickTools
          disabled={archived || versionLocked}
          addButtonRef={addButtonRef}
          shortcutHelpTriggerRef={shortcutHelpTriggerRef}
          shortcutHelpOpen={activeDisclosure === 'shortcuts'}
          onShortcutHelpOpenChange={(open) => {
            if (open) {
              rememberTrigger(shortcutHelpTriggerRef.current)
              setLibraryOpen(false)
              setActiveDisclosure('shortcuts')
            } else {
              setActiveDisclosure((current) => current === 'shortcuts' ? undefined : current)
            }
          }}
          onAdd={openLibraryFromQuickTools}
          onFitView={() => { void canvasRef.current?.fitView() }}
        />}
        canvas={<WorkflowCanvas
          ref={canvasRef}
          readOnly={archived || versionLocked}
          fitRequest={fitRequest}
          nodes={decorateRunNodes(nodes, events)}
          edges={edges}
          onNodesChange={handleNodesChange}
          onEdgesChange={handleEdgesChange}
          onConnect={handleConnect}
          onDelete={handleDelete}
          isValidConnection={isValidConnection}
          currentNodeID={activeRunNodeID(events)}
          onInvalidConnection={({ connection, clientX, clientY }) => {
            const message = connectionIssue(connection, nodes, edges)
            if (message) setConnectionStatus({
              message,
              clientX: Math.max(8, Math.min(clientX, window.innerWidth - 300)),
              clientY: Math.max(8, Math.min(clientY, window.innerHeight - 100)),
            })
          }}
          onNodeClick={(node, trigger) => {
            rememberTrigger(trigger)
            if (!archived && !versionLocked) requestIntent({ kind: 'config', nodeId: node.id })
          }}
        />}
        nodeLibrary={libraryOpen ? <NodeLibraryDrawer definitions={definitions} onAdd={addNode} onClose={() => setLibraryOpen(false)} /> : undefined}
        workbench={workbench.mode.kind !== 'closed' ? <WorkbenchPanel titleId="studio-workbench-title" closeDisabled={workbench.mode.kind === 'versions' && versionLocked} onRequestClose={closeTopLayer}>
          {workbench.mode.kind === 'config' && selectedNode && <NodeConfigPanel titleId="studio-workbench-title" node={selectedNode} draft={configDraft} onApply={(config, ports) => { applySelectedConfig(config, ports) }} onApplyAndTest={applyAndOpenTest} />}
          {workbench.mode.kind === 'test' && <><header className="workbench-heading"><span className="node-category">调试</span><h2 id="studio-workbench-title">测试运行</h2></header><TestRunPanel schema={startSchema} events={events} running={running} cancelled={runCancelled} error={runError} onRun={runDraft} onCancel={cancelRun} /></>}
          {workbench.mode.kind === 'versions' && <VersionGovernancePanel titleId="studio-workbench-title" workflow={workflow} saveState={saveState} editSerial={draftEditSerial} archived={archived} onApplyWorkflow={applyVersionWorkflow} onLockChange={setVersionLocked} />}
        </WorkbenchPanel> : undefined}
      />
      {connectionStatus && <output className="studio-canvas-status" aria-live="polite" style={{ left: connectionStatus.clientX, top: connectionStatus.clientY }}>{connectionStatus.message}</output>}
      {archived && <div className="studio-archive-banner" role="status">已归档，只读模式。恢复后才能保存、测试或发布。</div>}
      <ConfirmDialog
        open={Boolean(workbench.pendingIntent)}
        title="保存节点配置更改？"
        description="当前节点有尚未应用的配置。应用后继续，或放弃这些更改。"
        confirmLabel="应用并继续"
        discardLabel="放弃更改"
        cancelLabel="取消"
        confirmDisabled={configDraft.status !== 'ready' || !configDraft.normalized || !configDraft.preview}
        onConfirm={() => continuePendingIntent('apply')}
        onDiscard={() => continuePendingIntent('discard')}
        onCancel={() => workbench.resolveDirty('cancel')}
      />
      {publishOpen && <PublishDialog slug={workflow.slug} version={publishedVersion} error={publishError} publishing={publishing} onConfirm={publish} onClose={() => setPublishOpen(false)} />}
      <AgentPageSettingsDialog
        open={agentSettingsOpen}
        workflowName={workflow.name}
        workflowDescription={workflow.description}
        value={workflow.agentPresentation}
        saving={agentSettingsSaving}
        error={agentSettingsError}
        onClose={() => { setAgentSettingsOpen(false); setAgentSettingsError('') }}
        onSave={(presentation) => { void saveAgentSettings(presentation) }}
      />
    </main>
  )
}

function createID(prefix: string) {
  return `${prefix}-${crypto.randomUUID()}`
}

export function graphAfterDelete(nodes: StudioNode[], edges: StudioEdge[], deletedNodes: StudioNode[], deletedEdges: StudioEdge[]) {
  const nodeIDs = new Set(deletedNodes.map((node) => node.id))
  const edgeIDs = new Set(deletedEdges.map((edge) => edge.id))
  return {
    nodes: nodes.filter((node) => !nodeIDs.has(node.id)),
    edges: edges.filter((edge) => !edgeIDs.has(edge.id) && !nodeIDs.has(edge.source) && !nodeIDs.has(edge.target)),
  }
}

export function isPersistentNodeChange(change: NodeChange<StudioNode>) {
	if (change.type === 'position') return change.dragging === false
	return change.type === 'add' || change.type === 'remove' || change.type === 'replace'
}

export function isPersistentEdgeChange(change: EdgeChange<StudioEdge>) {
	return change.type === 'add' || change.type === 'remove' || change.type === 'replace'
}

function defaultValue(schema: JSONSchema): unknown {
  if (schema.default !== undefined) return structuredClone(schema.default)
  if (schema.type === 'object') return Object.fromEntries(Object.entries(schema.properties ?? {}).map(([key, child]) => [key, defaultValue(child)]).filter(([, value]) => value !== undefined))
  if (schema.type === 'array') return []
  if (schema.type === 'boolean') return false
  if (schema.enum?.length) return schema.enum[0]
  if (schema.type === 'string') return ''
  return undefined
}

export function validateConnection(connection: Connection | StudioEdge, nodes: StudioNode[], edges: StudioEdge[]) {
  return connectionIssue(connection, nodes, edges) === undefined
}

export function connectionIssue(connection: Connection | StudioEdge, nodes: StudioNode[], edges: StudioEdge[]) {
  if (!connection.source || !connection.target || !connection.sourceHandle || !connection.targetHandle) return '请连接有效的输入和输出端口'
  if (connection.source === connection.target) return '节点不能连接到自身'
  const source = nodes.find((node) => node.id === connection.source)
  const target = nodes.find((node) => node.id === connection.target)
  const output = source?.data.ports.outputs.find((port) => port.key === connection.sourceHandle)
  const input = target?.data.ports.inputs.find((port) => port.key === connection.targetHandle)
  if (!output || !input) return '端口不存在或已变化'
  if (output.type !== 'any' && input.type !== 'any' && output.type !== input.type) return `端口类型不兼容：${output.type} 不能连接到 ${input.type}`
  if (input.cardinality === 'one' && edges.some((edge) => edge.id !== ('id' in connection ? connection.id : undefined) && edge.target === connection.target && edge.targetHandle === connection.targetHandle)) return '目标端口只允许一条输入连线'
  return undefined
}

export function markInvalidEdges(nodes: StudioNode[], edges: StudioEdge[]) {
  return edges.map((edge) => {
    const invalid = !validateConnection(edge, nodes, edges)
    return { ...edge, data: { ...edge.data, invalid }, style: invalid ? { stroke: '#d92d20', strokeDasharray: '5 4' } : undefined }
  })
}

export function activeRunNodeID(events: RunEvent[]) {
  const settled = new Set<string>()
  for (let index = events.length - 1; index >= 0; index -= 1) {
    const event = events[index]
    if (event.type === 'run.completed' || event.type === 'run.failed' || event.type === 'run.cancelled') return undefined
    if (!event.nodeId) continue
    if (event.type === 'node.completed' || event.type === 'node.failed' || event.type === 'node.skipped' || event.type === 'node.cancelled') settled.add(event.nodeId)
    else if (event.type === 'node.started' && !settled.has(event.nodeId)) return event.nodeId
  }
  return undefined
}

export function decorateRunNodes(nodes: StudioNode[], events: RunEvent[]) {
  const statuses = new Map<string, string>()
  for (const event of events) {
    if (!event.nodeId || !event.type.startsWith('node.')) continue
    statuses.set(event.nodeId, event.type === 'node.started' ? 'running' : event.type.slice('node.'.length))
  }
  const currentNodeID = activeRunNodeID(events)
  return nodes.map((node) => ({
    ...node,
    data: { ...node.data, debugStatus: statuses.get(node.id), debugCurrent: node.id === currentNodeID },
  }))
}

function deriveStartSchema(nodes: StudioNode[]): JSONSchema {
  const fields = nodes.find((node) => node.data.nodeType === 'start')?.data.config.fields
  const properties: Record<string, JSONSchema> = {}
  const required: string[] = []
  const order: string[] = []
  if (Array.isArray(fields)) for (const raw of fields) {
    if (!raw || typeof raw !== 'object') continue
    const field = raw as Record<string, unknown>
    const key = String(field.key ?? '')
    if (!key) continue
    const type = String(field.type ?? 'text')
    const property: JSONSchema = { title: String(field.label ?? key), description: String(field.description ?? '') }
    if (type === 'number') property.type = 'number'
    else if (type === 'boolean') property.type = 'boolean'
    else if (type === 'json') property['x-ui-widget'] = 'json'
    else { property.type = 'string'; property['x-ui-widget'] = type === 'textarea' || type === 'select' ? type : 'text' }
    if (type === 'select' && Array.isArray(field.options)) property.enum = field.options as string[]
    if (typeof field.placeholder === 'string') property['x-ui-placeholder'] = field.placeholder
    if (field.default !== undefined) property.default = field.default
    properties[key] = property
    order.push(key)
    if (field.required) required.push(key)
  }
  return { type: 'object', properties, required, 'x-ui-order': order, additionalProperties: false }
}

function publicMessage(error: unknown) {
  if (error instanceof APIError && error.code === 'WORKFLOW_TEMPLATE_INVALID') return '当前草稿不能导出，请先修复工作流问题'
  if (error instanceof APIError) return error.message
  return '操作失败，请稍后重试'
}
