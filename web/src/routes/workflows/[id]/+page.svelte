<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { beforeNavigate, goto } from '$app/navigation';
	import { SvelteFlowProvider } from '@xyflow/svelte';
	import ChevronLeft from '@lucide/svelte/icons/chevron-left';
	import ChevronRight from '@lucide/svelte/icons/chevron-right';
	import EditorTopBar from '$lib/components/EditorTopBar.svelte';
	import EditorSidebar from '$lib/components/EditorSidebar.svelte';
	import BuildCanvas from '$lib/components/BuildCanvas.svelte';
	import BuildInspector from '$lib/components/BuildInspector.svelte';
	import RunScreen from '$lib/components/RunScreen.svelte';
	import RunInspector from '$lib/components/RunInspector.svelte';
	import FlowSettingsSheet from '$lib/components/FlowSettingsSheet.svelte';
	import VersionHistorySheet from '$lib/components/VersionHistorySheet.svelte';
	import CopilotPanel from '$lib/components/CopilotPanel.svelte';
	import { copilot } from '$lib/copilot/session.svelte';
	import { app } from '$lib/state.svelte';
	import { confirmAction } from '$lib/confirm.svelte';

	const flowId = $derived(page.params.id ?? '');

	// Leaving the flow with unsaved changes asks first, in a themed dialog.
	// Closing or reloading the tab gets the browser's own prompt instead
	// (beforeunload below): browsers don't allow a page to replace it.
	let leaving = false;
	beforeNavigate((nav) => {
		if (!app.dirty || leaving || nav.willUnload || !nav.to || nav.to.url.pathname === page.url.pathname) return;
		nav.cancel();
		const to = nav.to.url;
		confirmAction({
			title: 'Leave without saving?',
			description: `Your changes to "${app.flow?.name ?? 'this flow'}" haven't been saved.`,
			confirmLabel: 'Leave',
			cancelLabel: 'Keep editing',
			destructive: true
		}).then((ok) => {
			if (!ok) return;
			leaving = true;
			goto(to).finally(() => (leaving = false));
		});
	});

	// Open the flow named in the URL, then honour ?mode=runs&run=<workflowId>.
	$effect(() => {
		const id = flowId;
		const params = new URLSearchParams(page.url.search);
		app.openFlow(id).then(async () => {
			const run = params.get('run');
			if (params.get('mode') === 'runs' || run) {
				app.mode = 'runs';
				if (run) await app.openRun(run);
				else await app.loadRuns(true);
			} else {
				app.mode = 'build';
			}
		});
	});

	// Keep the run list fresh while on the Runs tab.
	$effect(() => {
		if (app.mode !== 'runs' || !app.flow) return;
		const t = setInterval(() => app.loadRuns(false), 3000);
		return () => clearInterval(t);
	});

	// Selecting a step always shows the inspector, since that is where its actions live.
	const inspectorOpen = $derived(!app.rightCollapsed || app.selectedNodeId !== null);

	// Follow the copilot while this flow is open, so a tool call it made
	// before a reload is carried out even with the panel closed.
	$effect(() => {
		if (app.flow?.id === flowId) copilot.attach(flowId);
		return () => copilot.detach();
	});

	onMount(() => {
		const onKey = (e: KeyboardEvent) => {
			const el = e.target as HTMLElement | null;
			const typing = !!el && (el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName));
			if (!typing && !e.metaKey && !e.ctrlKey && !e.altKey) {
				if (e.key === '[') app.leftCollapsed = !app.leftCollapsed;
				if (e.key === ']') app.rightCollapsed = !app.rightCollapsed;
			}
			if ((e.metaKey || e.ctrlKey) && e.key === 's') {
				e.preventDefault();
				if (app.mode === 'build' && app.dirty) app.save();
			}
		};
		const onUnload = (e: BeforeUnloadEvent) => {
			if (app.dirty) e.preventDefault();
		};
		window.addEventListener('keydown', onKey);
		window.addEventListener('beforeunload', onUnload);
		return () => {
			window.removeEventListener('keydown', onKey);
			window.removeEventListener('beforeunload', onUnload);
		};
	});
</script>

<svelte:head><title>{app.flow?.name ?? 'Workflow'} · Visual Workflows</title></svelte:head>

<div class="flex h-full flex-col">
	<EditorTopBar />
	<div class="flex min-h-0 flex-1">
		<EditorSidebar />
		<main class="relative min-w-0 flex-1 bg-[var(--canvas)]">
			{#if app.flow && app.flow.id === flowId}
				{#if app.mode === 'build'}
					<SvelteFlowProvider><BuildCanvas /></SvelteFlowProvider>
				{:else}
					<RunScreen />
				{/if}
			{:else}
				<div class="grid h-full place-items-center text-sm text-[var(--slate-300)]">Loading flow…</div>
			{/if}
		</main>
		{#if app.flow}
			<aside class="relative shrink-0 border-l border-[var(--line)] bg-[#111218]/85 {inspectorOpen ? 'w-[320px]' : 'w-3'}">
				<button
					class="absolute top-1/2 -left-3 z-20 grid h-10 w-3 -translate-y-1/2 place-items-center border border-[var(--line)] bg-[#1b1d29] text-[var(--slate-300)] hover:border-[var(--uv-light)] hover:text-[var(--off-white)]"
					onclick={() => {
						if (inspectorOpen) {
							app.rightCollapsed = true;
							app.selectedNodeId = null;
						} else app.rightCollapsed = false;
					}}
					data-tip={inspectorOpen ? 'Hide inspector ( ] )' : 'Show inspector ( ] )'}
					aria-label={inspectorOpen ? 'Hide inspector' : 'Show inspector'}
				>
					{#if inspectorOpen}<ChevronRight class="size-3" />{:else}<ChevronLeft class="size-3" />{/if}
				</button>
				{#if inspectorOpen}
					{#if app.mode === 'build'}<BuildInspector />{:else}<RunInspector />{/if}
				{/if}
			</aside>
			{#if app.copilotOpen && app.mode === 'build'}
				<aside class="w-[360px] shrink-0 border-l border-[var(--line)] bg-[#111218]/90"><CopilotPanel /></aside>
			{/if}
		{/if}
	</div>
</div>
<FlowSettingsSheet />
<VersionHistorySheet />
