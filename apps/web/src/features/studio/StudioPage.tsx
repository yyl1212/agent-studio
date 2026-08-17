import {
  addEdge,
  applyEdgeChanges,
  applyNodeChanges,
  type Connection,
  type EdgeChange,
  type NodeChange,
} from '@xyflow/react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { Link, useParams } from 'react-router-dom'

import type { JSONSchema } from '../../components/schema-form/types'
import { APIError, api, type NodeDefinition, type Workflow } from '../../lib/api/client'
import { readNDJSON, type RunEvent } from '../../lib/api/ndjson'
import { ConfigDrawer } from './ConfigDrawer'
import { fromFlowGraph, normalizePorts, portsFromDefinition, toFlowGraph } from './graphAdapter'
import { NodeLibraryDrawer } from './NodeLibraryDrawer'
import { PublishDialog } from './PublishDialog'
import { SaveQueue, type SaveState } from './saveQueue'
import { TestRunDrawer } from './TestRunDrawer'
import type { StudioEdge, StudioNode } from './types'
import { WorkflowCanvas } from './WorkflowCanvas'
import '@xyflow/react/dist/style.css'
import './studio.css'

export function StudioPage() {
  const { id = '' } = useParams()
  const [workflow, setWorkflow] = useState<Workflow>()
  const [definitions, setDefinitions] = useState<NodeDefinition[]>([])
  const [nodes, setNodes] = useState<StudioNode[]>([])
  const [edges, setEdges] = useState<StudioEdge[]>([])
  const [selectedID, setSelectedID] = useState<string>()
  const [libraryOpen, setLibraryOpen] = useState(false)
  const [testOpen, setTestOpen] = useState(false)
  const [publishOpen, setPublishOpen] = useState(false)
  const [publishedVersion, setPublishedVersion] = useState<number>()
  const [publishError, setPublishError] = useState('')
  const [publishing, setPublishing] = useState(false)
  const [saveState, setSaveState] = useState<SaveState>('saved')
  const [loadError, setLoadError] = useState('')
  const [events, setEvents] = useState<RunEvent[]>([])
  const [runError, setRunError] = useState('')
  const [running, setRunning] = useState(false)
  const saveQueue = useRef<SaveQueue | undefined>(undefined)
  const runController = useRef<AbortController | undefined>(undefined)

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

  const selectedNode = nodes.find((node) => node.id === selectedID)
  const resolveKey = JSON.stringify(nodes.map((node) => [node.id, node.data.nodeType, node.data.typeVersion, node.data.config]))
  useEffect(() => {
    if (nodes.length === 0) return
    const controller = new AbortController()
    const timer = setTimeout(() => {
      Promise.all(nodes.map(async (node) => [node.id, await api.resolveNodeType(node.data.nodeType, node.data.typeVersion, node.data.config, controller.signal)] as const))
        .then((resolved) => {
          const portsByID = new Map(resolved)
          setNodes((currentNodes) => {
            const nextNodes = currentNodes.map((node) => portsByID.has(node.id) ? { ...node, data: { ...node.data, ports: normalizePorts(portsByID.get(node.id)!) } } : node)
            setEdges((currentEdges) => markInvalidEdges(nextNodes, currentEdges))
            return nextNodes
          })
        })
        .catch(() => undefined)
    }, 250)
    return () => { clearTimeout(timer); controller.abort() }
  }, [resolveKey])

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
      position: { x: 320, y: 180 + addedCount * 220 },
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
    setSelectedID(node.id)
    setLibraryOpen(false)
    commit(next, edges)
  }

  const updateSelectedConfig = (config: Record<string, unknown>) => {
    setNodes((current) => {
      const next = current.map((node) => node.id === selectedID ? { ...node, data: { ...node.data, config } } : node)
      commit(next, edges)
      return next
    })
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
        setSelectedID(validation.issues.find((issue) => issue.nodeId)?.nodeId)
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

  if (loadError) return <main className="page-container"><p role="alert">{loadError}</p></main>
  if (!workflow) return <main className="page-container" aria-live="polite">正在加载编辑器…</main>

  return (
    <main className="studio-page">
      <header className="studio-toolbar">
        <div className="studio-title"><Link to="/workflows" aria-label="返回工作流列表">←</Link><div><strong>{workflow.name}</strong><small>{saveLabel(saveState)}</small></div></div>
        <div className="studio-actions">
          <Link to={`/workflows/${workflow.id}/runs`}>运行记录</Link>
          <button type="button" onClick={() => setLibraryOpen(true)}>添加节点</button>
          <button type="button" onClick={() => setTestOpen(true)}>测试运行</button>
          <button className="primary-button" type="button" onClick={() => { setPublishOpen(true); setPublishedVersion(undefined); setPublishError('') }}>发布</button>
        </div>
      </header>
      <WorkflowCanvas nodes={nodes} edges={edges} onNodesChange={handleNodesChange} onEdgesChange={handleEdgesChange} onConnect={handleConnect} isValidConnection={isValidConnection} onNodeClick={(node) => setSelectedID(node.id)} />
      {libraryOpen && <NodeLibraryDrawer definitions={definitions} onAdd={addNode} onClose={() => setLibraryOpen(false)} />}
      {selectedNode && <ConfigDrawer node={selectedNode} onChange={updateSelectedConfig} onClose={() => setSelectedID(undefined)} />}
      {testOpen && <TestRunDrawer schema={startSchema} events={events} running={running} error={runError} onRun={runDraft} onCancel={() => runController.current?.abort()} onClose={() => setTestOpen(false)} />}
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
  if (error instanceof APIError) return error.message
  return '操作失败，请稍后重试'
}
