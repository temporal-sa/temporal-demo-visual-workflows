<script lang="ts">
	import { tick } from 'svelte';
	import Sparkles from '@lucide/svelte/icons/sparkles';
	import Send from '@lucide/svelte/icons/send';
	import Square from '@lucide/svelte/icons/square';
	import Undo2 from '@lucide/svelte/icons/undo-2';
	import RotateCcw from '@lucide/svelte/icons/rotate-ccw';
	import X from '@lucide/svelte/icons/x';
	import Check from '@lucide/svelte/icons/check';
	import LoaderCircle from '@lucide/svelte/icons/loader-circle';
	import TriangleAlert from '@lucide/svelte/icons/triangle-alert';
	import BookOpen from '@lucide/svelte/icons/book-open';
	import { Button } from '$lib/components/ui/button';
	import { Textarea } from '$lib/components/ui/textarea';
	import { app } from '$lib/state.svelte';
	import { confirmAction } from '$lib/confirm.svelte';
	import { copilot } from '$lib/copilot/session.svelte';
	import type { CopilotEntry } from '$lib/types';

	// The copilot chat: you describe the flow, Claude edits the canvas. Its
	// edits are unsaved changes; Save keeps them, Undo this turn drops them.

	let draft = $state('');
	let list = $state<HTMLElement>();

	const entries = $derived(copilot.state.entries);
	const status = $derived(
		copilot.sending
			? 'Sending…'
			: copilot.state.status === 'editing'
				? 'Editing the canvas…'
				: copilot.state.status === 'thinking'
					? 'Thinking…'
					: ''
	);
	// Reference tools read the catalogs; they show as a quieter row.
	const reads = new Set(['list_step_types', 'describe_step_type', 'list_connectors', 'describe_operation', 'get_flow']);

	$effect(() => {
		if (app.flow) copilot.attach(app.flow.id);
	});
	// Keep the latest entry in view.
	$effect(() => {
		const _n = entries.length;
		tick().then(() => list?.scrollTo({ top: list.scrollHeight, behavior: 'smooth' }));
	});

	async function send() {
		const text = draft;
		if (!text.trim() || copilot.sending) return;
		draft = '';
		if (!(await copilot.send(text))) draft = text;
	}

	function onkey(e: KeyboardEvent) {
		if (e.key === 'Enter' && !e.shiftKey) {
			e.preventDefault();
			send();
		}
	}

	async function newChat() {
		const ok = await confirmAction({
			title: 'Start a new conversation?',
			description: 'The copilot forgets this one. Changes it made to the canvas stay.',
			confirmLabel: 'New conversation'
		});
		if (ok) await copilot.reset();
	}

	async function undo(e: CopilotEntry) {
		const ok = await confirmAction({
			title: 'Undo this turn?',
			description: 'The canvas goes back to how it was when you sent this message. Edits since then, yours included, are dropped.',
			confirmLabel: 'Undo',
			destructive: true
		});
		if (ok && e.messageId) copilot.undo(e.messageId);
	}

	function toolText(e: CopilotEntry) {
		if (e.text) return e.text;
		const input = (e.input ?? {}) as Record<string, string>;
		const what = input.type ?? input.step ?? input.connector ?? '';
		return `${e.tool?.replaceAll('_', ' ')}${what ? ` · ${what}` : ''}`;
	}

	const examples = [
		'Charge a card with Stripe, then post the receipt to Slack. If the charge fails, alert #ops.',
		'Add an approval before the refund for amounts over 500.',
		'What does this flow do?'
	];
</script>

<div class="flex h-full flex-col">
	<div class="flex items-center gap-2 border-b border-[var(--line)] px-3 py-2">
		<Sparkles class="size-4 text-[var(--uv-light)]" />
		<span class="eyebrow">Copilot</span>
		<span class="mono text-[0.6rem] text-[var(--slate-500)]">Claude · a Temporal Workflow</span>
		<div class="ml-auto flex items-center gap-1">
			<Button variant="ghost" size="icon-sm" data-tip="New conversation" aria-label="New conversation" onclick={newChat} disabled={!entries.length}><RotateCcw /></Button>
			<Button variant="ghost" size="icon-sm" data-tip="Close" aria-label="Close copilot" onclick={() => (app.copilotOpen = false)}><X /></Button>
		</div>
	</div>

	<div bind:this={list} class="min-h-0 flex-1 space-y-2.5 overflow-y-auto px-3 py-3">
		{#each entries as e (e.seq)}
			{#if e.kind === 'user'}
				<div class="group ml-6 border border-[var(--line)] bg-[var(--uv)]/15 px-2.5 py-1.5 text-[0.8rem] whitespace-pre-wrap">
					{e.text}
					{#if copilot.canUndo(e.messageId)}
						<button class="mono mt-1 flex items-center gap-1 text-[0.6rem] text-[var(--slate-300)] opacity-0 group-hover:opacity-100 hover:text-[var(--off-white)]" onclick={() => undo(e)}>
							<Undo2 class="size-3" /> Undo this turn
						</button>
					{/if}
				</div>
			{:else if e.kind === 'assistant'}
				<div class="text-[0.8rem] leading-snug whitespace-pre-wrap">{e.text}</div>
			{:else if e.kind === 'tool'}
				<div class="mono flex items-start gap-1.5 text-[0.66rem] {reads.has(e.tool ?? '') ? 'text-[var(--slate-500)]' : 'text-[var(--slate-300)]'}">
					{#if e.status === 'running'}
						<LoaderCircle class="mt-0.5 size-3 shrink-0 animate-spin text-[var(--uv-light)]" />
					{:else if e.status === 'failed'}
						<TriangleAlert class="mt-0.5 size-3 shrink-0 text-[var(--red)]" />
					{:else if reads.has(e.tool ?? '')}
						<BookOpen class="mt-0.5 size-3 shrink-0" />
					{:else}
						<Check class="mt-0.5 size-3 shrink-0 text-[var(--green)]" />
					{/if}
					<span class="min-w-0 break-words" style:color={e.status === 'failed' ? 'var(--red)' : undefined}>{toolText(e)}</span>
				</div>
			{:else}
				<div class="flex items-start gap-1.5 text-[0.72rem] text-[var(--yellow)]"><TriangleAlert class="mt-0.5 size-3 shrink-0" />{e.text}</div>
			{/if}
		{:else}
			<div class="space-y-3 pt-2">
				<p class="text-[0.8rem] text-[var(--slate-300)]">
					Describe what you want and the copilot builds it on the canvas. Its changes are unsaved until you press Save.
				</p>
				<div class="space-y-1.5">
					{#each examples as ex (ex)}
						<button class="w-full border border-[var(--line)] px-2.5 py-1.5 text-left text-[0.74rem] text-[var(--slate-300)] hover:border-[var(--uv-light)] hover:text-[var(--off-white)]" onclick={() => (draft = ex)}>{ex}</button>
					{/each}
				</div>
			</div>
		{/each}
		{#if status}
			<div class="mono flex items-center gap-1.5 text-[0.66rem] text-[var(--uv-light)]"><LoaderCircle class="size-3 animate-spin" />{status}</div>
		{/if}
	</div>

	<div class="space-y-1.5 border-t border-[var(--line)] p-3">
		{#if copilot.error}<p class="text-[0.7rem] text-[var(--red)]">{copilot.error}</p>{/if}
		<Textarea
			class="max-h-40 min-h-16 text-[0.8rem]"
			bind:value={draft}
			onkeydown={onkey}
			placeholder={copilot.busy ? 'Queue another message…' : 'Ask the copilot to build or change this flow'}
		/>
		<div class="flex items-center gap-2">
			<span class="mono text-[0.58rem] text-[var(--slate-500)]">Enter to send · Shift+Enter for a new line</span>
			{#if copilot.busy}
				<Button variant="outline" size="sm" class="ml-auto" onclick={() => copilot.stop()}><Square /> Stop</Button>
			{/if}
			<Button size="sm" class="btn-uv {copilot.busy ? '' : 'ml-auto'}" disabled={!draft.trim() || copilot.sending} onclick={send}><Send /> Send</Button>
		</div>
	</div>
</div>
