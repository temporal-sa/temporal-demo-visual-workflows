<script lang="ts" module>
	import type { Node } from '@xyflow/svelte';
	import type { Direction, FlowNode } from '$lib/types';
	import type { CardSaga } from '$lib/sagaCanvas';

	export type CardData = {
		node: FlowNode;
		run: boolean;
		direction: Direction;
		saga?: CardSaga;
		/** Outcomes with an edge, which keep their labels. */
		connected?: string[];
		/** Inside a saga's box without being in it: that saga's label. */
		stranger?: string;
	};
	export type CardNode = Node<CardData, 'step'>;
</script>

<script lang="ts">
	import { Handle, Position, type NodeProps } from '@xyflow/svelte';
	import ExternalLink from '@lucide/svelte/icons/external-link';
	import Undo2 from '@lucide/svelte/icons/undo-2';
	import Zap from '@lucide/svelte/icons/zap';
	import { NODE_SPECS } from '$lib/nodes';
	import { handlesFor, isEntry } from '$lib/types';
	import { hasBody } from '$lib/loops';
	import { app } from '$lib/state.svelte';
	import Sparkles from '@lucide/svelte/icons/sparkles';
	import { nodeTone, waitingText } from '$lib/status';

	let { id, data, selected }: NodeProps<CardNode> = $props();

	const spec = $derived(NODE_SPECS[data.node.type]);
	const handles = $derived(handlesFor(data.node));
	const main = $derived(handles[0]);
	const secondary = $derived(handles.slice(1));
	const lr = $derived(data.direction !== 'TB');
	const problems = $derived(data.run ? [] : app.problemsFor(id));
	const rs = $derived(data.run ? app.nodeRunState(id) : undefined);
	const pending = $derived(data.run ? app.pendingActivity(id) : undefined);
	const retrying = $derived(!!pending && pending.attempt > 1);
	const tone = $derived(nodeTone(rs?.status, retrying));
	const Icon = $derived(spec.icon);
	const entry = $derived(isEntry(data.node.type));
	const lane = $derived(data.node.type === 'onSignal' || data.node.type === 'onUpdate' || data.node.type === 'onStart');
	// Message lanes count what they handled; On start runs once.
	const counts = $derived(lane && data.node.type !== 'onStart');

	const isSaga = $derived(data.node.type === 'saga');
	// Added or changed by the copilot since the last save.
	const touched = $derived(!data.run && app.copilotTouched.has(id));
	// Outcome labels show where an edge leaves, or while the step is
	// selected; a run shows the outcome it took.
	const showLabel = (h: string) => selected || !!data.connected?.includes(h) || (!!rs && rs.handle === h);
	// In a run, whether this member's failure unwound its saga follows the live state.
	const unwoundIt = $derived(!!data.saga?.sagaId && data.run && app.shownState?.sagas?.[data.saga.sagaId]?.trigger === id);
	const sagaState = $derived(isSaga && data.run ? app.shownState?.sagas?.[id] : undefined);
	const nodeLabel = (nid: string) => app.shownState?.flow?.nodes.find((n) => n.id === nid)?.label ?? nid;

	// Secondary outcomes sit on the perpendicular side so unhappy paths branch
	// away from the main line: LR → bottom edge, TB → right edge. A Loop's or
	// For each's body takes that side for its box, so its other outcomes
	// branch the other way: LR → top edge, TB → left edge.
	const farSide = (h: string) => hasBody(data.node) && h !== 'body';
	const secondaryPos = (h: string) => {
		const side = secondary.filter((x) => farSide(x) === farSide(h));
		return side.length === 1 ? 62 : 38 + (side.indexOf(h) * 44) / (side.length - 1);
	};
	const undoColor = (u?: string) => (u === 'failed' ? 'var(--red)' : u === 'running' ? 'var(--pink)' : 'var(--indigo-200)');
</script>

<div
	class="group relative w-[248px] border bg-[#15171f]/95 text-left shadow-[0_8px_24px_-12px_rgba(0,0,0,.8)] transition-colors"
	class:pulse={tone.pulse}
	class:opacity-50={rs?.status === 'skipped'}
	style:border-color={problems.length ? 'var(--red)' : !rs && selected ? 'var(--uv-light)' : touched ? 'color-mix(in srgb, var(--uv-light) 70%, #2b3348)' : lane && !rs ? 'color-mix(in srgb, var(--purple) 55%, #2b3348)' : tone.border}
	style:outline={selected ? '2px solid var(--uv-light)' : 'none'}
	style:outline-offset="3px"
	style:--pulse-color={tone.glow}
	style:border-style={rs?.status === 'skipped' || data.stranger ? 'dashed' : 'solid'}
>
	{#if !entry}
		<!-- A compensation's link arrives from the track beside the path. -->
		<Handle type="target" position={data.saga?.compensates ? (lr ? Position.Top : Position.Left) : lr ? Position.Left : Position.Top} class="!h-3 !w-3 !rounded-none !border-2" />
	{/if}

	{#if data.stranger}
		<div class="mono absolute -top-4 left-0 text-[0.56rem] text-[var(--slate-500)]">not in {data.stranger}</div>
	{/if}
	{#if touched}
		<div class="mono absolute -top-4 right-0 flex items-center gap-0.5 text-[0.56rem] text-[var(--uv-light)]" data-tip="Added or changed by the copilot; unsaved"><Sparkles class="size-2.5" />copilot</div>
	{/if}
	<div class="flex items-start gap-2.5 px-3 pt-2.5 pb-2">
		<div
			class="mt-0.5 grid size-8 shrink-0 place-items-center border"
			style:color={spec.accent}
			style:border-color={spec.accent}
			style:background="color-mix(in srgb, {spec.accent} 12%, transparent)"
		>
			<Icon class="size-4" />
		</div>
		<div class="min-w-0 flex-1">
			<div class="flex items-center justify-between gap-2">
				<span class="eyebrow truncate !text-[0.6rem]" style:color={spec.eyebrow ? spec.accent : undefined}>{spec.eyebrow?.(data.node.config) ?? spec.label}</span>
				{#if rs}
					<span class="status-badge !text-[0.55rem]" style:color={tone.text}>{tone.label}</span>
				{/if}
			</div>
			<div class="truncate text-[0.95rem] leading-tight font-semibold text-[var(--off-white)]">{data.node.label}</div>
			<div class="mono mt-0.5 truncate text-[0.68rem] text-[var(--slate-300)]">{spec.summary(data.node.config)}</div>
			{#if !data.run && spec.badge?.(data.node.config)}
				{@const badge = spec.badge(data.node.config)!}
				<div
					class="mono mt-1 inline-block border px-1 text-[0.55rem]"
					style:color={badge.missing ? 'var(--red)' : 'var(--indigo-200)'}
					style:border-color={badge.missing ? 'var(--red)' : 'var(--line)'}
				>
					{badge.text}
				</div>
			{/if}
		</div>
	</div>

	<!-- Saga: a member's two settings, a compensation's owner, or the saga's stack. -->
	{#if data.saga?.member || data.saga?.compensates}
		<div class="mono flex items-center gap-2 border-t border-[var(--line)] px-3 py-1 text-[0.6rem]">
			{#if data.saga.compensates}
				<span class="flex items-center gap-1 text-[var(--pink)]"><Undo2 class="size-2.5" /> undoes {data.saga.compensates}</span>
			{:else}
				<span
					class="flex items-center gap-1"
					style:color={unwoundIt ? 'var(--red)' : data.saga.member === 'unwinds' ? 'var(--pink)' : 'var(--slate-500)'}
					data-tip={data.saga.member === 'unwinds' ? `If it fails, ${data.saga.sagaLabel} unwinds` : `If it fails, ${data.saga.sagaLabel} carries on`}
				>
					{#if data.saga.member === 'unwinds'}<Zap class="size-2.5" /> {unwoundIt ? 'unwound the saga' : 'unwinds'}{:else}– handled{/if}
				</span>
				{#if data.saga.undo}
					<span class="ml-auto flex min-w-0 items-center gap-1 truncate text-[var(--pink)]"><Undo2 class="size-2.5 shrink-0" /> {data.saga.undo}{data.saga.builtIn ? ' · built in' : ''}</span>
				{:else}
					<span class="ml-auto text-[var(--slate-500)]">no undo</span>
				{/if}
			{/if}
		</div>
	{/if}
	{#if isSaga && sagaState && (sagaState.registered?.length || sagaState.status)}
		<div class="border-t border-[var(--line)] px-3 py-1.5 text-[0.64rem]">
			<div class="mono mb-0.5 truncate" style:color={sagaState.status === 'failed' ? 'var(--red)' : sagaState.status ? 'var(--pink)' : 'var(--slate-300)'}>
				{sagaState.status === 'unwinding' ? 'Unwinding…' : sagaState.status === 'compensated' ? 'Unwound cleanly' : sagaState.status === 'failed' ? 'A compensation failed' : 'Registered undos'}
				{#if sagaState.trigger}· {nodeLabel(sagaState.trigger)} failed{/if}
			</div>
			<!-- One line, so the card keeps the height the ladder was laid out for; the track beside it shows each undo. -->
			{#if sagaState.registered?.length}
				{@const reg = sagaState.registered}
				<div class="mono truncate text-[var(--slate-300)]" data-tip={[...reg].reverse().map((s, i) => `${i + 1}. ${nodeLabel(s)}`).join('\n')}>
					<span class="text-[var(--pink)]">{reg.length}</span> registered · newest {nodeLabel(reg[reg.length - 1])}
				</div>
			{/if}
		</div>
	{/if}

	{#if rs && ((counts && rs.invocations) || rs.undo || rs.progress || rs.childWorkflowId)}
		<div class="flex flex-wrap items-center gap-1.5 border-t border-[var(--line)] px-3 py-1 text-[0.64rem]">
			{#if counts && rs.invocations}
				<span class="mono text-[var(--purple)]">×{rs.invocations} {data.node.type === 'onUpdate' ? 'updates handled' : 'events handled'}</span>
			{/if}
			{#if rs.progress}<span class="mono text-[var(--yellow)]">{rs.progress}</span>{/if}
			{#if rs.undo}
				<span class="status-badge flex items-center gap-1 !text-[0.55rem]" style:color={undoColor(rs.undo)}
					><Undo2 class="size-2.5" />undo {rs.undo}</span
				>
			{/if}
			{#if rs.childWorkflowId}
				<a
					href="/executions/{encodeURIComponent(rs.childWorkflowId)}"
					class="nodrag mono ml-auto flex items-center gap-0.5 text-[var(--indigo-200)]"
					onclick={(e) => e.stopPropagation()}
					data-tip={rs.childWorkflowId}>child run <ExternalLink class="size-2.5" /></a
				>
			{/if}
		</div>
	{/if}

	{#if retrying && pending}
		<div class="border-t border-[var(--line)] px-3 py-1.5 text-[0.68rem] leading-snug text-[var(--pink)]">
			<span class="mono font-semibold">Attempt {pending.attempt}{pending.maxAttempts ? ` of ${pending.maxAttempts}` : ''}</span>
			· Temporal will retry
			{#if pending.lastFailure}
				<div class="mono truncate text-[var(--slate-300)]" data-tip={pending.lastFailure}>{pending.lastFailure}</div>
			{/if}
		</div>
	{:else if rs?.status === 'waiting' && rs.waiting}
		<div class="border-t border-[var(--line)] px-3 py-1.5 text-[0.68rem] leading-snug text-[var(--indigo-100)]">
			{waitingText(rs.waiting, (data.node.config as { condition?: string }).condition)}
		</div>
	{:else if rs?.status === 'failed' && rs.error}
		<div class="mono truncate border-t border-[var(--line)] px-3 py-1.5 text-[0.66rem] text-[var(--red)]" data-tip={rs.error}>
			{rs.error}
		</div>
	{:else if problems.length}
		<div class="border-t border-[var(--line)] px-3 py-1.5 text-[0.68rem] leading-snug text-[var(--red)]">{problems[0].message}</div>
	{/if}

	<div
		class="mono flex items-center justify-between gap-1 overflow-hidden border-t border-[var(--line)] px-3 py-1 text-[0.6rem] whitespace-nowrap text-[var(--slate-500)]"
	>
		<span class="min-w-0 truncate">steps.{data.node.key}</span>
		<span class="ml-2 max-w-[55%] shrink-0 truncate">{spec.primitive}</span>
	</div>

	<!-- Main outcome: continues the line. End run has none. A Saga's outcomes
	     leave past its track, perpendicular to the flow. -->
	{#if main}
		<Handle
			type="source"
			position={isSaga ? (lr ? Position.Bottom : Position.Right) : lr ? Position.Right : Position.Bottom}
			id={main}
			style={isSaga ? (lr ? 'left: 35%' : 'top: 35%') : undefined}
			class="!h-3 !w-3 !rounded-none !border-2 {rs?.handle === main ? '!border-[var(--green)] !bg-[var(--green)]' : ''}"
		/>
	{/if}
	{#if handles.length > 1 && showLabel(main)}
		<span
			class="edge-handle-label pointer-events-none absolute z-10 bg-[var(--canvas)] px-0.5 leading-[1.35] whitespace-nowrap text-[var(--slate-300)]"
			style={isSaga
				? lr
					? 'left: calc(35% + 9px); top: calc(100% + 2px)'
					: 'left: calc(100% + 2px); top: calc(35% - 15px)'
				: lr
					? 'left: calc(100% + 2px); top: calc(50% - 15px)'
					: 'left: calc(50% + 9px); top: calc(100% + 2px)'}
			style:color={rs?.handle === main ? 'var(--green)' : undefined}>{main}</span
		>
	{/if}

	<!-- Saga handles, on the side that faces a saga's track: ◆ links a step
	     to a Saga, ↺ links a member to what undoes it. Shown on hover. -->
	{#if isSaga}
		<Handle
			type="target"
			id="members"
			position={lr ? Position.Left : Position.Top}
			class="!h-3 !w-3 !rotate-45 !rounded-none !border-2 !border-[var(--pink)] !bg-[#15171f]"
		/>
	{:else if !entry && !data.saga?.compensates}
		<Handle
			type="source"
			id="saga"
			position={lr ? Position.Bottom : Position.Right}
			style={lr ? 'left: 9%' : 'top: 12%'}
			class="!h-2.5 !w-2.5 !rotate-45 !rounded-none !border-2 !border-[var(--pink)] !bg-[#15171f] opacity-0 transition-opacity group-hover:opacity-100 {selected ? 'opacity-100' : ''}"
			data-tip="Drag to a Saga to make this step a member"
		/>
		{#if data.saga?.member}
			<Handle
				type="source"
				id="compensate"
				position={lr ? Position.Bottom : Position.Right}
				style={lr ? 'left: 21%' : 'top: 26%'}
				class="!h-2.5 !w-2.5 !rounded-full !border-2 !border-[var(--pink)] !bg-[#15171f] {data.saga.undo ? '' : 'opacity-0 transition-opacity group-hover:opacity-100'}"
				data-tip="Drag to the step that undoes this one"
			/>
		{/if}
	{/if}

	<!-- Secondary outcomes: branch off perpendicular to the main line. -->
	{#each secondary as h (h)}
		{@const at = isSaga ? 70 : secondaryPos(h)}
		{@const far = farSide(h)}
		<Handle
			type="source"
			position={far ? (lr ? Position.Top : Position.Left) : lr ? Position.Bottom : Position.Right}
			id={h}
			style={lr ? `left: ${at}%` : `top: ${at}%`}
			class="!h-3 !w-3 !rounded-none !border-2 !border-[color-mix(in_srgb,var(--pink)_70%,#465a78)] {rs?.handle === h
				? '!border-[var(--pink)] !bg-[var(--pink)]'
				: ''}"
		/>
		{#if showLabel(h)}
			<span
				class="edge-handle-label pointer-events-none absolute z-10 bg-[var(--canvas)] px-0.5 leading-[1.35] whitespace-nowrap"
				style={far
					? lr
						? `left: calc(${at}% + 9px); bottom: calc(100% + 2px)`
						: `right: calc(100% + 2px); top: calc(${at}% - 15px)`
					: lr
						? `left: calc(${at}% + 9px); top: calc(100% + 2px)`
						: `left: calc(100% + 2px); top: calc(${at}% - 15px)`}
				style:color={rs?.handle === h ? 'var(--pink)' : 'color-mix(in srgb, var(--pink) 60%, var(--slate-300))'}>{h}</span
			>
		{/if}
	{/each}
</div>
