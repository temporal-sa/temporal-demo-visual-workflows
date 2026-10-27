<script lang="ts">
	import Trash2 from '@lucide/svelte/icons/trash-2';
	import Settings2 from '@lucide/svelte/icons/settings-2';
	import { ScrollArea } from '$lib/components/ui/scroll-area';
	import { Button } from '$lib/components/ui/button';
	import NodeForm from './NodeForm.svelte';
	import { app } from '$lib/state.svelte';
	import { flowPaths } from '$lib/layout';
	import { removeSteps } from '$lib/edit';

	const node = $derived(app.flow?.nodes.find((n) => n.id === app.selectedNodeId) ?? null);
	const flowProblems = $derived(app.problems.filter((p) => !p.nodeId));
	const lanes = $derived(app.flow ? flowPaths(app.flow).filter((p) => p.kind !== 'main') : []);

	function removeNode() {
		const flow = app.flow;
		if (!flow || !node || node.type === 'trigger') return;
		removeSteps(flow, new Set([node.id]));
		app.selectedNodeId = null;
		app.structure++;
	}
</script>

<ScrollArea class="h-full">
	<div class="space-y-5 p-4">
		{#if node}
			{#key node.id}
				<NodeForm {node} />
			{/key}
			{#if node.type !== 'trigger'}
				<Button variant="destructive" size="sm" class="mono w-full" onclick={removeNode}><Trash2 /> Remove step</Button>
			{/if}
		{:else if app.flow}
			<div>
				<div class="eyebrow">Flow</div>
				<div class="mt-1 text-lg font-semibold">{app.flow.name}</div>
				{#if app.flow.description}<p class="mt-1 text-xs text-[var(--slate-300)]">{app.flow.description}</p>{/if}
				<Button variant="outline" size="sm" class="mono mt-3 w-full" onclick={() => (app.settingsOpen = true)}><Settings2 /> Flow settings</Button>
			</div>

			{#if app.problems.length}
				<div class="space-y-1 border border-[var(--red)]/60 bg-[var(--red)]/10 p-2.5 text-xs text-[var(--red)]">
					<div class="eyebrow !text-[var(--red)]">{app.problems.length} problem{app.problems.length === 1 ? '' : 's'}</div>
					{#each flowProblems as p, i (i)}<div>{p.message}</div>{/each}
					{#if app.problems.length > flowProblems.length}
						<div class="text-[var(--slate-300)]">Steps with problems are outlined on the canvas.</div>
					{/if}
				</div>
			{/if}

			<div class="grid grid-cols-3 gap-2 border border-[var(--line)] p-2.5 text-xs">
				<div><div class="eyebrow !text-[0.58rem]">Steps</div><div class="mono mt-0.5">{app.flow.nodes.length}</div></div>
				<div><div class="eyebrow !text-[0.58rem]">Lanes</div><div class="mono mt-0.5">{lanes.length}</div></div>
				<div><div class="eyebrow !text-[0.58rem]">Variables</div><div class="mono mt-0.5">{app.flow.settings.variables?.length ?? 0}</div></div>
			</div>
			{#if lanes.length}
				<ul class="space-y-1 text-xs">
					{#each lanes as l (l.entryId)}<li class="mono text-[var(--purple)]">{l.label} · {l.nodeIds.length} steps</li>{/each}
				</ul>
			{/if}

			<div class="space-y-2 border border-[var(--line)] p-3">
				<div class="eyebrow !text-[0.62rem]">Expressions</div>
				<p class="text-xs leading-relaxed text-[var(--slate-300)]">
					Any text field can include <span class="mono text-[var(--green)]">{'{{ … }}'}</span>. They are evaluated deterministically inside the Workflow, so
					replays always agree.
				</p>
				<ul class="mono space-y-1 text-[0.68rem] text-[var(--indigo-100)]">
					<li>{'{{ input.customer_id }}'}</li>
					<li>{'{{ steps.get_order.body.status }}'}</li>
					<li>{'{{ vars.attempts + 1 }}'}</li>
					<li>events.order_status.status == "shipped"</li>
					<li>{'{{ run.id }}'} · {'{{ run.callbackUrl }}/events/<name>'}</li>
				</ul>
			</div>

			<div class="space-y-1.5 text-xs text-[var(--slate-300)]">
				<div class="eyebrow !text-[0.62rem]">Canvas</div>
				<p>Drag from a step's handle to connect. The main outcome continues the line; errors and timeouts branch to the side.</p>
				<p>On signal and On update start event lanes: they run alongside the main path and share its variables.</p>
				<p>On start is the init lane: it runs once, first. The main path and the event lanes wait for it, including a message sent with Signal- or Update-With-Start.</p>
				<p>Select a step or edge and press <span class="mono">Delete</span> to remove it. A run executes a snapshot of the saved flow.</p>
			</div>
		{/if}
	</div>
</ScrollArea>
