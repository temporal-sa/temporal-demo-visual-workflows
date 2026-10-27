import { api, ApiError } from '$lib/api';
import { app, errorMessage } from '$lib/state.svelte';
import type { CopilotState } from '$lib/types';
import { compactFlow, runTool } from './tools';

// The copilot conversation for the open flow, as this tab sees it. It polls
// the copilot Workflow's Query, carries out the tool calls meant for this tab
// (in order, once each), and returns their results by Update. A tab's ID
// lives in sessionStorage, so it survives a reload: a call made before the
// reload is answered after it.

const empty = (): CopilotState => ({ status: 'idle', entries: [], pending: [] });

function tabId(): string {
	let id = sessionStorage.getItem('vflow:copilot-tab');
	if (!id) {
		id = crypto.randomUUID();
		sessionStorage.setItem('vflow:copilot-tab', id);
	}
	return id;
}

class CopilotSession {
	flowId = $state('');
	state = $state<CopilotState>(empty());
	sending = $state(false);
	error = $state('');
	readonly clientId = typeof sessionStorage === 'undefined' ? '' : tabId();
	private handled = new Set<string>();
	private timer: ReturnType<typeof setTimeout> | undefined;
	private running = false;
	private attached = false;

	busy = $derived(this.state.status !== 'idle' || (this.state.queued ?? 0) > 0 || this.sending);

	/** Follow the copilot for a flow: while its panel is open, or while it is working. */
	attach(flowId: string) {
		if (this.flowId !== flowId) {
			this.flowId = flowId;
			this.state = empty();
			this.handled.clear();
		}
		this.attached = true;
		this.poll();
	}

	/** Stop polling (the editor closed). Calls already carried out stay remembered. */
	detach() {
		this.attached = false;
		clearTimeout(this.timer);
		this.timer = undefined;
	}

	private schedule() {
		clearTimeout(this.timer);
		if (!this.flowId || !this.attached) return;
		// Quick while it works; slow while idle with the panel open; not at
		// all otherwise (polling an open Workflow's Query needs a worker).
		const delay = this.busy || this.state.pending.length ? 700 : app.copilotOpen ? 5000 : 0;
		if (delay) this.timer = setTimeout(() => this.poll(), delay);
	}

	async poll() {
		const flowId = this.flowId;
		if (!flowId || this.running) return;
		this.running = true;
		try {
			const st = await api.copilot(flowId);
			if (flowId !== this.flowId) return;
			this.state = st;
			await this.carryOut();
		} catch (err) {
			this.error = errorMessage(err);
		} finally {
			this.running = false;
			this.schedule();
		}
	}

	/** Carries out this tab's pending tool calls, in order. */
	private async carryOut() {
		for (const call of this.state.pending) {
			if (call.clientId !== this.clientId || this.handled.has(call.id)) continue;
			this.handled.add(call.id);
			let result: unknown, error: string | undefined;
			try {
				result = await runTool(call.name, call.input);
			} catch (err) {
				error = err instanceof Error ? err.message : String(err);
			}
			try {
				await api.copilotResult(this.flowId, error === undefined ? { callId: call.id, result } : { callId: call.id, error });
			} catch (err) {
				// Already answered (another poll got there first) is fine.
				if (!(err instanceof ApiError && err.status === 409)) throw err;
			}
		}
	}

	async send(text: string) {
		const flow = app.flow;
		if (!flow || !text.trim()) return false;
		const id = crypto.randomUUID();
		// The canvas before this turn, for Undo this turn.
		sessionStorage.setItem(`vflow:copilot-undo:${id}`, JSON.stringify($state.snapshot(flow)));
		this.sending = true;
		this.error = '';
		try {
			await api.copilotSend(flow.id, { id, text: text.trim(), flow: compactFlow($state.snapshot(flow) as typeof flow), clientId: this.clientId });
			return true;
		} catch (err) {
			this.error = errorMessage(err);
			return false;
		} finally {
			this.sending = false;
			this.poll();
		}
	}

	canUndo(messageId?: string) {
		return !!messageId && sessionStorage.getItem(`vflow:copilot-undo:${messageId}`) !== null;
	}

	/** Puts the canvas back as it was before this message. */
	undo(messageId: string) {
		const snap = sessionStorage.getItem(`vflow:copilot-undo:${messageId}`);
		if (snap) app.restoreCanvas(snap);
	}

	async stop() {
		try {
			await api.copilotStop(this.flowId);
		} catch (err) {
			this.error = errorMessage(err);
		}
		this.poll();
	}

	async reset() {
		try {
			await api.copilotReset(this.flowId);
			this.state = empty();
			this.handled.clear();
		} catch (err) {
			this.error = errorMessage(err);
		}
	}
}

export const copilot = new CopilotSession();
