import { api } from '$lib/api';
import { connectorSpec } from '$lib/connectors';
import { addEdge, removeSteps } from '$lib/edit';
import { makeNode, NODE_SPECS, uniqueKey } from '$lib/nodes';
import { builtInRollback, compensationCandidates, joinSaga, leaveSaga, sagaRoles, setCompensation } from '$lib/saga';
import { app } from '$lib/state.svelte';
import { handlesFor, isConnector, isEntry, SINGLE_TYPES, type ConnectorType, type Definition, type FlowNode, type NodeType, type SagaConfig } from '$lib/types';

// The copilot's callback tools: the copilot Workflow asks, this tab carries
// the call out on the open flow, as unsaved changes, and the result goes back
// to the model. Steps are named by key. A handler throws to report a
// problem the model should correct.

type Args = Record<string, unknown>;
type Result = { summary: string } & Record<string, unknown>;

const KEY = /^[a-z][a-z0-9_]*$/;

/** The flow as the copilot sees it: steps by key, edges between keys. */
export function compactFlow(def: Definition) {
	const keyOf = new Map(def.nodes.map((n) => [n.id, n.key]));
	return {
		name: def.name,
		description: def.description,
		variables: def.settings.variables ?? [],
		steps: def.nodes.map((n) => ({
			key: n.key,
			type: n.type,
			label: n.label,
			outcomes: handlesFor(n),
			config: n.type === 'saga' ? {} : n.config
		})),
		edges: def.edges.map((e) => ({ from: keyOf.get(e.source), outcome: e.sourceHandle || 'out', to: keyOf.get(e.target) })),
		sagas: def.nodes
			.filter((n) => n.type === 'saga')
			.map((s) => ({
				saga: s.key,
				members: ((s.config as SagaConfig).members ?? []).map((m) => ({
					step: keyOf.get(m.step),
					unwinds: m.unwinds,
					undo: m.compensation ? keyOf.get(m.compensation) : m.builtIn ? 'builtin' : 'none'
				}))
			}))
	};
}

function flowOrThrow(): Definition {
	if (!app.flow) throw new Error('no flow is open in the editor');
	return app.flow;
}

function step(def: Definition, key: unknown): FlowNode {
	const n = def.nodes.find((n) => n.key === key);
	if (!n) throw new Error(`no step with key "${String(key)}"; steps are: ${def.nodes.map((n) => n.key).join(', ')}`);
	return n;
}

function outcomeOf(n: FlowNode, outcome: unknown): string {
	const handles = handlesFor(n);
	const h = typeof outcome === 'string' && outcome ? outcome : handles[0];
	if (!handles.includes(h)) throw new Error(`${n.key} has no outcome "${h}"; its outcomes are: ${handles.join(', ') || 'none'}`);
	return h;
}

/** A merge patch: values replace, null removes, objects merge. */
function mergePatch(target: Record<string, unknown>, patch: unknown): void {
	if (!patch || typeof patch !== 'object' || Array.isArray(patch)) throw new Error('config must be an object');
	for (const [k, v] of Object.entries(patch as Record<string, unknown>)) {
		if (v === null) delete target[k];
		else if (typeof v === 'object' && !Array.isArray(v) && target[k] && typeof target[k] === 'object' && !Array.isArray(target[k])) {
			mergePatch(target[k] as Record<string, unknown>, v);
		} else target[k] = v;
	}
}

function setKey(def: Definition, n: FlowNode, key: unknown) {
	if (typeof key !== 'string' || !KEY.test(key)) throw new Error(`key "${String(key)}" must be lower_snake_case, starting with a letter`);
	if (def.nodes.some((o) => o.id !== n.id && o.key === key)) throw new Error(`another step already has the key "${key}"`);
	n.key = key;
}

/** Note what the copilot changed, and redraw (and tidy, if that's on). */
function changed(n?: FlowNode) {
	if (n) app.copilotTouched.add(n.id);
	app.structure++;
	if (app.autoTidy) app.requestTidy(n?.id);
}

/** Where a new step goes: after the step it follows, or past the rest. */
function placeAfter(def: Definition, prev?: FlowNode) {
	const lr = app.direction === 'LR';
	if (prev) {
		const siblings = def.edges.filter((e) => e.source === prev.id).length;
		return lr ? { x: prev.position.x + 320, y: prev.position.y + siblings * 170 } : { x: prev.position.x + siblings * 320, y: prev.position.y + 170 };
	}
	const far = def.nodes.reduce((m, n) => Math.max(m, lr ? n.position.x : n.position.y), 0);
	return lr ? { x: far + 320, y: 160 } : { x: 160, y: far + 170 };
}

const handlers: Record<string, (args: Args) => Result | Promise<Result>> = {
	get_flow: () => ({ summary: 'Read the canvas', ...compactFlow(flowOrThrow()) }),

	add_step: (a) => {
		const def = flowOrThrow();
		const type = a.type as NodeType;
		if (!(type in NODE_SPECS)) throw new Error(`no step type "${String(a.type)}"`);
		if (SINGLE_TYPES.includes(type) && def.nodes.some((n) => n.type === type)) throw new Error(`a flow has only one ${type} step`);
		let operation: string | undefined;
		if (isConnector(type)) {
			const spec = connectorSpec(type as ConnectorType);
			operation = a.operation as string;
			if (!spec?.operations.some((o) => o.name === operation)) {
				throw new Error(`${type} needs an operation: one of ${spec?.operations.map((o) => o.name).join(', ')}`);
			}
		}
		const after = a.after as { step?: string; outcome?: string } | undefined;
		const prev = after?.step ? step(def, after.step) : undefined;
		const handle = prev ? outcomeOf(prev, after?.outcome) : undefined;
		const node = makeNode(type, placeAfter(def, prev), def.nodes, operation);
		if (typeof a.label === 'string' && a.label.trim()) {
			node.label = a.label.trim();
			node.key = uniqueKey(node.label, def.nodes);
		}
		if (a.key !== undefined) setKey(def, node, a.key);
		if (a.config !== undefined) mergePatch(node.config as Record<string, unknown>, a.config);
		def.nodes.push(node);
		if (prev && handle) addEdge(def, prev.id, node.id, handle);
		changed(node);
		return {
			summary: `Added ${node.label}${prev ? ` after ${prev.label}` : ''}`,
			key: node.key,
			outcomes: handlesFor(node),
			config: node.config
		};
	},

	update_step: (a) => {
		const def = flowOrThrow();
		const n = step(def, a.step);
		const was = n.label;
		const patch = a.config as Record<string, unknown> | undefined;
		if (patch !== undefined && (!patch || typeof patch !== 'object' || Array.isArray(patch))) throw new Error('config must be an object');
		if (patch && isConnector(n.type) && 'operation' in patch && patch.operation !== (n.config as { operation?: string }).operation) {
			throw new Error("a connector step's operation is fixed; remove the step and add one with the other operation");
		}
		if (a.key !== undefined) setKey(def, n, a.key);
		if (typeof a.label === 'string' && a.label.trim()) n.label = a.label.trim();
		if (patch) mergePatch(n.config as Record<string, unknown>, patch);
		changed(n);
		return { summary: `Changed ${was}`, key: n.key, outcomes: handlesFor(n), config: n.config };
	},

	remove_step: (a) => {
		const def = flowOrThrow();
		const n = step(def, a.step);
		if (n.type === 'trigger') throw new Error("the trigger can't be removed; change its kind instead");
		removeSteps(def, new Set([n.id]));
		app.copilotTouched.delete(n.id);
		if (app.selectedNodeId === n.id) app.selectedNodeId = null;
		changed();
		return { summary: `Removed ${n.label}` };
	},

	connect: (a) => {
		const def = flowOrThrow();
		const from = step(def, a.from);
		const to = step(def, a.to);
		if (from.id === to.id) throw new Error("a step can't connect to itself");
		if (to.type === 'saga') throw new Error('nothing connects into a Saga step; use set_saga_member');
		if (isEntry(to.type)) throw new Error(`${to.key} starts a path; nothing connects into it`);
		const handle = outcomeOf(from, a.outcome);
		addEdge(def, from.id, to.id, handle);
		changed(to);
		return { summary: `Connected ${from.label} → ${to.label}${handle === handlesFor(from)[0] ? '' : ` (${handle})`}` };
	},

	disconnect: (a) => {
		const def = flowOrThrow();
		const from = step(def, a.from);
		const to = step(def, a.to);
		const before = def.edges.length;
		def.edges = def.edges.filter((e) => !(e.source === from.id && e.target === to.id && (!a.outcome || (e.sourceHandle || 'out') === a.outcome)));
		if (def.edges.length === before) throw new Error(`no edge from ${from.key} to ${to.key}`);
		changed();
		return { summary: `Disconnected ${from.label} → ${to.label}` };
	},

	set_saga_member: (a) => {
		const def = flowOrThrow();
		const saga = step(def, a.saga);
		if (saga.type !== 'saga') throw new Error(`${saga.key} isn't a Saga step`);
		const n = step(def, a.step);
		if (isEntry(n.type) || n.type === 'end' || n.type === 'respond') throw new Error(`${n.key} can't be in a saga`);
		// Check everything before changing anything.
		const undo = a.undo;
		const current = sagaRoles(def).get(n.id)?.member;
		if (undo === 'builtin' && !builtInRollback(n)) throw new Error(`${n.key} has no built-in rollback`);
		let comp: FlowNode | undefined;
		if (typeof undo === 'string' && undo && undo !== 'builtin' && undo !== 'none') {
			comp = step(def, undo);
			const probe = { step: n.id, unwinds: true, compensation: current?.compensation };
			if (!compensationCandidates(def, probe).some((c) => c.id === comp!.id)) {
				throw new Error(`${comp.key} can't undo ${n.key}: a compensation step has nothing connected into it and isn't in a saga or undoing another step`);
			}
		}
		if (sagaRoles(def).get(n.id)?.saga.id !== saga.id) joinSaga(def, saga, n);
		const member = sagaRoles(def).get(n.id)!.member;
		member.unwinds = a.unwinds !== false;
		if (undo === 'builtin') {
			setCompensation(member, undefined);
			member.builtIn = true;
		} else if (undo === 'none' || undo === null) {
			setCompensation(member, undefined);
			delete member.builtIn;
		} else if (comp) setCompensation(member, comp.id);
		changed(n);
		const how = member.compensation ? `undone by ${step(def, compKey(def, member.compensation)).label}` : member.builtIn ? 'built-in rollback' : 'no undo';
		return { summary: `${n.label} in ${saga.label}: ${member.unwinds ? 'unwinds' : 'handled'}, ${how}` };
	},

	remove_saga_member: (a) => {
		const def = flowOrThrow();
		const saga = step(def, a.saga);
		const n = step(def, a.step);
		leaveSaga(def, n.id);
		changed(n);
		return { summary: `Took ${n.label} out of ${saga.label}` };
	},

	update_flow: (a) => {
		const def = flowOrThrow();
		if (typeof a.name === 'string' && a.name.trim()) def.name = a.name.trim();
		if (typeof a.description === 'string') def.description = a.description;
		if (Array.isArray(a.variables)) {
			def.settings.variables = (a.variables as { name: string; initial?: string; visible?: boolean }[]).map((v) => {
				if (!KEY.test(v.name ?? '')) throw new Error(`variable name "${v.name}" must be lower_snake_case`);
				return { name: v.name, ...(v.initial !== undefined ? { initial: String(v.initial) } : {}), ...(v.visible ? { visible: true } : {}) };
			});
		}
		return { summary: 'Changed the flow settings' };
	},

	tidy_up: () => {
		app.requestTidy();
		return { summary: 'Tidied up the canvas' };
	},

	check_flow: async () => {
		const def = flowOrThrow();
		const { problems } = await api.validateFlow(JSON.parse(JSON.stringify(def)) as Definition);
		app.problems = problems;
		const keyOf = new Map(def.nodes.map((n) => [n.id, n.key]));
		return {
			summary: problems.length ? `Found ${problems.length} problem${problems.length === 1 ? '' : 's'}` : 'No problems',
			problems: problems.map((p) => ({ step: p.nodeId ? keyOf.get(p.nodeId) : undefined, message: p.message }))
		};
	}
};

function compKey(def: Definition, id: string): string {
	return def.nodes.find((n) => n.id === id)?.key ?? id;
}

/** Carries out one tool call. Throws when the call can't be done. */
export async function runTool(name: string, args: Args): Promise<Result> {
	const handler = handlers[name];
	if (!handler) throw new Error(`the editor has no tool "${name}"`);
	return handler(args ?? {});
}
