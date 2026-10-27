import type {
	Pattern,
	StartResult,
	WithStart,
	AgentState,
	CopilotState,
	Definition,
	FlowDetail,
	FlowSummary,
	FlowVersion,
	Problem,
	RunState,
	RunSummary,
	RunView,
	ScheduleInfo,
	SessionInfo,
	Template,
	WorkerStatus
} from './types';

export class ApiError extends Error {
	constructor(
		message: string,
		readonly status: number,
		readonly problems: Problem[] = []
	) {
		super(message);
	}
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
	const res = await fetch(path, {
		method,
		credentials: 'same-origin',
		headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
		body: body === undefined ? undefined : JSON.stringify(body)
	});
	if (!res.ok) {
		let message = `${res.status} ${res.statusText}`;
		let problems: Problem[] = [];
		try {
			const data = await res.json();
			if (data?.error) message = data.error;
			if (Array.isArray(data?.problems)) problems = data.problems;
		} catch {
			/* not JSON */
		}
		throw new ApiError(message, res.status, problems);
	}
	if (res.status === 204) return undefined as T;
	const text = await res.text();
	return (text ? JSON.parse(text) : undefined) as T;
}

const enc = encodeURIComponent;
const query = (params: Record<string, string | number | undefined>) => {
	const q = new URLSearchParams();
	for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== '') q.set(k, String(v));
	const s = q.toString();
	return s ? `?${s}` : '';
};

export const api = {
	session: () => request<SessionInfo>('GET', '/api/session'),
	worker: () => request<WorkerStatus>('GET', '/api/worker'),

	listFlows: () => request<FlowSummary[]>('GET', '/api/flows'),
	createFlow: (d: Partial<Definition>) => request<FlowDetail>('POST', '/api/flows', d),
	getFlow: (id: string) => request<FlowDetail>('GET', `/api/flows/${enc(id)}`),
	saveFlow: (d: Definition) => request<Definition>('PUT', `/api/flows/${enc(d.id)}`, d),
	deleteFlow: (id: string) => request<void>('DELETE', `/api/flows/${enc(id)}`),
	/** Checks a flow as saving would, without saving it. */
	validateFlow: (d: Definition) => request<{ problems: Problem[] }>('POST', '/api/flows/validate', d),
	copilot: (id: string) => request<CopilotState>('GET', `/api/flows/${enc(id)}/copilot`),
	copilotSend: (id: string, m: { id: string; text: string; flow: unknown; clientId: string }) =>
		request<void>('POST', `/api/flows/${enc(id)}/copilot/messages`, m),
	copilotResult: (id: string, r: { callId: string; result?: unknown; error?: string }) =>
		request<unknown>('POST', `/api/flows/${enc(id)}/copilot/results`, r),
	copilotStop: (id: string) => request<void>('POST', `/api/flows/${enc(id)}/copilot/stop`),
	copilotReset: (id: string) => request<void>('POST', `/api/flows/${enc(id)}/copilot/reset`),
	/** Drops the user's copy of a built-in example. */
	resetFlow: (id: string) => request<FlowDetail>('POST', `/api/flows/${enc(id)}/reset`),
	versions: (id: string) => request<FlowVersion[]>('GET', `/api/flows/${enc(id)}/versions`),
	version: (id: string, v: number) => request<Definition>('GET', `/api/flows/${enc(id)}/versions/${v}`),

	setSchedule: (id: string, enabled: boolean) =>
		request<{ scheduleActive: boolean; paused?: boolean }>('POST', `/api/flows/${enc(id)}/schedule`, { enabled }),
	scheduleAction: (id: string, action: 'pause' | 'unpause' | 'trigger', note?: string) =>
		request<{ scheduleActive: boolean; paused?: boolean }>('POST', `/api/flows/${enc(id)}/schedule`, { action, note }),
	schedules: () => request<ScheduleInfo[]>('GET', '/api/schedules'),

	/** A flow that responds early answers in the same call (Update-With-Start). */
	/** with sends the input as that Signal or Update, with start, to the run for its key (keyed flows). */
	startRun: (id: string, input?: unknown, withMessage?: WithStart) =>
		request<StartResult>('POST', `/api/flows/${enc(id)}/runs`, {
			...(input === undefined ? {} : { input }),
			...(withMessage ? { with: withMessage.kind, message: withMessage.name } : {})
		}),
	listRuns: (id: string) => request<RunSummary[]>('GET', `/api/flows/${enc(id)}/runs`),
	allRuns: (p: { status?: string; flowId?: string; limit?: number } = {}) =>
		request<RunSummary[]>('GET', `/api/runs${query(p)}`),

	getRun: (wid: string) => request<RunView>('GET', `/api/runs/${enc(wid)}`),
	/** Full state with outputs. On serverless this may wake a worker. */
	runDetails: (wid: string) => request<RunState>('GET', `/api/runs/${enc(wid)}/details`),
	/** Build a connector step's Activity argument from sample data, with the Workflow's own code. */
	previewConnector: (type: string, config: unknown, env: unknown) =>
		request<{ argument: Record<string, unknown> | null; errors: Record<string, string> }>('POST', '/api/connectors/preview', { type, config, env }),
	/** Build a Transform step's mapped output from sample data, with the Workflow's own code. */
	previewTransform: (config: unknown, env: unknown) =>
		request<{ argument: Record<string, unknown> | null; errors: Record<string, string> }>('POST', '/api/transform/preview', { config, env }),
	signal: (wid: string, name: string, payload?: unknown) =>
		request<void>('POST', `/api/runs/${enc(wid)}/signal`, { name, payload }),
	update: (wid: string, name: string, payload?: unknown) =>
		request<{ result: unknown }>('POST', `/api/runs/${enc(wid)}/updates/${enc(name)}`, { payload }),
	vars: (wid: string) => request<Record<string, unknown>>('GET', `/api/runs/${enc(wid)}/vars`),
	/** item: for a step in a For each body, the item the decision is for. */
	approve: (wid: string, nodeId: string, approved: boolean, comment?: string, item?: number) =>
		request<void>('POST', `/api/runs/${enc(wid)}/approve`, { nodeId, approved, comment, item }),
	/** Resume a step paused after its retries ran out. */
	fix: (wid: string, nodeId: string, action: 'retry' | 'fail', change: { url?: string; body?: string } = {}, item?: number) =>
		request<void>('POST', `/api/runs/${enc(wid)}/fix`, { nodeId, action, item, ...change }),
	cancel: (wid: string) => request<void>('POST', `/api/runs/${enc(wid)}/cancel`),
	agent: (wid: string, nodeId: string) => request<AgentState>('GET', `/api/runs/${enc(wid)}/agents/${enc(nodeId)}`),
	answer: (wid: string, nodeId: string, answer: string) =>
		request<void>('POST', `/api/runs/${enc(wid)}/agents/${enc(nodeId)}/answer`, { answer }),

	templates: () => request<Template[]>('GET', '/api/templates'),
	template: (id: string, t?: string) => request<Template>('GET', `/api/templates/${enc(id)}${query({ t })}`),
	publish: (flowId: string, listed: boolean) =>
		request<{ template: Template; shareUrl: string }>('POST', '/api/templates', { flowId, listed }),
	useTemplate: (id: string, t?: string) => request<FlowDetail>('POST', `/api/templates/${enc(id)}/use`, t ? { t } : {}),
	deleteTemplate: (id: string) => request<void>('DELETE', `/api/templates/${enc(id)}`),

	patterns: () => request<{ categories: string[]; patterns: Pattern[] }>('GET', '/api/patterns'),
	pattern: (id: string) => request<Pattern>('GET', `/api/patterns/${enc(id)}`),
	/** Copies the pattern's example (and any child flows) into the user's library. */
	usePattern: (id: string) => request<FlowDetail>('POST', `/api/patterns/${enc(id)}/use`)
};
