# Runbook: Visual Workflows demo

This runbook is for presenting the demo. The [README](README.md) explains what it is and how it's built.

## What it shows

A no-code builder for business processes: you draw a flow, and every run of it is a durable Temporal Workflow. Orders, payments, refunds and bookings call flaky outside systems, wait minutes or days for callbacks and people, and sometimes have to be unwound halfway.

The demo makes three problems visible:

- **Outside systems fail, and so do your own servers.** The payment gateway fails three times before it answers, and you can take the workers away mid-run. The run picks up at the same step. Beats 1 and 2 show it.
- **A process that waits has to accept changes while it waits.** A held order takes a new address, a longer hold and a cancellation while its timer runs, and rejects a change that breaks its rules. Beat 3 shows it.
- **A process that fails halfway has to undo what it already did.** The card declines after the flight and hotel are booked, and both bookings are cancelled in reverse order. Beat 4 shows it.

## Before you start

- **On tmprl-demo.cloud:** open **Visual Workflows** from the catalog and sign in. The app, a flow Worker, and a sandbox that stands in for the store, the payment gateway and the carriers all run in the cluster. Beat 2 needs a crashable environment; see [Start it](#start-it).
- **Locally:** install Go 1.27, Node 24 and the [Temporal CLI](https://docs.temporal.io/cli). The optional AI beat needs `ANTHROPIC_API_KEY` exported in the terminal that runs `make dev`; nothing else needs a key.
- **Against your own Temporal Cloud Namespace:** set `TEMPORAL_ADDRESS`, `TEMPORAL_NAMESPACE` and `TEMPORAL_API_KEY` for every process. They ignore your CLI profile. The flow Worker uses Worker Versioning; start it with `WORKER_SET_CURRENT_VERSION=true` so its build becomes the current version, as `make dev` does locally.

Each browser is its own user. Your flows and runs are scoped to your sign-in on tmprl-demo.cloud, or to a session cookie locally, and every user starts with the ten example flows.

## Start it

### On tmprl-demo.cloud

1. Open the demo from the catalog. A healthy start shows the **Overview** page and a green **Worker online** pill.
2. For beat 2, open the demo's drawer in the catalog and press **Create crashable environment** on the **Crashable environment** card. When it shows **Running**, press **Open environment**. Present every beat from that environment, so you can crash its Workers without touching anyone else's.

The environment is a private copy of the demo's Workers on its own Task Queue. Your flows there start fresh from the examples, because each user's library is keyed by the Task Queue. It auto-deletes after an hour; **Extend +1h** adds time.

### Locally

From the repository root:

```sh
cd web && npm install && cd ..
make dev
```

Open http://localhost:5173. A healthy start shows the **Overview** page and a green **Worker online** pill. The Temporal UI is at http://localhost:8233, and logs go to `.logs/` (`tail -f .logs/worker.log`).

`make dev` reuses any Temporal server already listening on port 7233, and the UI sends API calls to whatever listens on port 8080. Stop other demos first.

## The walkthrough

Every beat starts from **Workflows** in the left rail. Click a flow's row to open it in **Build**, then press **Run** and **Start workflow**; the sample input is filled in. The view switches to **Runs** with the new run open. Click any step to see its details and actions in the inspector on the right. `[` and `]` hide and show the side panels.

### 1. A flaky payment gateway fails three times, and the run carries on

1. Open **Resilient payment capture**. Click **Charge card** and show its retry policy in the inspector: 6 attempts, 2 seconds apart to start, backoff 1.5.
2. Press **Run**, then **Start workflow**.
3. **Charge card** turns pink and reads **Attempt 2 of 6**, then 3, then 4. Click it: the inspector shows each **Last failure** (`503 Service Unavailable: payment gateway unavailable`) and when Temporal will retry.
4. The fourth attempt succeeds. **Wait for settlement** waits until the bank's callback arrives as a `payment_settled` Signal, about 8 seconds later, and the run completes. On the dev server the whole run takes about 20 seconds.
5. Say: "The gateway failed three times. Nobody wrote a retry loop: the step has a retry policy, and Temporal schedules each attempt and records it. The bank's callback came in as a Signal, and the run was waiting for it without holding a server."
6. For proof, click **Open in Temporal UI**. The history has one `ActivityTaskStarted` for the charge with attempt 4, and the `WorkflowExecutionSignaled` event for `payment_settled`.

### 2. Take the Workers away mid-run

On tmprl-demo.cloud, from the crashable environment:

1. In the environment, start **Resilient payment capture** again.
2. Right away, in the catalog's **Crashable environment** card, press **Scale down workers**. The run takes about 20 seconds, so it may finish before the Workers are gone; step 4 covers that case. The card shows **Workers scaled to zero**.
3. Back in the demo, the pill turns pink and reads **No worker — runs are paused, progress is safe**, and the run shows "No worker is polling. Temporal has preserved this run's progress; it resumes when a worker returns." The canvas still shows which steps finished.
4. Start one more run while no Worker is up. It's accepted and waits at its first step.
5. Say: "There's no Worker running this flow now. The run's state isn't in a Worker; it's in Temporal. The page still shows its progress, and a callback sent to it now, like the bank's, is still recorded."
6. Press **Scale up workers**. When a Worker is back, the pill turns green and both runs carry on from the step they stopped at.

The app spots a missing Worker from Worker heartbeats, which arrive every 5 seconds and count as stale after 15; on the dev server the pill changed within about 15 seconds. Where Worker heartbeats aren't reported, it falls back to the Task Queue's pollers and can take over a minute.

**Crash workers** kills the Worker pods instead and lets Kubernetes replace them. It's the same point, but the outage can be over before the pill notices.

Locally, run `make crash-worker` in another terminal while **Charge card** is retrying, then `make worker` to bring a Worker back. `make crash-worker` runs `pkill -9 -f 'bin/worker|go-build.*/worker'`, which kills any process on your machine whose command line matches, including another demo's Worker.

### 3. Change an order while it waits

1. Open **Editable order** and run it. The run view shows the main path (**Hold window** racing **Cancel requested?**) and three lanes beside it: **On change_address**, **On cancel_order** and **On extend_hold**.
2. **Hold window** reads "Durable timer — resumes at" a time two minutes away. The **Variables** panel shows `address`, `hold_minutes` and `cancelled`.
3. Click **On change_address**. Under **Send an event**, set **Payload (JSON)** to `{"address": "9 Elm St, Seattle"}` and press **Send signal**. `address` changes in **Variables**.
4. Click **On extend_hold**. Set **Request (JSON)** to `{"minutes": 5}` and press **Send update**. The card shows **Result** `{"address": "9 Elm St, Seattle", "holdMinutes": 7}`, and the **Hold window** timer moves five minutes later.
5. Send it again with `{"minutes": 90}`. The card shows **Not accepted**: the validator `request.minutes > 0 && request.minutes <= 60 && !vars.cancelled` rejected it.
6. Say: "A Signal is fire-and-forget: the address change is recorded and the run applies it. An Update is a request with an answer: the caller gets the new hold length back, and a request that breaks the rules is turned away before it touches the run."
7. Click **On cancel_order** and press **Send signal** with any payload. **Cancel requested?** wins the race, **Hold window** is marked skipped ("lost the race to Cancel requested?"), and **Confirm cancellation** runs.
8. For proof, open the run in the Temporal UI. The history has `WorkflowExecutionSignaled` events and an accepted and completed Update for `extend_hold`, and no event for the rejected one.

### 4. The card declines after the trip is booked, and the bookings are undone

1. Open **Trip booking (saga)**. **Book flight** and **Book hotel** each carry an undo request, a call to the booking's cancel endpoint.
2. Run it. **Book flight** and **Book hotel** finish, then **Charge card** fails with `402 Payment Required: card declined`. A decline isn't retried.
3. The banner reads "Compensating… a later step failed, so completed steps are being undone in reverse order", then "Compensation finished: completed steps were undone." The hotel is cancelled first, then the flight. Click either step to see **Undo request done**.
4. Say: "The card declined after we'd booked the flight and the hotel. Each step said how to undo itself, and the run undid them newest first. If the Worker had died halfway through the undo, it would have resumed there too."

### 5. Optional: an AI agent as durable steps

This beat calls the Claude API. On tmprl-demo.cloud the key is configured; locally it needs `ANTHROPIC_API_KEY`.

1. Open **AI support triage** and run it.
2. Click **Triage agent**. The **Agent trace · ReAct** panel shows each model turn and tool call as it happens. If the agent decides on a refund over $150, it asks first: **The agent is asking** appears, and you reply with **Answer**.
3. Say: "Each model call and each tool call is an Activity with its own retries. If the Worker dies mid-conversation, the agent picks up at the turn it was on, and it can wait for a person for as long as it takes."
4. Click **Open child run** to show that the agent is its own child Workflow.

What the agent decides varies from run to run, so the refund question may not come up.

## Show it in code

All links point at commit [`e0efa24`](https://github.com/temporal-sa/temporal-demo-visual-workflows/commit/e0efa24dd6b5105ccdefca94d3fc4a54a15e32bc). Open a file locally with `code -g <path>:<line>`.

`FlowWorkflow` is an interpreter: a run gets the flow as input and turns each step into a Temporal call. The entries below are the lines behind each beat. A customer writing their own Workflows would write the same calls directly, without the interpreter.

### Give each step a retry policy

**When to use it:** after beat 1, or when someone asks where the retry loop is.

**Where it is:** [`internal/workflows/options.go` lines 21–40](https://github.com/temporal-sa/temporal-demo-visual-workflows/blob/e0efa24dd6b5105ccdefca94d3fc4a54a15e32bc/internal/workflows/options.go#L21-L40) (`code -g internal/workflows/options.go:21`). The charge step's policy is in [`internal/examples/examples.go` lines 55–61](https://github.com/temporal-sa/temporal-demo-visual-workflows/blob/e0efa24dd6b5105ccdefca94d3fc4a54a15e32bc/internal/examples/examples.go#L55-L61).

```go
// activityOptions turns the builder's settings into Temporal Activity
// options. Retries come from the retry policy, not from a loop in the
// Workflow: Temporal schedules each attempt and records it.
func activityOptions(activityID string, o flow.ActivityOptions) workflow.ActivityOptions {
	return workflow.ActivityOptions{
		ActivityID:             activityID,
		StartToCloseTimeout:    time.Duration(o.StartToCloseSeconds) * time.Second,
		ScheduleToCloseTimeout: time.Duration(o.ScheduleToCloseSeconds) * time.Second,
		// Every Activity here heartbeats while it waits (see keepAlive), so a
		// lost attempt is retried within seconds and a cancelled one stops.
		HeartbeatTimeout: HeartbeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        seconds(o.Retry.InitialIntervalSeconds),
			BackoffCoefficient:     o.Retry.BackoffCoefficient,
			MaximumInterval:        seconds(o.Retry.MaximumIntervalSeconds),
			MaximumAttempts:        int32(o.Retry.MaxAttempts),
			NonRetryableErrorTypes: o.Retry.NonRetryableErrorTypes,
		},
	}
}
```

```go
n("charge", flow.TypeHTTP, "charge", "Charge card", 300, 120, flow.HTTPConfig{
	Method: "POST", URL: s + "/flaky/{{ run.id }}?failures=3",
	// ...
	Activity: flow.ActivityOptions{Retry: flow.RetryConfig{
		MaxAttempts: 6, InitialIntervalSeconds: 2, BackoffCoefficient: 1.5, MaximumIntervalSeconds: 10,
	}},
}),
```

**What you need to understand:**

- Each step's settings become a `RetryPolicy` on the Activity. The Workflow calls the Activity once; the Temporal Service schedules every retry, waits the backoff, and records the attempt count. That's why the run view can show **Attempt 4 of 6** without the Workflow knowing.
- The charge step waits 2 s, 3 s and 4.5 s between attempts (backoff 1.5, capped at 10 s) and gives up after 6. A step that sets nothing inherits the flow's defaults, and the flow inherits 5 attempts starting 1 s apart with backoff 2 and a 30-second timeout per attempt.
- When the attempts run out, the step fails. If it has an error edge, the flow takes that branch instead, as **Alert ops** does here.
- The customer doesn't write a loop, a sleep, or a counter in a database. A Worker that dies between attempts doesn't lose the count.

**Docs:** [Retry Policies](https://docs.temporal.io/encyclopedia/retry-policies), [Activity timeouts](https://docs.temporal.io/encyclopedia/detecting-activity-failures)

### Let the failure decide whether to retry

**When to use it:** during beat 4, when the card decline isn't retried, or when someone asks how Temporal knows not to retry a bad request.

**Where it is:** [`internal/workflows/activities.go` lines 153–169](https://github.com/temporal-sa/temporal-demo-visual-workflows/blob/e0efa24dd6b5105ccdefca94d3fc4a54a15e32bc/internal/workflows/activities.go#L153-L169) (`code -g internal/workflows/activities.go:153`)

```go
// Most 4xx responses mean the request itself is wrong, so retrying would
// only repeat the same answer. 408 and 429 are the exceptions: the server
// is asking us to come back later.
if slices.Contains(req.NonRetryableStatus, resp.StatusCode) {
	return out, temporal.NewNonRetryableApplicationError(msg, "HTTPStatusNotRetried", nil, out)
}
if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != 408 && resp.StatusCode != 429 {
	return out, temporal.NewNonRetryableApplicationError(msg, "HTTPClientError", nil, out)
}
// A server that says when to come back (Retry-After on a 429 or 503)
// sets the next retry's delay, overriding the policy's backoff. That is
// the delayed retry pattern: the failure decides when to try again.
if delay, ok := retryAfter(resp.Header.Get("Retry-After"), time.Now()); ok {
	return out, temporal.NewApplicationErrorWithOptions(msg+fmt.Sprintf(" (retrying in %s, as asked)", delay), "HTTPServerError",
		temporal.ApplicationErrorOptions{NextRetryDelay: delay, Details: []any{out}})
}
return out, temporal.NewApplicationError(msg, "HTTPServerError", out)
```

**What you need to understand:**

- A non-retryable `ApplicationError` ends the retries at once, whatever the policy says. The card decline is a 402, so beat 4 fails on the first attempt and compensation starts straight away.
- A 503 returns an ordinary `ApplicationError`, so the retry policy applies. That's the gateway in beat 1.
- `NextRetryDelay` overrides the policy's backoff for the next attempt only. Here a `Retry-After` header sets it, capped at 10 minutes in `retryAfter`.
- Each step can also list extra HTTP statuses or error types not to retry, under **Don't retry these HTTP statuses** and **Don't retry these error types** in its settings. Those become `NonRetryableStatus` and the policy's `NonRetryableErrorTypes`.

**Docs:** [Failure handling: non-retryable errors](https://docs.temporal.io/encyclopedia/retry-policies#non-retryable-errors), [Go: error handling](https://docs.temporal.io/develop/go/best-practices/error-handling)

### Notice a lost Worker in seconds, and stop cancelled calls

**When to use it:** during beat 2, or when someone asks how long Temporal waits before it retries a step whose Worker died.

**Where it is:** [`internal/workflows/activities.go` lines 55–60](https://github.com/temporal-sa/temporal-demo-visual-workflows/blob/e0efa24dd6b5105ccdefca94d3fc4a54a15e32bc/internal/workflows/activities.go#L55-L60) (`code -g internal/workflows/activities.go:55`) and [lines 129–136](https://github.com/temporal-sa/temporal-demo-visual-workflows/blob/e0efa24dd6b5105ccdefca94d3fc4a54a15e32bc/internal/workflows/activities.go#L129-L136)

```go
// HeartbeatTimeout is how long Temporal waits for a heartbeat before it treats
// the attempt as lost (its worker crashed, or the Lambda invocation ended) and
// retries it. Without heartbeats that takes the whole StartToClose timeout.
// It also bounds how quickly a cancelled attempt finds out (see keepAlive).
// A variable only so tests can shorten it.
var HeartbeatTimeout = 10 * time.Second
```

```go
defer keepAlive(ctx, "waiting for "+method+" "+truncate(req.URL, 80))()
resp, err := a.HTTP.Do(httpReq)
if err != nil {
	if ctx.Err() != nil {
		activity.GetLogger(ctx).Info("http request abandoned: the step was cancelled", "method", method, "url", req.URL)
		return HTTPResponse{}, ctx.Err()
	}
	return HTTPResponse{}, err // network errors are worth retrying
```

**What you need to understand:**

- Every HTTP and Claude call heartbeats every 2 seconds while it waits. If the Worker dies mid-call, the heartbeats stop, and after 10 seconds Temporal treats the attempt as failed and retries it on another Worker. Without a heartbeat timeout it would wait out the whole attempt timeout.
- The SDK sends at most one heartbeat to the server per 80% of the heartbeat timeout, with the latest details. The run view shows the latest one as **heartbeat … ago** on the running step.
- A heartbeat is also how a cancellation reaches a running Activity. When **Fastest carrier quote** picks Swift, the Workflow cancels the other branches; the next heartbeat returns the cancellation, the SDK cancels `ctx`, and the HTTP request to Parcel Post is aborted. The sandbox logs "quote request abandoned by the caller".
- If the Worker dies while a step is between attempts, as in the local beat 2, the attempt simply waits in the Task Queue until a Worker polls.

**Docs:** [Activity heartbeats](https://docs.temporal.io/encyclopedia/detecting-activity-failures#activity-heartbeat), [Go: Activity heartbeats](https://docs.temporal.io/develop/go/activities/timeouts#activity-heartbeats)

### Turn a callback into a Signal

**When to use it:** after beat 1 or 2, when someone asks how the bank's callback reached a run that had no Worker.

**Where it is:** [`internal/app/hooks.go` lines 78–103](https://github.com/temporal-sa/temporal-demo-visual-workflows/blob/e0efa24dd6b5105ccdefca94d3fc4a54a15e32bc/internal/app/hooks.go#L78-L103) (`code -g internal/app/hooks.go:78`)

```go
// hookEvent is a run's callback URL: an outside system POSTs an event and it
// becomes a Signal of that name on the run.
func (s *Server) hookEvent(w http.ResponseWriter, r *http.Request) {
	wid, ok := s.hookRun(w, r)
	// ...
	// Recorded in the run's history even if no worker is running; the run
	// reacts as soon as one runs it.
	if err := s.tc.SignalWorkflow(r.Context(), wid, "", name, payload); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	s.log.Info("event delivered", "workflow_id", wid, "signal", name)
	w.WriteHeader(http.StatusNoContent)
}
```

**What you need to understand:**

- Every run has a callback URL. The flow hands it to the outside system (the charge step sends `{{ run.callbackUrl }}/events/payment_settled`), and a POST to it becomes a Signal on that run. Nothing polls.
- The Temporal Service writes the Signal to the run's history when it arrives, whether or not a Worker is running. The run handles it when a Worker next runs it.
- The Workflow records each event as it arrives, so a **Wait for signal** step also sees an event that came before the step started.
- On tmprl-demo.cloud the callback URL shown in the run view uses the app's in-cluster address. The sandbox can reach it; your laptop can't. Use **Send signal** in the inspector instead.

**Docs:** [Signals](https://docs.temporal.io/sending-messages#sending-signals), [Go: send a Signal](https://docs.temporal.io/develop/go/workflows/message-passing#send-signal-from-client)

### Change a running Workflow with an Update, and reject bad requests

**When to use it:** during beat 3, after the `{"minutes": 90}` request is turned away.

**Where it is:** [`internal/workflows/flow.go` lines 437–480](https://github.com/temporal-sa/temporal-demo-visual-workflows/blob/e0efa24dd6b5105ccdefca94d3fc4a54a15e32bc/internal/workflows/flow.go#L437-L480) (`code -g internal/workflows/flow.go:437`)

```go
// registerUpdateLane makes an On update lane callable. The validator runs
// before anything is written to history: a rejected Update leaves no trace,
// and the caller gets the reason at once. An accepted one runs the lane and
// returns its Respond step's value to the caller.
func (r *flowRun) registerUpdateLane(ctx workflow.Context, lane flow.Node) error {
	// ...
	return workflow.SetUpdateHandlerWithOptions(ctx, c.UpdateName,
		func(ctx workflow.Context, request any) (any, error) {
			// ...
			if err := r.runPath(ctx, lane, request); err != nil {
				return nil, temporal.NewApplicationError(describeError(err), "LaneFailed")
			}
			return r.responses[lane.ID], nil
		},
		workflow.UpdateHandlerOptions{Validator: func(request any) error {
			if r.closing || r.ending || r.initErr != nil {
				return fmt.Errorf("this run is finishing and no longer accepts %s", c.UpdateName)
			}
			// ...
			env := r.env()
			env["request"] = request
			v, err := flow.Eval(c.Validator, env)
			if err != nil || !flow.Truthy(v) {
				return fmt.Errorf("rejected: %s is not true for this request", c.Validator)
			}
			return nil
		}},
	)
}
```

**What you need to understand:**

- An Update is a synchronous request to a running Workflow: the caller waits and gets a result back. Here the result is the **Respond** step's value, the new hold length.
- The validator runs first and can only read state. When it returns an error, the Update is rejected, no `WorkflowExecutionUpdateAccepted` event is written, and the caller gets the reason. A Signal can't be rejected; it's always recorded.
- An Update needs a Worker to run its validator and handler. With no Worker up, the API waits 30 seconds and returns "no worker answered in time; try again". A Signal doesn't wait.
- The approval buttons in **Refund approval** use the same mechanism. Their validator rejects a decision for a step that isn't waiting, so a double click can't approve twice.

**Docs:** [Updates](https://docs.temporal.io/sending-messages#sending-updates), [Go: Update handlers and validators](https://docs.temporal.io/develop/go/workflows/message-passing#updates)

### A timer that moves when the data changes

**When to use it:** during beat 3, when the hold timer moves after the Update.

**Where it is:** [`internal/workflows/nodes.go` lines 196–220](https://github.com/temporal-sa/temporal-demo-visual-workflows/blob/e0efa24dd6b5105ccdefca94d3fc4a54a15e32bc/internal/workflows/nodes.go#L196-L220) (`code -g internal/workflows/nodes.go:196`)

```go
// An expression timer recalculates whenever a variable changes, so a
// lane can move the deadline while the timer is running.
for {
	v, err := flow.Eval(c.Expression, env)
	// ...
	target, err := flow.TimerTarget(v, start)
	// ...
	remaining := target.Sub(workflow.Now(ctx))
	if remaining <= 0 {
		break
	}
	r.wait(node.ID, flow.Waiting{Kind: "timer", Until: &target, Details: c.Expression})
	seen := r.varsVersion
	changed, err := workflow.AwaitWithTimeout(ctx, remaining, func() bool { return r.varsVersion != seen })
	if err != nil {
		return "", nil, err
	}
	if !changed {
		break
	}
}
```

**What you need to understand:**

- `workflow.AwaitWithTimeout` starts a durable timer and also wakes when the condition becomes true. When a lane sets a variable, the loop recalculates the deadline from the original start time and waits again.
- The timer lives in the Temporal Service. It fires on time even when no Worker is running; the Workflow reacts when a Worker picks it up.
- `workflow.Now` is the Workflow's deterministic clock, so a replay computes the same deadline.

**Docs:** [Durable timers](https://docs.temporal.io/workflow-execution/timers-delays), [Go: AwaitWithTimeout](https://pkg.go.dev/go.temporal.io/sdk/workflow#AwaitWithTimeout)

### Undo completed steps in reverse order

**When to use it:** during beat 4.

**Where it is:** the undo is recorded as each step succeeds in [`internal/workflows/nodes.go` lines 88–100](https://github.com/temporal-sa/temporal-demo-visual-workflows/blob/e0efa24dd6b5105ccdefca94d3fc4a54a15e32bc/internal/workflows/nodes.go#L88-L100) (`code -g internal/workflows/nodes.go:88`) and run on failure in [`internal/workflows/flow.go` lines 509–538](https://github.com/temporal-sa/temporal-demo-visual-workflows/blob/e0efa24dd6b5105ccdefca94d3fc4a54a15e32bc/internal/workflows/flow.go#L509-L538) (`code -g internal/workflows/flow.go:509`)

```go
if c.Undo != nil {
	// Render the undo now, while this step's output is at hand, so it
	// can refer to it: {{ steps.charge.body.transactionId }}.
	// ...
	r.undos = append(r.undos, undoStep{NodeID: node.ID, Request: undo, Options: opts})
}
```

```go
// compensate runs the undo requests of completed steps, newest first. It
// uses a disconnected context so it also runs when the run was cancelled.
func (r *flowRun) compensate(ctx workflow.Context) {
	if len(r.undos) == 0 {
		return
	}
	ctx, _ = workflow.NewDisconnectedContext(ctx)
	var a *Activities
	r.state.Compensation = "running"
	r.dirty = true
	failed := false
	for i := len(r.undos) - 1; i >= 0; i-- {
		u := r.undos[i]
		r.set(u.NodeID, func(ns *flow.NodeState) { ns.Undo = "running" })
		err := workflow.ExecuteActivity(
			workflow.WithActivityOptions(ctx, activityOptions(u.NodeID+"-undo", u.Options)),
			a.HTTPRequest, u.Request,
		).Get(ctx, nil)
		// ...
	}
	// ...
}
```

**What you need to understand:**

- This is the saga pattern: a list of compensations grows as steps succeed, and a failure runs it backwards. In a hand-written Workflow it's a slice and a `defer`.
- Each undo is an Activity with the same retry policy as its step, so a cancellation endpoint that's briefly down is retried.
- The undo request is rendered when the step succeeds, so it can use the step's output, such as the booking ID.
- `NewDisconnectedContext` lets compensation run even when the run itself was cancelled.
- The undo list is Workflow state. If the Worker dies during compensation, the next Worker replays the history and carries on with the next undo.

**Docs:** [Saga pattern](https://docs.temporal.io/design-patterns/saga-pattern), [Go: cancellation and disconnected contexts](https://docs.temporal.io/develop/go/workflows/cancellation)

### Watching a run never needs a Worker

**When to use it:** during beat 2, when the run view keeps showing progress with no Worker up.

**Where it is:** [`internal/workflows/flow.go` lines 569–582](https://github.com/temporal-sa/temporal-demo-visual-workflows/blob/e0efa24dd6b5105ccdefca94d3fc4a54a15e32bc/internal/workflows/flow.go#L569-L582) (`code -g internal/workflows/flow.go:569`)

```go
// publishProgress keeps a compact copy of the run's state in its memo. The
// API reads it with Describe, which needs no worker, so watching a run never
// wakes one. Changes made in the same Workflow Task are written once.
func (r *flowRun) publishProgress(ctx workflow.Context) {
	for {
		if err := workflow.Await(ctx, func() bool { return r.dirty }); err != nil {
			return
		}
		r.dirty = false
		if err := workflow.UpsertMemo(ctx, map[string]any{flow.MemoProgress: r.progress()}); err != nil {
			workflow.GetLogger(ctx).Warn("could not record progress", "error", err)
		}
	}
}
```

**What you need to understand:**

- A Query would need a Worker to answer it. The memo is stored by the Temporal Service, and `DescribeWorkflowExecution` returns it along with pending Activities and their attempt counts, so the run view polls Describe and works with every Worker down.
- The memo holds a trimmed progress record, because a memo has a size limit. Full step outputs come from a Query, which is why **Load step outputs** is a separate button.
- This is a choice for this UI, not something every Workflow needs. Many apps keep a read model elsewhere, or use Queries.

**Docs:** [Memo](https://docs.temporal.io/workflow-execution#memo), [Go: UpsertMemo](https://pkg.go.dev/go.temporal.io/sdk/workflow#UpsertMemo)

### Ship new Workflow code without breaking runs in flight

**When to use it:** when someone asks what happens to running flows when the builder's code changes. The demo doesn't roll out a new version live.

**Where it is:** [`internal/workflows/register.go` lines 21–30](https://github.com/temporal-sa/temporal-demo-visual-workflows/blob/e0efa24dd6b5105ccdefca94d3fc4a54a15e32bc/internal/workflows/register.go#L21-L30) (`code -g internal/workflows/register.go:21`) and [`internal/workflows/handoff.go` lines 125–132](https://github.com/temporal-sa/temporal-demo-visual-workflows/blob/e0efa24dd6b5105ccdefca94d3fc4a54a15e32bc/internal/workflows/handoff.go#L125-L132) (`code -g internal/workflows/handoff.go:125`)

```go
// Register adds the flow Workflows and their Activities. Both Workflows are
// Pinned: a run finishes on the worker build it started on, so a new build
// never replays an old run's history and no patching is needed. Keyed runs
// move to the newest build when they continue as new.
func Register(r Registry, acts *Activities) {
	pinned := workflow.RegisterOptions{VersioningBehavior: workflow.VersioningBehaviorPinned}
	r.RegisterWorkflowWithOptions(FlowWorkflow, pinned)
	r.RegisterWorkflowWithOptions(AgentWorkflow, pinned)
	r.RegisterActivity(acts)
}
```

```go
opts := workflow.ContinueAsNewErrorOptions{}
if workflow.GetInfo(ctx).GetTargetWorkerDeploymentVersionChanged() {
	opts.InitialVersioningBehavior = workflow.ContinueAsNewVersioningBehaviorAutoUpgrade
}
next := r.in
next.Resume = r.resumeState()
r.flush(ctx)
return workflow.NewContinueAsNewErrorWithOptions(ctx, opts, FlowWorkflow, next)
```

**What you need to understand:**

- Two kinds of change are handled separately. Editing a flow in the builder never affects a running run, because each run gets a snapshot of the flow as its input. Changing the interpreter's Go code is handled by Worker Versioning.
- The Worker declares a Worker Deployment and a build ID (`cmd/worker/main.go`). On tmprl-demo.cloud the platform sets both and makes a new build current when it rolls out. A Pinned run stays on the build it started on until it finishes; new runs start on the current build. There's no patching in the Workflow code.
- A long-lived run, such as **Customer loyalty (entity)**, continues as new when Temporal suggests it. If a newer build has become current, it asks for `AutoUpgrade` on that Continue-As-New, so the next run starts on the new build with its state carried over. Without that option, a Pinned run's next run stays on the same build.
- The Continue-As-New upgrade option is in Public Preview and marked experimental in the SDK.

**Docs:** [Worker Versioning](https://docs.temporal.io/production-deployment/worker-deployments/worker-versioning), [Upgrade on Continue-as-New](https://docs.temporal.io/production-deployment/worker-deployments/worker-versioning/upgrade-on-continue-as-new)

### Run the agent as a child Workflow

**When to use it:** during the optional beat 5.

**Where it is:** [`internal/workflows/nodes.go` lines 353–368](https://github.com/temporal-sa/temporal-demo-visual-workflows/blob/e0efa24dd6b5105ccdefca94d3fc4a54a15e32bc/internal/workflows/nodes.go#L353-L368) (`code -g internal/workflows/nodes.go:353`). Each model turn is scheduled in `AgentWorkflow`, [`internal/workflows/agent.go` lines 123–129](https://github.com/temporal-sa/temporal-demo-visual-workflows/blob/e0efa24dd6b5105ccdefca94d3fc4a54a15e32bc/internal/workflows/agent.go#L123-L129).

```go
// The agent runs as a child Workflow: its loop gets its own history,
// its own Query and Update for the trace and human questions, and it
// can be inspected on its own in the Temporal UI.
childID := r.childID("agent", node, lane, "")
r.set(node.ID, func(ns *flow.NodeState) { ns.ChildWorkflowID = childID })
ctx = workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{WorkflowID: childID})
var res AgentResult
err = workflow.ExecuteChildWorkflow(ctx, AgentWorkflow, AgentInput{
	Instructions:  instructions,
	Tools:         c.Tools,
	AllowAskHuman: c.AllowAskHuman,
	MaxTurns:      c.MaxTurns,
	Context:       map[string]any{"input": env["input"], "steps": env["steps"], "vars": r.vars},
	ModelOptions:  c.ModelActivity.Resolve(agentModelDefaults),
	ToolOptions:   c.ToolActivity.Resolve(flow.ActivityOptions{Retry: flow.RetryConfig{MaxAttempts: 3}}.Resolve(r.defaults)),
}).Get(ctx, &res)
```

**What you need to understand:**

- Each model turn is a `CallClaude` Activity and each tool call is an `HTTPRequest` Activity, so a crash mid-conversation resumes at the turn it was on, and finished turns aren't paid for twice.
- Model calls default to 5 attempts and a 300-second timeout per attempt. The Anthropic client is built with its own retries turned off (`register.go`), so every retry is a Temporal retry you can see.
- When the agent asks a person, the child Workflow waits on an Update for the answer. The wait holds no Worker.
- A tool call that still fails after its retries goes back to the model as an error result, so the agent can adapt instead of failing.

**Docs:** [Child Workflows](https://docs.temporal.io/child-workflows), [AI Cookbook](https://docs.temporal.io/ai/cookbook)

### What's demo-only

When someone asks what they'd change for their own system, these are the parts to leave out:

- **The outside world is simulated.** [`cmd/sandbox/main.go` lines 1–11](https://github.com/temporal-sa/temporal-demo-visual-workflows/blob/e0efa24dd6b5105ccdefca94d3fc4a54a15e32bc/cmd/sandbox/main.go#L1-L11): the store, the payment gateway, the bookings and the carriers. The failures are chosen by the flow's URL: [`/flaky/{key}?failures=3`](https://github.com/temporal-sa/temporal-demo-visual-workflows/blob/e0efa24dd6b5105ccdefca94d3fc4a54a15e32bc/cmd/sandbox/main.go#L229-L254) fails three times per run, `/cards/charge?decline=true` always declines, and Budget Freight always answers 503. None of this is in the Workflow code.
- **Activity errors are logged as warnings.** [`internal/temporalx/temporalx.go` lines 86–108](https://github.com/temporal-sa/temporal-demo-visual-workflows/blob/e0efa24dd6b5105ccdefca94d3fc4a54a15e32bc/internal/temporalx/temporalx.go#L86-L108) rewrites the SDK's "Activity error." line so an expected retry doesn't show as a red ERROR on a shared screen.
- **Worker heartbeats every 5 seconds.** [`internal/temporalx/temporalx.go` lines 78–80](https://github.com/temporal-sa/temporal-demo-visual-workflows/blob/e0efa24dd6b5105ccdefca94d3fc4a54a15e32bc/internal/temporalx/temporalx.go#L78-L80) shortens the experimental Worker heartbeat from 60 seconds so the pill notices a killed Worker quickly. This isn't the Activity heartbeat.
- **The Worker can make its own build current.** [`cmd/worker/main.go` lines 35–39](https://github.com/temporal-sa/temporal-demo-visual-workflows/blob/e0efa24dd6b5105ccdefca94d3fc4a54a15e32bc/cmd/worker/main.go#L35-L39): a local convenience. In production the deployment pipeline sets the current version.
- **The interpreter.** A customer building one process would write the Workflow directly, with the same Activity options, Signal and Update handlers, timers and compensation list shown above.

## Reset and tear down

- **Between runs:** nothing carries over from one run to the next. Each run is its own Workflow, so start another.
- **Running runs:** open a run and press **Cancel**, or filter **Executions** by **Running** to find them. **Customer loyalty (entity)** runs never finish on their own; send `close_account` or cancel them.
- **Schedules:** **Daily stock report** runs only when you switch its schedule on. Switch it off in the **Schedule** column on **Workflows**, or pause it on **Schedules**. A Schedule left on keeps starting runs every weekday.
- **Edited example flows:** use **Templates**, pick the same flow, and press **Use template** for a fresh copy.
- **Crashable environment:** press **Delete** on the card when you're done. If you scaled the Workers down, the environment's runs stay paused until you scale up or delete it. It deletes itself when its time runs out.
- **Locally:** press `Ctrl-C` in the `make dev` terminal. It stops the dev server, the sandbox, the app, the Worker and Vite. The dev server keeps nothing between starts.

## When something goes wrong

| What you see | Likely cause | Fix |
|---|---|---|
| The pill reads **No worker — runs are paused, progress is safe** and runs don't move | The flow Worker isn't running | Locally, `make worker`. In a crashable environment, **Scale up workers** |
| The pill reads **API unreachable** | The app process is down, or the Vite dev server can't reach it on port 8080 | Restart `make dev`; check that nothing else is on port 8080 |
| **Send update**, **Approve** or **Reject** says "no worker answered in time; try again" | An Update needs a Worker, and none answered within 30 seconds | Bring a Worker back, then send it again. Signals don't need one |
| Runs from another demo show up, or yours land on another server | `make dev` reused a Temporal server already on port 7233 | Stop the other server and restart `make dev` |
| **AI support triage** fails at **Triage agent** with an API key error | `ANTHROPIC_API_KEY` isn't set for the Worker | Export it and restart `make dev`, or skip beat 5 |
| A `curl` to the run's callback URL fails on tmprl-demo.cloud | The callback URL uses the app's in-cluster address | Use **Send signal** in the inspector |
| **Wait until shipped** or **Wait for settlement** times out | The sandbox restarted, and its pending callbacks are lost | Start a new run |
| The pill stays green after **Crash workers** | Kubernetes replaced the pods before the app noticed | Use **Scale down workers** for a visible outage |

## Keeping this runbook current

The code links point at a fixed commit. When a linked file changes, update its link, line numbers, and snippet together. Before an event, list the linked files that have changed:

```sh
grep -oE 'blob/[0-9a-f]{40}/[^#)]+' RUNBOOK.md | sort -u | while IFS=/ read -r _ sha file; do
  git diff --quiet "$sha" HEAD -- "$file" || echo "changed since ${sha:0:7}: $file"
done
```
