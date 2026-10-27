import { forgetSteps } from './saga';
import type { Definition, FlowEdge } from './types';

// Changes to a flow's structure shared by the canvas, the inspector and the
// copilot, so each makes them the same way. Callers signal the canvas
// (app.structure++) themselves.

/** Connect an outcome to a step, unless that edge exists. Returns the edge. */
export function addEdge(def: Definition, source: string, target: string, handle = 'out'): FlowEdge {
	const existing = def.edges.find((e) => e.source === source && e.target === target && (e.sourceHandle || 'out') === handle);
	if (existing) return existing;
	const edge = { id: `e${Math.random().toString(36).slice(2, 9)}`, source, target, sourceHandle: handle };
	def.edges.push(edge);
	return edge;
}

/** Remove steps, their edges, and any saga membership or compensation that named them. */
export function removeSteps(def: Definition, ids: Set<string>): void {
	if (!ids.size) return;
	forgetSteps(def, ids);
	def.nodes = def.nodes.filter((n) => !ids.has(n.id));
	def.edges = def.edges.filter((e) => !ids.has(e.source) && !ids.has(e.target));
}
