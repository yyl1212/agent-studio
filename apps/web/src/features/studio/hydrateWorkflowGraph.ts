import type { NodeDefinition, ResolvedPorts, Workflow } from '../../lib/api/client'
import { portsFromDefinition, toFlowGraph } from './graphAdapter'
import type { FlowGraph } from './types'

export type ResolveNodePorts = (
	type: string,
	version: string,
	config: Record<string, unknown>,
	signal: AbortSignal,
) => Promise<ResolvedPorts>

export async function hydrateWorkflowGraph(
	workflow: Workflow,
	definitions: NodeDefinition[],
	resolve: ResolveNodePorts,
	signal: AbortSignal,
): Promise<FlowGraph> {
	const resolvedEntries = await Promise.all(workflow.draftGraph.nodes.map(async (node) => {
		try {
			return [node.id, await resolve(node.type, node.typeVersion, node.config, signal)] as const
		} catch (error) {
			if (signal.aborted) throw error
			const definition = definitions.find((candidate) => candidate.type === node.type && candidate.version === node.typeVersion)
			return [node.id, portsFromDefinition(definition)] as const
		}
	}))
	return toFlowGraph(workflow.draftGraph, definitions, Object.fromEntries(resolvedEntries))
}
