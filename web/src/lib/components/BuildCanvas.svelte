<script lang="ts">
	import { untrack } from 'svelte';
	import { SvelteFlow, Background, BackgroundVariant, Controls, MiniMap, Panel, useSvelteFlow, type Connection, type Edge, type NodeTypes, type EdgeTypes } from '@xyflow/svelte';
	import LoopExitEdge from './LoopExitEdge.svelte';
	import LoopBackEdge from './LoopBackEdge.svelte';
	import Sparkles from '@lucide/svelte/icons/sparkles';
	import X from '@lucide/svelte/icons/x';
	import FlowNodeCard, { type CardNode } from './FlowNodeCard.svelte';
	import GhostNode from './GhostNode.svelte';
	import { cardSagas, ghostNodes, isGhost, sagaEdges } from '$lib/sagaCanvas';
	import { compensationSteps, joinSaga, leaveSaga, sagaRoles, setCompensation } from '$lib/saga';
	import { addEdge, removeSteps } from '$lib/edit';
	import FitOnResize from './FitOnResize.svelte';
	import LaneBands from './LaneBands.svelte';
	import Scopes from './Scopes.svelte';
	import { Button } from '$lib/components/ui/button';
	import { app } from '$lib/state.svelte';
	import { makeNode } from '$lib/nodes';
	import { toEdges } from '$lib/edges';
	import { bodyScopes, laneBands, sagaScopes, suggestDirection, tidy, type Band, type Scope, type Size } from '$lib/layout';
	import { isEntry, SINGLE_TYPES, type NodeType, type Position } from '$lib/types';

	const nodeTypes: NodeTypes = { step: FlowNodeCard, ghost: GhostNode };
	const edgeTypes: EdgeTypes = { loopExit: LoopExitEdge, loopBack: LoopBackEdge };
	const { screenToFlowPosition, fitView, setCenter, getViewport, setViewport } = useSvelteFlow();

	let nodes = $state.raw<CardNode[]>([]);
	let edges = $state.raw<Edge[]>([]);
	let bands = $state.raw<Band[]>([]);
	let scopes = $state.raw<Scope[]>([]);
	let width = $state(0);
	let dismissed = $state<Record<string, boolean>>({});
	let positionsRev = $state(0);

	function sizes(): Map<string, Size> {
		return new Map(
			nodes.filter((n) => !isGhost(n.id)).map((n) => [n.id, { width: n.measured?.width ?? 248, height: n.measured?.height ?? 96 }])
		);
	}

	function refreshBands() {
		const flow = app.flow;
		if (!flow) return ((bands = []), (scopes = []));
		const pos = new Map<string, Position>(nodes.filter((n) => !isGhost(n.id)).map((n) => [n.id, n.position]));
		bands = laneBands(flow, pos, sizes());
		scopes = [...bodyScopes(flow, pos, sizes()), ...sagaScopes(flow, pos, sizes())];
		// Steps that sit inside a saga's or a loop body's box without being in it say so.
		const stranger = new Map<string, string>();
		for (const sc of scopes) for (const id of sc.strangers) stranger.set(id, flow.nodes.find((n) => n.id === sc.id)?.label ?? 'the scope');
		if (nodes.some((n) => !isGhost(n.id) && (n.data as { stranger?: string }).stranger !== stranger.get(n.id))) {
			nodes = nodes.map((n) => (isGhost(n.id) ? n : { ...n, data: { ...n.data, stranger: stranger.get(n.id) } }));
		}
	}

	// Rebuild the canvas when the flow is swapped, its structure changes, or
	// the direction flips. Config and label edits flow through the node's data proxy.
	$effect(() => {
		const _rev = app.structure;
		const direction = app.direction;
		const flow = untrack(() => app.flow);
		if (!flow) {
			nodes = [];
			edges = [];
			bands = [];
			return;
		}
		untrack(() => {
			const selected = app.selectedNodeId;
			const sagas = cardSagas(flow);
			// Outcomes with an edge keep their labels.
			const connected = new Map<string, string[]>();
			for (const e of flow.edges) connected.set(e.source, [...(connected.get(e.source) ?? []), e.sourceHandle || 'out']);
			nodes = [
				...flow.nodes.map((n) => ({
					id: n.id,
					type: 'step' as const,
					position: { ...n.position },
					data: { node: n, run: false, direction, saga: sagas.get(n.id), connected: connected.get(n.id) },
					selected: n.id === selected,
					deletable: n.type !== 'trigger'
				})),
				// Built-in rollbacks, as ghosts under their members.
				...(ghostNodes(flow, null, direction) as unknown as CardNode[])
			];
			edges = [...toEdges(flow), ...sagaEdges(flow, null, true, selected)];
			positionsRev++;
		});
		// Bands need measured node sizes, which exist after the next render.
		const t = setTimeout(refreshBands, 60);
		return () => clearTimeout(t);
	});

	// A saga's member links show while the saga or one of its members is
	// selected.
	$effect(() => {
		const sel = app.selectedNodeId;
		untrack(() => {
			const flow = app.flow;
			if (!flow) return;
			const sagaLinks = new Map(sagaEdges(flow, null, true, sel).map((e) => [e.id, e]));
			if (edges.some((e) => sagaLinks.has(e.id) && !!e.hidden !== !!sagaLinks.get(e.id)!.hidden)) {
				edges = edges.map((e) => (sagaLinks.has(e.id) ? { ...e, hidden: sagaLinks.get(e.id)!.hidden } : e));
			}
		});
	});

	// Tidy up: layered layout in the current direction, using measured sizes.
	let handledTidy = untrack(() => app.tidyRequest);
	$effect(() => {
		const req = app.tidyRequest;
		if (req === handledTidy) return;
		handledTidy = req;
		untrack(() => {
			const flow = app.flow;
			if (!flow) return;
			const follow = app.tidyFollow;
			const anchor = 'anchor' in follow ? flow.nodes.find((n) => n.id === follow.anchor) : undefined;
			const before = anchor && { ...anchor.position };
			const laid = tidy(flow, app.direction, sizes());
			for (const n of flow.nodes) if (laid[n.id]) n.position = laid[n.id];
			app.structure++;
			if (anchor && before) {
				// Pan so the step you just connected stays where it was on screen.
				const { x, y, zoom } = getViewport();
				setViewport({ x: x - (anchor.position.x - before.x) * zoom, y: y - (anchor.position.y - before.y) * zoom, zoom }, { duration: 200 });
			} else setTimeout(() => fitView({ padding: 0.08, maxZoom: 1.15, duration: 250 }), 120);
		});
	});

	const suggestion = $derived.by(() => {
		const _r = positionsRev;
		const flow = app.flow;
		if (!flow || dismissed[flow.id]) return null;
		return suggestDirection(flow, app.direction);
	});

	function syncPositions() {
		const flow = app.flow;
		if (!flow) return;
		for (const n of nodes) {
			if (isGhost(n.id)) continue;
			const target = flow.nodes.find((f) => f.id === n.id);
			if (target && (target.position.x !== n.position.x || target.position.y !== n.position.y)) {
				target.position = { x: Math.round(n.position.x), y: Math.round(n.position.y) };
			}
		}
		positionsRev++;
		refreshBands();
	}

	function onconnect(c: Connection) {
		const flow = app.flow;
		if (!flow) return;
		// Saga links are settings on the Saga step, not control-flow edges.
		if (c.sourceHandle === 'saga') {
			const saga = flow.nodes.find((n) => n.id === c.target);
			const step = flow.nodes.find((n) => n.id === c.source);
			if (saga && step) joinSaga(flow, saga, step);
			app.structure++;
			if (app.autoTidy) app.requestTidy(c.source);
			return;
		}
		if (c.sourceHandle === 'compensate') {
			const role = sagaRoles(flow).get(c.source);
			if (role) setCompensation(role.member, c.target);
			app.structure++;
			if (app.autoTidy) app.requestTidy(c.source);
			return;
		}
		addEdge(flow, c.source, c.target, c.sourceHandle || 'out');
		app.structure++;
		// Auto tidy-up: lay the flow out again, keeping your place on the canvas.
		if (app.autoTidy) app.requestTidy(c.source);
	}

	function isValidConnection(c: Connection | Edge) {
		if (c.source === c.target || isGhost(c.target)) return false;
		const flow = app.flow;
		const target = flow?.nodes.find((n) => n.id === c.target);
		if (!flow || !target) return false;
		// To a Saga only from a step's saga handle; from that handle only to a Saga.
		if (c.sourceHandle === 'saga' || c.targetHandle === 'members') return c.sourceHandle === 'saga' && target.type === 'saga' && c.targetHandle === 'members';
		// A compensation is a step nothing leads into, that isn't in a saga itself.
		if (c.sourceHandle === 'compensate') {
			return !isEntry(target.type) && !flow.edges.some((e) => e.target === target.id) && !sagaRoles(flow).has(target.id) && !compensationSteps(flow).has(target.id);
		}
		return !isEntry(target.type);
	}

	function ondelete({ nodes: gone, edges: goneEdges }: { nodes: { id: string }[]; edges: { id: string }[] }) {
		const flow = app.flow;
		if (!flow) return;
		const nodeIds = new Set(gone.map((n) => n.id).filter((id) => !isGhost(id)));
		const edgeIds = new Set(goneEdges.map((e) => e.id));
		// Saga links: removing one changes the Saga's settings.
		for (const id of edgeIds) {
			if (id.startsWith('saga:')) leaveSaga(flow, id.split(':')[2]);
			if (id.startsWith('comp:')) {
				const role = sagaRoles(flow).get(id.slice(5));
				if (role) {
					setCompensation(role.member, undefined);
					delete role.member.builtIn;
				}
			}
		}
		removeSteps(flow, nodeIds);
		flow.edges = flow.edges.filter((e) => !edgeIds.has(e.id));
		if (app.selectedNodeId && nodeIds.has(app.selectedNodeId)) app.selectedNodeId = null;
		app.structure++;
	}

	function addNode(type: NodeType, clientPoint?: { x: number; y: number }, variant?: string) {
		const flow = app.flow;
		if (!flow) return;
		if (SINGLE_TYPES.includes(type) && flow.nodes.some((n) => n.type === type)) return;
		let position: { x: number; y: number };
		const lr = app.direction === 'LR';
		if (clientPoint) {
			const p = screenToFlowPosition(clientPoint);
			position = { x: p.x - 124, y: p.y - 30 };
		} else {
			const sel = flow.nodes.find((n) => n.id === app.selectedNodeId);
			if (sel) position = lr ? { x: sel.position.x + 320, y: sel.position.y } : { x: sel.position.x, y: sel.position.y + 170 };
			else {
				const far = flow.nodes.reduce((m, n) => Math.max(m, lr ? n.position.x : n.position.y), 0);
				position = lr ? { x: far + 320, y: 160 } : { x: 160, y: far + 170 };
			}
		}
		const node = makeNode(type, { x: Math.round(position.x), y: Math.round(position.y) }, flow.nodes, variant);
		flow.nodes.push(node);
		app.selectedNodeId = node.id;
		app.structure++;
		// Click-to-add can land off-screen; bring the new step into view.
		if (!clientPoint) setCenter(node.position.x + 124, node.position.y + 48, { zoom: getViewport().zoom, duration: 250 });
	}

	// Click-to-add from the palette. Ignore a request made before this canvas mounted.
	let handledSeq = untrack(() => app.addRequest?.seq ?? 0);
	$effect(() => {
		const req = app.addRequest;
		if (req && req.seq > handledSeq) {
			handledSeq = req.seq;
			untrack(() => addNode(req.type, undefined, req.variant));
		}
	});

	function ondragover(e: DragEvent) {
		if (e.dataTransfer?.types.includes('application/x-flow-node')) {
			e.preventDefault();
			e.dataTransfer.dropEffect = 'copy';
		}
	}

	// The payload is a step type, or type:variant for a connector's operation.
	function ondrop(e: DragEvent) {
		const payload = e.dataTransfer?.getData('application/x-flow-node');
		if (!payload) return;
		e.preventDefault();
		const [type, variant] = payload.split(':') as [NodeType, string | undefined];
		addNode(type, { x: e.clientX, y: e.clientY }, variant);
	}

	function acceptSuggestion(d: 'LR' | 'TB') {
		app.setDirection(d);
		app.requestTidy();
	}
</script>

<div class="h-full w-full" role="application" bind:clientWidth={width} {ondragover} {ondrop}>
	<SvelteFlow
		bind:nodes
		bind:edges
		{nodeTypes}
		{edgeTypes}
		colorMode="dark"
		minZoom={0.2}
		maxZoom={1.8}
		snapGrid={[16, 16]}
		deleteKey={['Delete', 'Backspace']}
		{onconnect}
		{isValidConnection}
		{ondelete}
		onnodedragstop={syncPositions}
		onnodeclick={({ node }) => (app.selectedNodeId = isGhost(node.id) ? (node.data as unknown as { member: string }).member : node.id)}
		onpaneclick={() => (app.selectedNodeId = null)}
	>
		<LaneBands {bands} />
		<Scopes {scopes} select={(id) => (app.selectedNodeId = id)} />
		<Background variant={BackgroundVariant.Dots} gap={22} size={1.4} />
		<Controls showLock={false} />
		<MiniMap pannable zoomable nodeColor={() => '#3a4060'} />
		<FitOnResize {width} fitKey={app.flow ? `${app.flow.id}:${app.direction}` : ''} />
		{#if suggestion}
			<Panel position="top-center">
				<div class="flex items-center gap-3 border border-[var(--uv-light)] bg-[#161827]/95 px-3 py-2 text-sm shadow-lg">
					<Sparkles class="size-4 text-[var(--purple)]" />
					<span>
						{suggestion === 'TB'
							? "Looks like you're building top-down — switch to vertical?"
							: "Looks like you're building left to right — switch to horizontal?"}
					</span>
					<Button size="xs" class="btn-uv mono" onclick={() => acceptSuggestion(suggestion)}>Switch & tidy up</Button>
					<button
						class="text-[var(--slate-300)] hover:text-[var(--off-white)]"
						onclick={() => app.flow && (dismissed[app.flow.id] = true)}
						aria-label="Dismiss suggestion"><X class="size-3.5" /></button
					>
				</div>
			</Panel>
		{/if}
	</SvelteFlow>
</div>
