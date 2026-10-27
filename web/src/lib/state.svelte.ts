import { SvelteSet } from 'svelte/reactivity';
import { toast } from 'svelte-sonner';
import { goto } from '$app/navigation';
import { api, ApiError } from './api';
import { blankFlow } from './nodes';
import { toRunState } from './progress';
import { loopBody } from './loops';
import { shell } from './shell.svelte';
import { splitSeconds } from './duration';
import type {
	Definition,
	Direction,
	FlowDetail,
	NodeState,
	NodeType,
	PendingActivity,
	Problem,
	RunState,
	RunSummary,
	RunView,
	TriggerConfig,
	WithStart
} from './types';
import { isConnector, type ConnectorConfig } from './types';
import { migrateParams } from './connectors';

export type Mode = 'build' | 'runs';

/** A new flow's ID, chosen in the browser: "f-" and 10 random hex digits. */
function newFlowId() {
	const bytes = crypto.getRandomValues(new Uint8Array(5));
	return 'f-' + Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
}

export function errorMessage(err: unknown) {
	return err instanceof Error ? err.message : String(err);
}

/** Definitions saved before settings existed get sane defaults; original per-step fields are folded in. */
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function normalizeFlow(d: Definition): Definition {
	d.settings ??= { activity: { retry: {} } };
	d.settings.activity ??= { retry: {} };
	d.settings.activity.retry ??= {};
	d.settings.variables ??= [];
	d.settings.direction ??= 'LR';
	d.edges ??= [];
	for (const n of d.nodes ?? []) {
		const c = n.config as Record<string, any>;
		if (n.type === 'http') {
			// Fold the original per-step fields into activity options.
			c.activity ??= { retry: {} };
			c.activity.retry ??= {};
			if (c.timeoutSeconds && !c.activity.startToCloseSeconds) c.activity.startToCloseSeconds = c.timeoutSeconds;
			if (c.retry) c.activity.retry = { ...c.retry, ...stripEmpty(c.activity.retry) };
			delete c.timeoutSeconds;
			delete c.retry;
		}
		if (isConnector(n.type)) {
			migrateParams(c as ConnectorConfig);
			c.settings ??= {};
			c.mapping ??= {};
			c.activity ??= { retry: {} };
			c.activity.retry ??= {};
		}
		if (n.type === 'agent') {
			c.modelActivity ??= { retry: {} };
			c.toolActivity ??= { retry: {} };
		}
		if (n.type === 'delay' && !c.mode) {
			const { amount, unit } = splitSeconds(c.seconds ?? 10);
			Object.assign(c, { mode: 'duration', amount, unit });
			delete c.seconds;
		}
	}
	return d;
}

function stripEmpty(o: Record<string, unknown>) {
	return Object.fromEntries(Object.entries(o).filter(([, v]) => v !== undefined && v !== 0 && !(Array.isArray(v) && !v.length)));
}

/** The editor and the run view. One instance per tab; the API scopes everything to the session. */
class AppState {
	flow = $state<Definition | null>(null);
	webhookUrl = $state('');
	scheduleActive = $state(false);
	/** The open flow is a built-in example; customized: the user saved their own copy. */
	builtIn = $state(false);
	customized = $state(false);
	/** Bumped when nodes or edges are added or removed, or positions are rewritten, so the canvas rebuilds. */
	structure = $state(0);
	/** The copilot panel is open. */
	copilotOpen = $state(false);
	/** Steps the copilot added or changed since the last save, highlighted on the canvas. */
	copilotTouched = new SvelteSet<string>();
	problems = $state<Problem[]>([]);
	saving = $state(false);
	private savedSnapshot = $state('');

	mode = $state<Mode>('build');
	addRequest = $state<{ type: NodeType; variant?: string; seq: number } | null>(null);
	/** Set by the Tidy up button; the build canvas consumes it with measured node sizes. */
	tidyRequest = $state(0);
	/** How the canvas follows the last tidy-up: fit the whole flow in view (Tidy up), or keep one step where it was on screen (auto tidy-up). */
	tidyFollow: { fit: true } | { anchor: string } = { fit: true };
	/** Tidy up after each new connection. Remembered per browser; on unless turned off. */
	autoTidy = $state(typeof localStorage === 'undefined' || localStorage.getItem('vflow:auto-tidy') !== 'off');
	leftCollapsed = $state(false);
	rightCollapsed = $state(false);
	selectedNodeId = $state<string | null>(null);
	settingsOpen = $state(false);
	historyOpen = $state(false);

	runs = $state<RunSummary[]>([]);
	selectedRunId = $state<string | null>(null);
	runView = $state<RunView | null>(null);
	/** Full state with outputs, fetched only when asked for (it may wake a worker). */
	details = $state<RunState | null>(null);
	detailsLoading = $state(false);
	runLoadError = $state('');
	/** The early response of the run this browser just started, if its flow responds early. */
	earlyResponse = $state<{ workflowId: string; response?: unknown; error?: string } | null>(null);

	dirty = $derived(this.flow !== null && JSON.stringify(this.flow) !== this.savedSnapshot);
	direction = $derived<Direction>(this.flow?.settings?.direction ?? 'LR');
	lastState = $derived(this.runView ? toRunState(this.runView, this.details) : null);
	/** The For each item picked in the run view, whose own step states replace the body's summary. */
	selectedItem = $state<{ each: string; index: number } | null>(null);
	/** The run state as drawn: lastState, with the picked item's body steps. */
	shownState = $derived.by(() => {
		const st = this.lastState;
		const pick = this.selectedItem;
		const item = pick ? st?.items?.[pick.each]?.find((i) => i.index === pick.index) : undefined;
		if (!st || !pick || !item) return st;
		const nodes = { ...st.nodes };
		for (const id of loopBody(st.flow, pick.each)) nodes[id] = item.nodes[id] ?? { status: 'pending' };
		return { ...st, nodes };
	});
	runActive = $derived(this.runView?.execution.status === 'Running');

	selectedNode = $derived.by(() => {
		const id = this.selectedNodeId;
		const nodes = this.mode === 'runs' || !this.flow ? this.lastState?.flow.nodes : this.flow.nodes;
		return nodes?.find((n) => n.id === id) ?? null;
	});

	trigger = $derived(this.flow?.nodes.find((n) => n.type === 'trigger') ?? null);

	private setFlow(flow: Definition) {
		this.flow = normalizeFlow(flow);
		this.savedSnapshot = JSON.stringify(this.flow);
		this.problems = [];
		this.copilotTouched.clear();
		this.structure++;
	}

	/** Puts the canvas back as it was (Undo this turn), leaving it unsaved. */
	restoreCanvas(snapshot: string) {
		const def = JSON.parse(snapshot) as Definition;
		if (!this.flow || def.id !== this.flow.id) return;
		this.flow = normalizeFlow({ ...def, version: this.flow.version, updatedAt: this.flow.updatedAt });
		this.copilotTouched.clear();
		this.problems = [];
		this.structure++;
	}

	async openFlow(id: string) {
		if (this.flow?.id === id) return;
		try {
			this.showFlow(await api.getFlow(id));
		} catch (err) {
			toast.error(`Could not open flow: ${errorMessage(err)}`);
		}
	}

	/** Opens a flow the API just created, from its response, and goes to its editor. */
	async openCreated(detail: FlowDetail) {
		// The editor page then has nothing to fetch, and the nav list catches up on its own.
		this.showFlow(detail);
		shell.loadFlows();
		this.mode = 'build';
		await goto(`/workflows/${encodeURIComponent(detail.flow.id)}`);
	}

	private showFlow(detail: FlowDetail) {
		this.setFlow(detail.flow);
		this.webhookUrl = detail.webhookUrl;
		this.scheduleActive = detail.scheduleActive;
		this.builtIn = !!detail.builtIn;
		this.customized = !!detail.customized;
		this.selectedNodeId = null;
		this.closeRun();
		this.runs = [];
	}

	closeFlow() {
		this.flow = null;
		this.savedSnapshot = '';
	}

	/** Set while a new flow's create is in flight; resolves false if it failed. */
	private creating: Promise<boolean> | null = null;

	/** Waits for a new flow to exist in the library. Anything that needs it saved calls this first. */
	async whenCreated(): Promise<boolean> {
		return this.creating ?? true;
	}

	/**
	 * Opens the editor on a new blank flow at once and creates it in the background.
	 * The browser names the flow, so the editor needn't wait; the library refuses an ID in use.
	 */
	async newFlow() {
		const flow = { ...blankFlow(), id: newFlowId(), version: 0, updatedAt: new Date().toISOString() } as Definition;
		this.showFlow({ flow, webhookUrl: '', scheduleActive: false });
		this.mode = 'build';
		const creating = (this.creating = this.create(flow));
		creating.finally(() => {
			if (this.creating === creating) this.creating = null;
		});
		toast.success('New flow. Drag steps in from the palette.');
		await goto(`/workflows/${encodeURIComponent(flow.id)}`);
	}

	private async create(flow: Definition): Promise<boolean> {
		try {
			const created = await api.createFlow(structuredClone(flow));
			shell.loadFlows();
			if (this.flow?.id !== flow.id) return true;
			// Take the library's version and time without touching edits made meanwhile.
			const { version, updatedAt } = created.flow;
			const saved = { ...JSON.parse(this.savedSnapshot), version, updatedAt };
			this.flow.version = version;
			this.flow.updatedAt = updatedAt;
			this.savedSnapshot = JSON.stringify(saved);
			this.webhookUrl = created.webhookUrl;
			return true;
		} catch (err) {
			toast.error(`Could not create flow: ${errorMessage(err)}`);
			if (this.flow?.id === flow.id) {
				this.closeFlow();
				await goto('/workflows');
			}
			return false;
		}
	}

	/** Loads an older version into the editor; saving makes it the newest version. */
	restoreVersion(def: Definition) {
		if (!this.flow) return;
		const restored = normalizeFlow(structuredClone({ ...def, id: this.flow.id, version: this.flow.version }));
		this.flow = restored;
		this.selectedNodeId = null;
		this.structure++;
		toast.success(`Loaded version ${def.version}. Save to make it the current version.`);
	}

	async save(): Promise<boolean> {
		if (!this.flow || !(await this.whenCreated())) return false;
		this.saving = true;
		try {
			const saved = normalizeFlow(await api.saveFlow($state.snapshot(this.flow)));
			// Keep the canvas as drawn: the server echoes the same nodes.
			this.flow = saved;
			this.savedSnapshot = JSON.stringify(saved);
			this.problems = [];
			// Saving a built-in example makes it the user's own copy.
			if (this.builtIn) this.customized = true;
			this.copilotTouched.clear();
			shell.loadFlows();
			toast.success(`Saved version ${saved.version}`);
			return true;
		} catch (err) {
			if (err instanceof ApiError && err.problems.length > 0) {
				this.problems = err.problems;
				toast.error(`Fix ${err.problems.length} problem${err.problems.length === 1 ? '' : 's'} before saving`, {
					description: err.problems
						.slice(0, 3)
						.map((p) => p.message)
						.join(' · ')
				});
			} else {
				toast.error(`Save failed: ${errorMessage(err)}`);
			}
			return false;
		} finally {
			this.saving = false;
		}
	}

	/** Drops the user's copy of the open built-in example and shows the example as built. */
	async resetToExample(): Promise<boolean> {
		if (!this.flow) return false;
		try {
			const detail = await api.resetFlow(this.flow.id);
			this.showFlow(detail);
			shell.loadFlows();
			toast.success('Back to the built-in example');
			return true;
		} catch (err) {
			toast.error(`Could not reset: ${errorMessage(err)}`);
			return false;
		}
	}

	problemsFor(nodeId: string): Problem[] {
		return this.problems.filter((p) => p.nodeId === nodeId);
	}

	/** Lay the flow out, then fit it in view, or keep the anchor step where it was on screen. */
	requestTidy(anchor?: string) {
		this.tidyFollow = anchor ? { anchor } : { fit: true };
		this.tidyRequest++;
	}

	setAutoTidy(on: boolean) {
		this.autoTidy = on;
		localStorage.setItem('vflow:auto-tidy', on ? 'on' : 'off');
	}

	/** Add a step from the palette. variant picks a connector's operation. */
	requestAdd(type: NodeType, variant?: string) {
		this.addRequest = { type, variant, seq: (this.addRequest?.seq ?? 0) + 1 };
	}

	toggleFocus() {
		const focus = !(this.leftCollapsed && this.rightCollapsed);
		this.leftCollapsed = focus;
		this.rightCollapsed = focus;
		shell.navCollapsed = focus;
	}

	setDirection(d: Direction) {
		if (!this.flow) return;
		this.flow.settings.direction = d;
		this.structure++;
	}

	sampleInput(): string {
		const t = this.trigger?.config as TriggerConfig | undefined;
		return t?.sampleInput?.trim() || '{}';
	}

	async startRun(input?: unknown, withMessage?: WithStart) {
		if (!this.flow || !(await this.whenCreated())) return;
		if (this.dirty && !(await this.save())) return;
		try {
			const res = await api.startRun(this.flow.id, input, withMessage);
			const workflowId = res.workflowId;
			if (res.responded) {
				this.earlyResponse = { workflowId, response: res.response };
				toast.success(withMessage ? `${withMessage.name} answered` : 'The run responded and carries on', { description: JSON.stringify(res.response) });
			} else if (res.responseError) {
				this.earlyResponse = { workflowId, error: res.responseError };
				toast.warning(withMessage ? `${withMessage.name} wasn't handled` : 'Run started, but it did not respond', { description: res.responseError });
			} else {
				this.earlyResponse = null;
				toast.success(withMessage ? `${withMessage.name} delivered` : 'Run started', { description: workflowId });
			}
			this.mode = 'runs';
			await Promise.all([this.loadRuns(false), this.openRun(workflowId)]);
		} catch (err) {
			toast.error(`Could not start run: ${errorMessage(err)}`);
		}
	}

	async setSchedule(enabled: boolean) {
		if (!this.flow || !(await this.whenCreated())) return;
		if (this.dirty && !(await this.save())) return;
		try {
			const res = await api.setSchedule(this.flow.id, enabled);
			this.scheduleActive = res.scheduleActive;
			shell.loadFlows();
			toast.success(res.scheduleActive ? 'Temporal Schedule is running this flow' : 'Schedule removed');
		} catch (err) {
			toast.error(`Could not update schedule: ${errorMessage(err)}`);
		}
	}

	async loadRuns(selectLatest: boolean) {
		if (!this.flow) return;
		try {
			this.runs = (await api.listRuns(this.flow.id)) ?? [];
			if (selectLatest && !this.selectedRunId && this.runs.length > 0) await this.openRun(this.runs[0].workflowId);
		} catch {
			/* background refresh; keep the last list */
		}
	}

	closeRun() {
		this.selectedRunId = null;
		this.runView = null;
		this.details = null;
		this.selectedItem = null;
	}

	async openRun(workflowId: string) {
		if (this.selectedRunId !== workflowId) {
			this.selectedRunId = workflowId;
			this.runView = null;
			this.details = null;
			this.selectedNodeId = null;
			this.selectedItem = null;
		}
		await this.refreshRun();
	}

	async refreshRun() {
		const wid = this.selectedRunId;
		if (!wid) return;
		try {
			const view = await api.getRun(wid);
			if (wid !== this.selectedRunId) return;
			if (view.flow) normalizeFlow(view.flow);
			this.runView = view;
			this.runLoadError = '';
		} catch (err) {
			this.runLoadError = errorMessage(err);
		}
	}

	/** Reads live state with outputs. On serverless this can wake a worker, so it is opt-in. */
	async loadDetails() {
		const wid = this.selectedRunId;
		if (!wid) return;
		this.detailsLoading = true;
		try {
			const d = await api.runDetails(wid);
			if (wid === this.selectedRunId) this.details = d;
		} catch (err) {
			toast.error(`Could not load outputs: ${errorMessage(err)}`);
		} finally {
			this.detailsLoading = false;
		}
	}

	nodeRunState(nodeId: string): NodeState | undefined {
		return this.shownState?.nodes[nodeId];
	}

	/** The step's pending Activity: the picked item's, for a For each body step. */
	pendingActivity(nodeId: string): PendingActivity | undefined {
		const pick = this.selectedItem;
		return this.pendingActivities(nodeId).find((a) => !pick || a.item === undefined || a.item === pick.index);
	}

	/** Every pending Activity of a step: one per item running it, in a For each body. */
	pendingActivities(nodeId: string): PendingActivity[] {
		return this.runView?.pendingActivities?.filter((a) => a.nodeId === nodeId) ?? [];
	}

	async cancelRun() {
		if (!this.selectedRunId) return;
		try {
			await api.cancel(this.selectedRunId);
			toast.success('Cancellation requested');
			await this.refreshRun();
		} catch (err) {
			toast.error(`Cancel failed: ${errorMessage(err)}`);
		}
	}

	async setMode(mode: Mode) {
		if (this.mode === mode) return;
		this.mode = mode;
		this.selectedNodeId = null;
		if (mode === 'runs') await this.loadRuns(true);
	}
}

export const app = new AppState();
