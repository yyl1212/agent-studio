import { describe, expect, it, vi } from 'vitest'

import { APIError, type NodeDefinition, type Workflow } from '../../lib/api/client'
import { portsFromDefinition } from './graphAdapter'
import { hydrateWorkflowGraph } from './hydrateWorkflowGraph'

const definition = (type: string): NodeDefinition => ({
	type, version: '1', title: type, description: '', category: '测试', configSchema: { type: 'object' },
	inputs: [{ key: 'fallback', title: '回退输入', type: 'string', required: false, cardinality: 'one' }], outputs: [],
	capabilities: [], executionSafety: 'pure', package: { name: 'test', displayName: 'test', license: 'MIT', repository: '', source: 'builtin' },
})

const definitions = [definition('dynamic'), definition('missing')]
const workflow: Workflow = {
	id: 'w1', name: '演示', slug: 'demo', description: '', draftRevision: 3,
	agentPresentation: { title: '演示', description: '', accent: 'indigo', submitLabel: '运行', resultMode: 'auto' },
	draftGraph: { schemaVersion: 1, nodes: [
		{ id: 'one', type: 'dynamic', typeVersion: '1', position: { x: 0, y: 0 }, config: {} },
		{ id: 'two', type: 'missing', typeVersion: '1', position: { x: 100, y: 0 }, config: {} },
	], edges: [] }, createdAt: '2026-08-27T00:00:00Z', updatedAt: '2026-08-27T00:00:00Z',
}

describe('hydrateWorkflowGraph', () => {
	it('并行解析动态端口并在单节点失败时安全回退', async () => {
		const first = deferred<{ inputs: NodeDefinition['inputs']; outputs: NodeDefinition['outputs'] }>()
		const resolve = vi.fn()
			.mockImplementationOnce(() => first.promise)
			.mockRejectedValueOnce(new APIError(404, 'NOT_FOUND', 'missing'))
		const promise = hydrateWorkflowGraph(workflow, definitions, resolve, new AbortController().signal)
		expect(resolve).toHaveBeenCalledTimes(2)
		first.resolve({ inputs: [{ key: 'in', title: '输入', type: 'string', required: false, cardinality: 'one' }], outputs: [] })
		const flow = await promise
		expect(flow.nodes[0].data.ports.inputs[0].key).toBe('in')
		expect(flow.nodes[1].data.ports).toEqual(portsFromDefinition(flow.nodes[1].data.definition))
	})
})

function deferred<T>() {
	let resolve!: (value: T) => void
	const promise = new Promise<T>((resolvePromise) => { resolve = resolvePromise })
	return { promise, resolve }
}
