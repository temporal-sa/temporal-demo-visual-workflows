<script lang="ts">
	import Play from '@lucide/svelte/icons/play';
	import Save from '@lucide/svelte/icons/save';
	import Hammer from '@lucide/svelte/icons/hammer';
	import History from '@lucide/svelte/icons/history';
	import Maximize2 from '@lucide/svelte/icons/maximize-2';
	import Minimize2 from '@lucide/svelte/icons/minimize-2';
	import Settings2 from '@lucide/svelte/icons/settings-2';
	import Wand from '@lucide/svelte/icons/wand-sparkles';
	import Sparkles from '@lucide/svelte/icons/sparkles';
	import ArrowRightFromLine from '@lucide/svelte/icons/arrow-right-from-line';
	import ArrowDownFromLine from '@lucide/svelte/icons/arrow-down-from-line';
	import Share2 from '@lucide/svelte/icons/share-2';
	import GalleryVerticalEnd from '@lucide/svelte/icons/gallery-vertical-end';
	import Link from '@lucide/svelte/icons/link';
	import Download from '@lucide/svelte/icons/download';
	import ChevronLeft from '@lucide/svelte/icons/chevron-left';
	import Clock from '@lucide/svelte/icons/clock';
	import { toast } from 'svelte-sonner';
	import * as Dialog from '$lib/components/ui/dialog';
	import * as Tabs from '$lib/components/ui/tabs';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu';
	import { Button } from '$lib/components/ui/button';
	import { Switch } from '$lib/components/ui/switch';
	import { Textarea } from '$lib/components/ui/textarea';
	import Choice from './Choice.svelte';
	import { api } from '$lib/api';
	import { app, errorMessage, type Mode } from '$lib/state.svelte';
	import { confirmAction } from '$lib/confirm.svelte';
	import { downloadFlow } from '$lib/flowio';
	import type { OnSignalConfig, OnUpdateConfig, WithStart } from '$lib/types';

	let runOpen = $state(false);
	let runInput = $state('{}');
	let runInputError = $state('');
	// A keyed flow can also be started by a message: its input goes to the
	// key's run as that Signal or Update, starting the run if needed.
	let sendAs = $state('start');
	const sendOptions = $derived.by(() => {
		const f = app.flow;
		if (!f?.settings.keyedBy) return [];
		const opts = [{ value: 'start', label: 'Start the flow' }];
		for (const n of f.nodes) {
			if (n.type === 'onSignal') {
				const name = (n.config as OnSignalConfig).signalName;
				opts.push({ value: `signal:${name}`, label: `Signal-With-Start: ${name}` });
			} else if (n.type === 'onUpdate') {
				const name = (n.config as OnUpdateConfig).updateName;
				opts.push({ value: `update:${name}`, label: `Update-With-Start: ${name}` });
			}
		}
		return opts.length > 1 ? opts : [];
	});

	function openRunDialog() {
		runInput = app.sampleInput();
		try {
			runInput = JSON.stringify(JSON.parse(runInput), null, 2);
		} catch {
			/* leave as typed */
		}
		runInputError = '';
		if (!sendOptions.some((o) => o.value === sendAs)) sendAs = 'start';
		runOpen = true;
	}

	async function start() {
		let input: unknown;
		try {
			input = runInput.trim() ? JSON.parse(runInput) : undefined;
		} catch (err) {
			runInputError = `Input is not valid JSON: ${(err as Error).message}`;
			return;
		}
		runOpen = false;
		const [kind, name] = sendAs.split(':');
		await app.startRun(input, kind === 'start' ? undefined : ({ kind, name } as WithStart));
	}

	async function share(listed: boolean) {
		const flow = app.flow;
		if (!flow) return;
		if (!(await app.whenCreated())) return;
		if (app.dirty && !(await app.save())) return;
		try {
			const res = await api.publish(flow.id, listed);
			if (listed) {
				toast.success('Published to the template gallery', { description: 'Anyone with access can use a copy.' });
			} else {
				const url = new URL(res.shareUrl, location.origin).toString();
				await navigator.clipboard?.writeText(url);
				toast.success('Share link copied', { description: url });
			}
		} catch (err) {
			toast.error(`Could not share: ${errorMessage(err)}`);
		}
	}

	async function resetExample() {
		const ok = await confirmAction({
			title: 'Reset to the built-in example?',
			description: 'Your saved changes to this example are dropped and you see it as it ships. Its runs, webhook and schedule stay.',
			confirmLabel: 'Reset',
			destructive: true
		});
		if (ok) await app.resetToExample();
	}

	const focused = $derived(app.leftCollapsed && app.rightCollapsed);
	const lr = $derived(app.direction === 'LR');
</script>

<header class="flex h-12 shrink-0 items-center gap-2 border-b border-[var(--line)] bg-[#111218]/90 px-3 backdrop-blur">
	<a href="/workflows" class="grid size-7 place-items-center text-[var(--slate-300)] hover:text-[var(--off-white)]" data-tip="All workflows" aria-label="All workflows"><ChevronLeft class="size-4" /></a>
	{#if app.flow}
		<div class="flex min-w-0 items-center gap-2">
			<input
				class="min-w-0 border border-transparent bg-transparent px-1.5 py-0.5 text-[0.95rem] font-semibold text-[var(--off-white)] outline-none hover:border-[var(--line)] focus:border-[var(--uv-light)] disabled:opacity-80"
				style:width="{Math.min(36, Math.max(10, app.flow.name.length + 2))}ch"
				bind:value={app.flow.name}
				disabled={app.mode === 'runs'}
				aria-label="Flow name"
			/>
			{#if app.flow.version}<span class="mono text-[0.66rem] text-[var(--slate-300)]">v{app.flow.version}</span>{/if}
			{#if app.customized}
				<button class="status-badge text-[var(--uv-light)] hover:underline" data-tip="Your copy of a built-in example. Click to reset it to the example." onclick={resetExample}>example · edited</button>
			{:else if app.builtIn}
				<span class="status-badge text-[var(--uv-light)]" data-tip="A built-in example, always as it ships. Saving a change makes it your own copy.">example</span>
			{/if}
			{#if app.flow.settings.entity}<span class="status-badge text-[var(--cyan)]" data-tip={app.flow.settings.keyedBy ? "One run per key, for the life of the entity" : "Each run lives until an End run step"}>entity</span>{/if}{#if app.flow.settings.keyedBy}<span class="status-badge text-[var(--cyan)]" data-tip="One run per key">keyed</span>{/if}
			{#if app.dirty}<span class="status-badge text-[var(--yellow)]">unsaved</span>{/if}
		</div>

		<Tabs.Root value={app.mode} onValueChange={(v) => app.setMode(v as Mode)} class="ml-1">
			<Tabs.List class="h-8">
				<Tabs.Trigger value="build" class="mono gap-1.5 text-xs"><Hammer class="size-3.5" />Build</Tabs.Trigger>
				<Tabs.Trigger value="runs" class="mono gap-1.5 text-xs"><History class="size-3.5" />Runs</Tabs.Trigger>
			</Tabs.List>
		</Tabs.Root>

		{#if app.mode === 'build'}
			<div class="ml-1 flex items-center gap-0.5 border-l border-[var(--line)] pl-2">
				<Button
					variant="ghost"
					size="icon-sm"
					data-tip={lr ? 'Left to right — switch to top-down' : 'Top-down — switch to left to right'}
					aria-label={lr ? 'Switch to top-down layout' : 'Switch to left-to-right layout'}
					onclick={() => app.setDirection(lr ? 'TB' : 'LR')}
				>
					{#if lr}<ArrowRightFromLine />{:else}<ArrowDownFromLine />{/if}
				</Button>
				<Button variant={app.copilotOpen ? 'secondary' : 'ghost'} size="sm" class="mono text-xs" data-tip="Build and change this flow with Claude" onclick={() => (app.copilotOpen = !app.copilotOpen)}><Sparkles /> Copilot</Button>
				<Button variant="ghost" size="sm" class="mono text-xs" data-tip="Lay the flow out automatically" onclick={() => app.requestTidy()}><Wand /> Tidy up</Button>
				<label class="mono flex items-center gap-1 pr-1 text-[0.62rem] text-[var(--slate-300)]" data-tip="Tidy up after each new connection">
					<Switch checked={app.autoTidy} onCheckedChange={(v) => app.setAutoTidy(v)} class="scale-75" aria-label="Tidy up after each new connection" />
					auto
				</label>
				<Button variant="ghost" size="icon-sm" data-tip="Flow settings: variables, defaults, keyed runs" aria-label="Flow settings" onclick={() => (app.settingsOpen = true)}><Settings2 /></Button>
				<Button variant="ghost" size="icon-sm" data-tip="Version history" aria-label="Version history" onclick={() => (app.historyOpen = true)}><Clock /></Button>
				<DropdownMenu.Root>
					<DropdownMenu.Trigger>
						{#snippet child({ props })}
							<Button {...props} variant="ghost" size="icon-sm" data-tip="Share" aria-label="Share"><Share2 /></Button>
						{/snippet}
					</DropdownMenu.Trigger>
					<DropdownMenu.Content align="start" class="w-60">
						<DropdownMenu.Item onclick={() => share(true)}><GalleryVerticalEnd /> Publish to gallery</DropdownMenu.Item>
						<DropdownMenu.Item onclick={() => share(false)}><Link /> Copy share link</DropdownMenu.Item>
						<DropdownMenu.Separator />
						<DropdownMenu.Item onclick={() => app.flow && downloadFlow(app.flow)}><Download /> Export JSON</DropdownMenu.Item>
					</DropdownMenu.Content>
				</DropdownMenu.Root>
			</div>
		{/if}
	{/if}

	<div class="ml-auto flex items-center gap-2">
		{#if app.flow}
			<Button variant="ghost" size="sm" class="mono text-xs" onclick={() => app.toggleFocus()} data-tip="Collapse side panels ( [ and ] toggle each )">
				{#if focused}<Minimize2 /> Panels{:else}<Maximize2 /> Focus{/if}
			</Button>
		{/if}
		{#if app.flow && app.mode === 'build'}
			<Button variant="outline" size="sm" class="mono" onclick={() => app.save()} disabled={app.saving || !app.dirty}><Save /> Save</Button>
			<Button size="sm" class="btn-green mono" onclick={openRunDialog}><Play /> Run</Button>
		{/if}
	</div>
</header>

<Dialog.Root bind:open={runOpen}>
	<Dialog.Content class="sm:max-w-lg">
		<Dialog.Header>
			<Dialog.Title>Start a run</Dialog.Title>
			<Dialog.Description>
				The run gets a snapshot of <strong>{app.flow?.name}</strong>{app.dirty ? ' (saved first)' : ''}. The input is available to every step as
				<code class="mono text-[var(--green)]">{'{{ input }}'}</code>.
				{#if app.flow?.settings.keyedBy}
					This flow is keyed by <code class="mono">{app.flow.settings.keyedBy}</code>: if a run for that key exists, the input goes to it instead.
				{/if}
			</Dialog.Description>
		</Dialog.Header>
		{#if sendOptions.length}
			<div class="space-y-1">
				<Choice bind:value={sendAs} options={sendOptions} />
				{#if sendAs !== 'start'}
					<p class="text-[0.68rem] leading-snug text-[var(--slate-300)]">
						The input is sent as {sendAs.split(':')[1]} to the run for its key, starting that run first if there isn't one (subject to the flow's Workflow ID
						policies). An On start lane, if the flow has one, runs before the message is handled.
					</p>
				{/if}
			</div>
		{/if}
		<Textarea class="mono min-h-48 text-xs" bind:value={runInput} spellcheck={false} />
		{#if runInputError}<p class="text-sm text-[var(--red)]">{runInputError}</p>{/if}
		<Dialog.Footer>
			<Button variant="ghost" onclick={() => (runOpen = false)}>Cancel</Button>
			<Button class="btn-green" onclick={start}><Play /> {sendAs === 'start' ? 'Start workflow' : 'Send'}</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
