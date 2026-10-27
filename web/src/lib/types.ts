// Mirrors internal/flow (definition.go, run.go) and docs/api.md. Keep the JSON
// field names in step with the Go struct tags.

export type NodeType =
	| 'trigger'
	| 'onStart'
	| 'onSignal'
	| 'onUpdate'
	| 'http'
	| 'waitForCondition'
	| 'waitForSignal'
	| 'approval'
	| 'delay'
	| 'condition'
	| 'transform'
	| 'setVariable'
	| 'firstOf'
	| 'respond'
	| 'agent'
	| 'runFlow'
	| 'forEach'
	| 'loop'
	| 'salesforce'
	| 'stripe'
	| 'servicenow'
	| 'slack'
	| 'github'
	| 'saga'
	| 'end';

/** Outcomes per node type. The first is the main path; the rest are secondary. */
export const HANDLES: Record<NodeType, string[]> = {
	trigger: ['out'],
	onStart: ['out'],
	onSignal: ['out'],
	onUpdate: ['out'],
	http: ['out', 'error'],
	waitForCondition: ['out', 'timeout'],
	waitForSignal: ['received', 'timeout'],
	approval: ['approved', 'rejected', 'timeout'],
	delay: ['out'],
	condition: ['true', 'false'],
	transform: ['out'],
	setVariable: ['out'],
	firstOf: ['out', 'error'],
	respond: ['out'],
	agent: ['out', 'error'],
	runFlow: ['out', 'error'],
	forEach: ['out', 'body', 'error'],
	loop: ['done', 'body', 'limit'],
	salesforce: ['out', 'error'],
	stripe: ['out', 'error'],
	servicenow: ['out', 'error'],
	slack: ['out', 'error'],
	github: ['out', 'error'],
	/** A saga's outcomes run only once it has unwound. */
	saga: ['compensated', 'failed'],
	/** End run completes the run: nothing follows it. */
	end: []
};

/** The outcomes a node shows: First of only fails when it waits for a success, and only a For each that runs steps has a body. */
export function handlesFor(node: FlowNode): string[] {
	if (node.type === 'firstOf' && (node.config as FirstOfConfig).wins !== 'succeeds') return ['out'];
	if (node.type === 'forEach' && (node.config as ForEachConfig).run !== 'steps') return ['out', 'error'];
	return HANDLES[node.type];
}


export const ENTRY_TYPES: NodeType[] = ['trigger', 'onStart', 'onSignal', 'onUpdate', 'saga'];
/** Types a flow has at most one of. */
export const SINGLE_TYPES: NodeType[] = ['trigger', 'onStart'];
export const isEntry = (t: NodeType) => ENTRY_TYPES.includes(t);

export type Direction = 'LR' | 'TB';

export interface Position {
	x: number;
	y: number;
}

export interface FlowNode {
	id: string;
	type: NodeType;
	key: string;
	label: string;
	position: Position;
	config: NodeConfig;
}

export interface FlowEdge {
	id: string;
	source: string;
	target: string;
	sourceHandle?: string;
}

export interface VariableDef {
	name: string;
	/** Starting value as JSON text. */
	initial?: string;
	visible?: boolean;
}

export interface RetryConfig {
	maxAttempts?: number;
	initialIntervalSeconds?: number;
	backoffCoefficient?: number;
	maximumIntervalSeconds?: number;
	nonRetryableErrorTypes?: string[];
	nonRetryableStatusCodes?: number[];
}

/** Zero/absent fields inherit: step → flow defaults → system defaults. */
export interface ActivityOptions {
	startToCloseSeconds?: number;
	scheduleToCloseSeconds?: number;
	retry: RetryConfig;
	/** Takes over once retry's attempts are used up: quick retries first, then slow ones. */
	slowRetry?: RetryConfig;
}

export const SYSTEM_ACTIVITY_DEFAULTS: Required<Pick<ActivityOptions, 'startToCloseSeconds'>> & ActivityOptions = {
	startToCloseSeconds: 30,
	retry: { maxAttempts: 5, initialIntervalSeconds: 1, backoffCoefficient: 2, maximumIntervalSeconds: 60 }
};
export const MAX_START_TO_CLOSE_SECONDS = 600;

export interface FlowSettings {
	direction?: Direction;
	variables?: VariableDef[];
	activity: ActivityOptions;
	keyedBy?: string;
	/** Keyed flows: the run outlives its main path, serving events until an End run step (or for ever). Off, it completes with its main path. */
	entity?: boolean;
	/** 1 (first) to 5 (last); 0 or absent is the default, 3. */
	priority?: number;
	/** Keyed flows: what a start does when the key's run is running. Absent is use_existing. */
	idConflictPolicy?: IDConflictPolicy;
	/** Keyed flows: whether a start may reuse the ID of the key's closed run. Absent is allow_duplicate. */
	idReusePolicy?: IDReusePolicy;
}

export type IDConflictPolicy = 'use_existing' | 'fail' | 'terminate_existing';
export type IDReusePolicy = 'allow_duplicate' | 'allow_duplicate_failed_only' | 'reject_duplicate';

export interface Definition {
	id: string;
	name: string;
	description?: string;
	version: number;
	updatedAt: string;
	settings: FlowSettings;
	nodes: FlowNode[];
	edges: FlowEdge[];
}

export type TimeUnit = 'seconds' | 'minutes' | 'hours' | 'days';

export interface CalendarSpec {
	days?: string[];
	hour: number;
	minute: number;
}

export interface ScheduleConfig {
	every?: number;
	everyUnit?: TimeUnit;
	cron?: string;
	calendar?: CalendarSpec;
	timezone?: string;
	overlap?: 'skip' | 'buffer_one' | 'buffer_all' | 'cancel_other' | 'terminate_other' | 'allow_all';
	catchupWindowSeconds?: number;
	jitterSeconds?: number;
}

export interface TriggerConfig {
	kind: 'manual' | 'webhook' | 'schedule';
	sampleInput?: string;
	schedule?: ScheduleConfig;
	/** Original interval field, still accepted. */
	everySeconds?: number;
	/** Manual and webhook runs: created now, start working after this (duration mode). */
	startDelay?: DelayConfig;
}

/** On start has no settings: its lane runs once, before the main path and every other lane. */
export type OnStartConfig = Record<string, never>;

export interface OnSignalConfig {
	signalName: string;
}

export interface OnUpdateConfig {
	updateName: string;
	validator?: string;
}

export interface HTTPRequestSpec {
	method: string;
	url: string;
	headers?: Record<string, string>;
	body?: string;
}

export interface HTTPConfig extends HTTPRequestSpec {
	activity: ActivityOptions;
	undo?: HTTPRequestSpec;
	/** When retries run out, wait for a person to retry (maybe corrected) or give up. */
	pauseForFix?: boolean;
	/** A rate limit shared by every run (SessionInfo.rateLimits): the request runs on that limit's task queue. */
	rateLimit?: string;
	/** Original per-step fields, still accepted and folded into activity. */
	timeoutSeconds?: number;
	retry?: RetryConfig;
}

/** Business connector steps (internal/connectors): one service each, run by operation. */
export const CONNECTOR_TYPES = ['salesforce', 'stripe', 'servicenow', 'slack', 'github'] as const;
export type ConnectorType = (typeof CONNECTOR_TYPES)[number];
export const isConnector = (t: NodeType): t is ConnectorType => (CONNECTOR_TYPES as readonly string[]).includes(t);

/** A connector step. The service and its credentials are the worker's, never the flow's. */
export interface ConnectorConfig {
	operation: string;
	/** What the operation acts on (an sObject, a table, a repository): templates, or booleans for switches. */
	settings?: Record<string, string | boolean>;
	/** A template per input field, by dotted path (values.Name). */
	mapping?: Record<string, string>;
	/** Compensate if the run fails later, as the operation's spec says. */
	undo?: boolean;
	activity: ActivityOptions;
	rateLimit?: string;
	/** Saved before settings and mapping existed; normalizeFlow converts it. */
	params?: Record<string, string | boolean>;
}

/** Mirrors connectors.Connector: served with the session. */
export interface ConnectorSpec {
	type: ConnectorType;
	label: string;
	connection: string;
	errors: string;
	operations: ConnectorOperation[];
	/** Fields of the service's records by name (Salesforce objects, ServiceNow tables). */
	catalog?: Record<string, ConnectorField[]>;
	catalogOf?: string;
}

export interface ConnectorOperation {
	name: string;
	label: string;
	activity: string;
	settings?: ConnectorField[];
	inputs?: ConnectorField[];
	outputs?: ConnectorField[];
	/** Card text; {name} stands for a setting. */
	summary: string;
	note?: string;
	/** The operation's built-in rollback, when it has one. */
	compensation?: ConnectorCompensation;
}

/** A built-in rollback: another operation of the connector, mapped from this one's argument (arg) and output (out). */
export interface ConnectorCompensation {
	operation: string;
	settings?: string[];
	mapping: Record<string, string>;
	note: string;
}

/** A saga: steps on the main path whose work can be undone. Its outcomes run once it has unwound. */
export interface SagaConfig {
	members?: SagaMember[];
}

export interface SagaMember {
	step: string;
	/** Its failure unwinds the saga; otherwise it is handled and the run carries on past it. */
	unwinds: boolean;
	/** The step that undoes it: the first of a chain that runs only when the saga unwinds. */
	compensation?: string;
	/** Compensate with the connector operation's built-in rollback. */
	builtIn?: boolean;
}

/** Where a saga stands in a run. */
export interface SagaState {
	registered?: string[];
	status?: '' | 'unwinding' | 'compensated' | 'failed';
	trigger?: string;
	error?: string;
}

export type FieldType = 'string' | 'number' | 'integer' | 'boolean' | 'date' | 'datetime' | 'object' | 'array' | 'json' | 'any' | '';

/** Mirrors connectors.Field: a setting, an input or an output. */
export interface ConnectorField {
	name: string;
	label?: string;
	type: FieldType;
	required?: boolean;
	description?: string;
	enum?: string[];
	enumLabels?: string[];
	suggestions?: string[];
	default?: string;
	placeholder?: string;
	multiline?: boolean;
	pattern?: string;
	patternHint?: string;
	readOnly?: boolean;
	fields?: ConnectorField[];
	items?: string;
	open?: boolean;
	/** The setting whose value picks this object's fields from the catalog. */
	catalog?: string;
	/** The catalog's required fields are required here. */
	creating?: boolean;
}

export interface WaitConditionConfig {
	signalName?: string;
	condition: string;
	timeoutSeconds?: number;
}

export interface WaitSignalConfig {
	signalName: string;
	timeoutSeconds?: number;
	/** Gather a batch of events instead of taking one (event accumulator). */
	collect?: CollectConfig;
	/** Which events are this step's, over event and (in a For each body) item: event.orderId == item.id. */
	match?: string;
}

export interface CollectConfig {
	quietSeconds: number;
	maxEvents?: number;
	maxWaitSeconds?: number;
	/** Expression over `event`; a repeated key is dropped. */
	dedupeBy?: string;
}

export type EndConfig = Record<string, never>;

export interface LoopConfig {
	/** Checked after each pass; true takes the done outcome. */
	until: string;
	/** 0 or absent: no limit. */
	maxPasses: number;
	/** Durable timer between passes. */
	pause?: DelayConfig;
}

export interface ApprovalConfig {
	title: string;
	details?: string;
	timeoutSeconds?: number;
}

export interface DelayConfig {
	mode?: 'duration' | 'expression';
	amount?: number;
	unit?: TimeUnit;
	expression?: string;
	/** Original field, still accepted. */
	seconds?: number;
}

export interface ConditionConfig {
	expression: string;
}

/** Reshapes data in Workflow code: mapped output fields, or a JSON template. */
export interface TransformConfig {
	/** A JSON template; used when the step has no fields. */
	output?: string;
	/** The output's fields, filled by mapping and lists. Present (even empty) means the mapper edits this step. */
	fields?: ConnectorField[];
	/** A template per output field, by path: customer.email, lines[].sku. */
	mapping?: Record<string, string>;
	/** Lists built element by element: lines from each element of a source list. Inside, `it` is the element. */
	lists?: Record<string, ListMapping>;
}

export interface ListMapping {
	/** An expression returning the source list. */
	from: string;
	/** Keep only elements where this expression over `it` is true. */
	where?: string;
}

export interface Assignment {
	name: string;
	value: string;
}

export interface SetVariableConfig {
	assignments: Assignment[];
}

export interface FirstOfConfig {
	/** 'arrives' (default): the first branch to arrive wins, and a failed branch fails the run.
	 *  'succeeds': failed branches drop out; the step fails only when every branch has. */
	wins?: 'arrives' | 'succeeds';
}

export interface RespondConfig {
	value: string;
}

export interface AgentToolParam {
	name: string;
	description: string;
}

export interface AgentTool {
	name: string;
	description: string;
	method: string;
	url: string;
	params: AgentToolParam[];
}

export interface AgentConfig {
	instructions: string;
	tools: AgentTool[];
	allowAskHuman: boolean;
	maxTurns: number;
	modelActivity: ActivityOptions;
	toolActivity: ActivityOptions;
}

export type ParentClosePolicy = 'terminate' | 'request_cancel' | 'abandon';

export interface RunFlowConfig {
	flowId: string;
	input?: string;
	parentClosePolicy?: ParentClosePolicy;
	/** 'result' (default) waits for the child; 'start' carries on once it has started (needs abandon). */
	wait?: 'result' | 'start';
}

export interface ForEachConfig {
	items: string;
	/** 'flow' (default) runs flowId per item as a child Workflow; 'steps' runs the body outcome's steps inside this run. */
	run?: 'flow' | 'steps';
	flowId?: string;
	input?: string;
	concurrency: number;
	/** At most count item starts per second or minute. */
	rate?: { count: number; per: 'second' | 'minute' };
	/** 'all' (default), 'first' success, or 'count' successes (a quorum). */
	finishWhen?: 'all' | 'first' | 'count';
	count?: number;
}

export type NodeConfig =
	| TriggerConfig
	| OnStartConfig
	| OnSignalConfig
	| OnUpdateConfig
	| HTTPConfig
	| ConnectorConfig
	| SagaConfig
	| WaitConditionConfig
	| WaitSignalConfig
	| ApprovalConfig
	| DelayConfig
	| ConditionConfig
	| TransformConfig
	| SetVariableConfig
	| FirstOfConfig
	| RespondConfig
	| AgentConfig
	| RunFlowConfig
	| ForEachConfig
	| LoopConfig
	| EndConfig;

export interface Problem {
	nodeId?: string;
	message: string;
}

// ---- Run state ----

export type RunStatus = 'running' | 'completed' | 'failed';
export type NodeStatus = 'pending' | 'running' | 'waiting' | 'completed' | 'failed' | 'skipped';
/** Temporal execution status names, as the API returns them. */
export type ExecutionStatus = 'Running' | 'Completed' | 'Failed' | 'Canceled' | 'Terminated' | 'TimedOut' | string;

export interface Waiting {
	kind: 'signal' | 'collect' | 'condition' | 'approval' | 'timer' | 'fix' | 'children' | string;
	signalName?: string;
	title?: string;
	details?: string;
	until?: string;
	/** On a step paused for a fix: the request it would send. */
	request?: HTTPRequestSpec;
}

export interface NodeState {
	status: NodeStatus;
	startedAt?: string;
	endedAt?: string;
	output?: unknown;
	error?: string;
	handle?: string;
	waiting?: Waiting;
	childWorkflowId?: string;
	invocations?: number;
	undo?: 'running' | 'done' | 'failed' | string;
	progress?: string;
}

export interface EventState {
	count: number;
	last?: unknown;
	lastAt?: string;
	pending: number;
}

export interface RunState {
	flowId: string;
	flowName: string;
	flowVersion: number;
	source: string;
	status: RunStatus;
	error?: string;
	startedAt: string;
	endedAt?: string;
	nodes: Record<string, NodeState>;
	callbackUrl: string;
	events: Record<string, EventState>;
	vars?: Record<string, unknown>;
	compensation?: '' | 'running' | 'done' | 'failed' | string;
	/** Each saga's state, by its Saga step's ID. */
	sagas?: Record<string, SagaState>;
	key?: string;
	generation?: number;
	flow: Definition;
	/** Per For each that runs steps: its most recent items, each with its own body step states. */
	items?: Record<string, ItemState[]>;
}

/** One item of a For each that runs steps. */
export interface ItemState {
	index: number;
	item?: unknown;
	status: NodeStatus;
	error?: string;
	startedAt?: string;
	endedAt?: string;
	nodes: Record<string, NodeState>;
}

/**
 * One node's progress in the run memo, with short keys to keep the memo small:
 * s status, h handle, e error, w waiting, t0 started, t1 ended,
 * i invocations, u undo, p progress note, c child workflow ID.
 */
export interface NodeProgress {
	s: NodeStatus;
	h?: string;
	e?: string;
	w?: Waiting;
	t0?: string;
	t1?: string;
	i?: number;
	u?: string;
	p?: string;
	c?: string;
}

/** The run memo: everything the run view draws without waking a worker. */
export interface Progress {
	status?: RunStatus;
	error?: string;
	startedAt?: string;
	endedAt?: string;
	source?: string;
	nodes: Record<string, NodeProgress>;
	vars?: Record<string, unknown>;
	/** The memo keeps a count per Signal name; full EventState comes from /details. */
	events?: Record<string, EventState | number>;
	compensation?: string;
	sagas?: Record<string, SagaState>;
	key?: string;
	generation?: number;
}

export interface TraceEntry {
	kind: 'reasoning' | 'tool_call' | 'tool_result' | 'question' | 'answer' | 'final' | string;
	text?: string;
	tool?: string;
	input?: unknown;
	isError?: boolean;
	at: string;
}

export interface AgentState {
	status: string;
	turn: number;
	maxTurns: number;
	trace: TraceEntry[];
	pendingQuestion?: string;
	answer?: string;
}

// ---- API envelopes (docs/api.md) ----

export type WorkerMode = 'serverless' | 'long-lived';

export interface SessionInfo {
	sessionKey: string;
	displayName?: string;
	signedIn?: boolean;
	taskQueue: string;
	namespace: string;
	temporalUiUrl: string;
	workerMode?: WorkerMode;
	/** Rate limits shared by every run, configured for the deployment (RATE_LIMITS). */
	rateLimits?: { name: string; count: number; per: 'second' | 'minute' }[];
	/** The business connectors' specs, for their step forms. */
	connectors?: ConnectorSpec[];
}

export interface WorkerStatus {
	mode?: WorkerMode;
	/** Serverless: a worker is running right now. None running is normal. */
	available: boolean;
	workers: number;
	source: 'heartbeat' | 'pollers' | string;
	error?: string;
}

export interface FlowSummary {
	id: string;
	name: string;
	description?: string;
	version: number;
	updatedAt: string;
	triggerKind: string;
	nodeCount: number;
	scheduleActive: boolean;
	keyed?: boolean;
	/** A built-in example; customized: the user saved their own copy of it. */
	builtIn?: boolean;
	customized?: boolean;
}

export interface FlowDetail {
	flow: Definition;
	webhookUrl: string;
	scheduleActive: boolean;
	builtIn?: boolean;
	customized?: boolean;
}

export interface FlowVersion {
	version: number;
	updatedAt: string;
	nodeCount: number;
}

export interface RunSummary {
	workflowId: string;
	flowId?: string;
	flowName?: string;
	flowVersion?: number;
	source?: string;
	key?: string;
	status: ExecutionStatus;
	startTime: string;
	closeTime?: string | null;
}

export interface PendingActivity {
	nodeId: string;
	/** Set when the step is running for one item of a For each body. */
	item?: number;
	activityType: string;
	attempt: number;
	maxAttempts: number;
	state: string;
	lastFailure?: string;
	nextAttemptTime?: string;
	/** Set once the running attempt has heartbeated. */
	lastHeartbeatTime?: string;
	heartbeat?: { phase: string; seconds: number };
}

export interface RunView {
	workflowId: string;
	temporalUrl: string;
	execution: {
		status: ExecutionStatus;
		startTime: string;
		closeTime?: string | null;
		historyLength: number;
		/** Set on a delayed start: when the run begins working. */
		executionTime?: string;
	};
	flow: Definition;
	/** <callbackUrl>/events/<name>, /updates/<name>, /vars */
	callbackUrl: string;
	key?: string;
	/** manual, webhook, schedule, event, or parent */
	source?: string;
	progress: Progress | null;
	pendingActivities: PendingActivity[];
}

/** A message sent with start: Signal-With-Start or Update-With-Start to the key's run. */
export interface WithStart {
	kind: 'signal' | 'update';
	name: string;
}

/** POST /api/flows/{id}/runs. A flow that responds early, or an Update-With-Start, also returns its Respond value. */
export interface StartResult {
	workflowId: string;
	response?: unknown;
	responded?: boolean;
	responseError?: string;
}

/** One entry in the design pattern library (internal/patterns). */
export interface Pattern {
	id: string;
	name: string;
	category: string;
	summary: string;
	/** visual: draw it · built-in: every flow gets it · not-in-builder: a worker or deployment concern */
	support: 'visual' | 'built-in' | 'not-in-builder';
	useFor: string[];
	build: { node?: string; text: string }[];
	watch?: string[];
	tryIt?: string;
	docsUrl: string;
	flow?: Definition;
	children?: Definition[];
	related?: string[];
}

export interface ScheduleInfo {
	flowId: string;
	flowName: string;
	paused: boolean;
	note?: string;
	summary: string;
	nextRunTimes: string[];
	recentRuns: { workflowId: string; scheduledAt: string }[];
	remainingActions?: number;
}

export interface Template {
	id: string;
	name: string;
	description?: string;
	author?: string;
	builtIn: boolean;
	listed: boolean;
	mine: boolean;
	publishedAt?: string;
	nodeCount: number;
	flow: Definition;
}

/** The copilot's conversation for a flow (internal/flow/copilot.go). */
export interface CopilotState {
	status: 'idle' | 'thinking' | 'editing';
	entries: CopilotEntry[];
	pending: CopilotCall[];
	queued?: number;
}

export interface CopilotEntry {
	seq: number;
	kind: 'user' | 'assistant' | 'tool' | 'error';
	text?: string;
	messageId?: string;
	tool?: string;
	callId?: string;
	input?: unknown;
	status?: 'running' | 'done' | 'failed';
	at: string;
}

export interface CopilotCall {
	id: string;
	name: string;
	input: Record<string, unknown>;
	clientId: string;
}
