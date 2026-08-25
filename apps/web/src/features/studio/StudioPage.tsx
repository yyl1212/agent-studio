import {
  addEdge,
  applyEdgeChanges,
  applyNodeChanges,
  type Connection,
  type EdgeChange,
  type NodeChange,
} from '@xyflow/react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link, useParams } from 'react-router-dom'

import type { JSONSchema } from '../../components/schema-form/types'
import { ConfirmDialog } from '../../components/ui/ConfirmDialog'
import { APIError, api, type NodeDefinition, type Workflow } from '../../lib/api/client'
import { readNDJSON, type RunEvent } from '../../lib/api/ndjson'
import { applyNodeConfig } from './configDraft'
import { fromFlowGraph, portsFromDefinition, toFlowGraph } from './graphAdapter'
import { NodeConfigPanel } from './NodeConfigPanel'
import { NodeLibraryDrawer } from './NodeLibraryDrawer'
import { PublishDialog } from './PublishDialog'
import { SaveQueue, type SaveState } from './saveQueue'
import { TestRunPanel } from './TestRunPanel'
import type { StudioEdge, StudioNode } from './types'
import { useNodeConfigDraft } from './useNodeConfigDraft'
import { useStudioWorkbench, type WorkbenchIntent } from './useStudioWorkbench'
import { WorkbenchPanel } from './WorkbenchPanel'
import { WorkflowCanvas } from './WorkflowCanvas'
import '@xyflow/react/dist/style.css'
import './studio.css'

export function StudioPage() {
  const { id = '' } = useParams()
  const [workflow, setWorkflow] = useState<Workflow>()
  const [definitions, setDefinitions] = useState<NodeDefinition[]>([])
  const [nodes, setNodes] = useState<StudioNode[]>([])
  const [edges, setEdges] = useState<StudioEdge[]>([])
  const [libraryOpen, setLibraryOpen] = useState(false)
  const [publishOpen, setPublishOpen] = useState(false)
  const [publishedVersion, setPublishedVersion] = useState<number>()
  const [publishError, setPublishError] = useState('')
  const [publishing, setPublishing] = useState(false)
  const [exporting, setExporting] = useState(false)
  const [exportError, setExportError] = useState('')
  const [saveState, setSaveState] = useState<SaveState>('saved')
  const [loadError, setLoadError] = useState('')
  const [events, setEvents] = useState<RunEvent[]>([])
  const [runError, setRunError] = useState('')
  const [running, setRunning] = useState(false)
  const saveQueue = useRef<SaveQueue | undefined>(undefined)
  const runController = useRef<AbortController | undefined>(undefined)
  const exportController = useRef<AbortController | undefined>(undefined)
  const workbench = useStudioWorkbench()

  useEffect(() => () => exportController.current?.abort(), [])

  useEffect(() => {
    const controller = new AbortController()
    Promise.all([api.getWorkflow(id, controller.signal), api.listNodeTypes(controller.signal)])
      .then(async ([loadedWorkflow, loadedDefinitions]) => {
        const resolvedEntries = await Promise.all(loadedWorkflow.draftGraph.nodes.map(async (node) => {
          try {
            return [node.id, await api.resolveNodeType(node.type, node.typeVersion, node.config, controller.signal)] as const
          } catch {
            return [node.id, portsFromDefinition(loadedDefinitions.find((definition) => definition.type === node.type && definition.version === node.typeVersion))] as const
          }
        }))
        const flow = toFlowGraph(loadedWorkflow.draftGraph, loadedDefinitions, Object.fromEntries(resolvedEntries))
        setWorkflow(loadedWorkflow)
        setDefinitions(loadedDefinitions)
        setNodes(flow.nodes)
        setEdges(flow.edges)
        saveQueue.current = new SaveQueue(
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
  const resolveNodePorts = useCallback((type: string, version: string, config: Record<string, unknown>, signal: AbortSignal) => api.resolveNodeType(type, version, config, signal), [])
  const configDraft = useNodeConfigDraft({ node: selectedNode, edges, resolve: resolveNodePorts })

  const commit = (nextNodes: StudioNode[], nextEdges: StudioEdge[]) => saveQueue.current?.enqueue(fromFlowGraph(nextNodes, nextEdges))
  const handleNodesChange = (changes: NodeChange<StudioNode>[]) => {
    setNodes((current) => {
      const next = applyNodeChanges(changes, current)
      commit(next, edges)
      return next
    })
  }
  const handleEdgesChange = (changes: EdgeChange<StudioEdge>[]) => {
    setEdges((current) => {
      const next = applyEdgeChanges(changes, current)
      commit(nodes, next)
      return next
    })
  }
  const isValidConnection = (connection: Connection | StudioEdge) => validateConnection(connection, nodes, edges)
  const handleConnect = (connection: Connection) => {
    if (!isValidConnection(connection)) return
    setEdges((current) => {
      const next = addEdge({ ...connection, id: createID('edge'), data: {} }, current)
      commit(nodes, next)
      return next
    })
  }

  const addNode = (definition: NodeDefinition) => {
    const addedCount = nodes.filter((existing) => existing.data.nodeType !== 'start' && existing.data.nodeType !== 'end').length
    const node: StudioNode = {
      id: createID(definition.type), type: 'studio',
      position: { x: 320, y: 260 + addedCount * 220 },
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

  const applySelectedConfig = (config: Record<string, unknown>, ports: Parameters<typeof applyNodeConfig>[4]) => {
    if (!selectedID) return
    const applied = applyNodeConfig(nodes, edges, selectedID, config, ports)
    setNodes(applied.nodes)
    setEdges(applied.edges)
    commit(applied.nodes, applied.edges)
    configDraft.markApplied(config, ports)
  }

  const startSchema = useMemo(() => deriveStartSchema(nodes), [nodes])
  const runDraft = async (input: Record<string, unknown>) => {
    if (!workflow) return
    runController.current?.abort()
    const controller = new AbortController()
    runController.current = controller
    setEvents([])
    setRunError('')
    setRunning(true)
    try {
      await saveQueue.current?.flush()
      const response = await api.runDraft(workflow.id, { draftRevision: saveQueue.current?.getRevision() ?? workflow.draftRevision, input }, controller.signal)
      await readNDJSON(response, (event) => setEvents((current) => [...current, event]), controller.signal)
    } catch (error) {
      if (!(error instanceof DOMException && error.name === 'AbortError')) setRunError(publicMessage(error))
    } finally {
      setRunning(false)
    }
  }

  const publish = async () => {
    if (!workflow) return
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
      await saveQueue.current?.flush()
      const revision = saveQueue.current?.getRevision() ?? workflow.draftRevision
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

  const executeIntent = (intent: WorkbenchIntent) => {
    if (intent.kind === 'open-library') {
      workbench.request({ kind: 'close' }, false)
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
    setLibraryOpen(false)
    workbench.request(intent, false)
  }

  const requestIntent = (intent: WorkbenchIntent) => {
    if (intent.kind === 'config' && workbench.mode.kind === 'config' && intent.nodeId === workbench.mode.nodeId) return
    if (configDraft.dirty && workbench.mode.kind === 'config') workbench.request(intent, true)
    else executeIntent(intent)
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
      <header className="studio-toolbar">
        <div className="studio-title"><Link to="/workflows" aria-label="返回工作流列表">←</Link><div><strong>{workflow.name}</strong><small>{saveLabel(saveState)}</small></div></div>
        <div className="studio-actions">
          <Link to={`/workflows/${workflow.id}/runs`}>运行记录</Link>
          <button type="button" onClick={() => requestIntent({ kind: 'open-library' })}>添加节点</button>
          <button type="button" onClick={() => requestIntent({ kind: 'test' })}>测试运行</button>
          <button type="button" onClick={() => requestIntent({ kind: 'export' })} disabled={exporting}>{exporting ? '导出中…' : '导出模板'}</button>
          <button className="primary-button" type="button" onClick={() => requestIntent({ kind: 'publish' })}>发布</button>
          {exportError && <span className="studio-toolbar-error" role="alert">{exportError}</span>}
        </div>
      </header>
      <WorkflowCanvas nodes={nodes} edges={edges} onNodesChange={handleNodesChange} onEdgesChange={handleEdgesChange} onConnect={handleConnect} isValidConnection={isValidConnection} onNodeClick={(node) => requestIntent({ kind: 'config', nodeId: node.id })} />
      {libraryOpen && <NodeLibraryDrawer definitions={definitions} onAdd={addNode} onClose={() => setLibraryOpen(false)} />}
      {workbench.mode.kind !== 'closed' && <WorkbenchPanel titleId="studio-workbench-title" onRequestClose={() => requestIntent({ kind: 'close' })}>
        {workbench.mode.kind === 'config' && selectedNode && <NodeConfigPanel titleId="studio-workbench-title" node={selectedNode} draft={configDraft} onApply={applySelectedConfig} />}
        {workbench.mode.kind === 'test' && <><header className="workbench-heading"><span className="node-category">调试</span><h2 id="studio-workbench-title">测试运行</h2></header><TestRunPanel schema={startSchema} events={events} running={running} error={runError} onRun={runDraft} onCancel={() => runController.current?.abort()} /></>}
      </WorkbenchPanel>}
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
    </main>
  )
}

function createID(prefix: string) {
  return `${prefix}-${crypto.randomUUID()}`
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
  if (!connection.source || !connection.target || !connection.sourceHandle || !connection.targetHandle || connection.source === connection.target) return false
  const source = nodes.find((node) => node.id === connection.source)
  const target = nodes.find((node) => node.id === connection.target)
  const output = source?.data.ports.outputs.find((port) => port.key === connection.sourceHandle)
  const input = target?.data.ports.inputs.find((port) => port.key === connection.targetHandle)
  if (!output || !input || (output.type !== 'any' && input.type !== 'any' && output.type !== input.type)) return false
  if (input.cardinality === 'one' && edges.some((edge) => edge.id !== ('id' in connection ? connection.id : undefined) && edge.target === connection.target && edge.targetHandle === connection.targetHandle)) return false
  return true
}

export function markInvalidEdges(nodes: StudioNode[], edges: StudioEdge[]) {
  return edges.map((edge) => {
    const invalid = !validateConnection(edge, nodes, edges)
    return { ...edge, data: { ...edge.data, invalid }, style: invalid ? { stroke: '#d92d20', strokeDasharray: '5 4' } : undefined }
  })
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

function saveLabel(state: SaveState) {
  return { saved: '已保存', pending: '等待保存', saving: '保存中…', conflict: '保存冲突，请刷新', error: '保存失败' }[state]
}

function publicMessage(error: unknown) {
  if (error instanceof APIError && error.code === 'WORKFLOW_TEMPLATE_INVALID') return '当前草稿不能导出，请先修复工作流问题'
  if (error instanceof APIError) return error.message
  return '操作失败，请稍后重试'
}
