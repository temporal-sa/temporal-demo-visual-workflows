package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/workflows"
)

// runInput builds a run's input: the flow snapshot, the snapshots of every
// flow it runs as a child, and the callback base. The Workflow appends its own
// ID to the callback base, so scheduled runs (whose IDs the Schedule assigns)
// get a callback URL too.
func (s *Server) runInput(ctx context.Context, r *http.Request, u user, def flow.Definition, input any, source string) (flow.RunInput, error) {
	if input == nil {
		if sample := triggerOf(def).SampleInput; sample != "" {
			if err := json.Unmarshal([]byte(sample), &input); err != nil {
				return flow.RunInput{}, fmt.Errorf("sample input: %w", err)
			}
		}
	}
	in := flow.RunInput{
		Flow: def, Input: input, Source: source,
		CallbackBase: s.callbackBaseURL(r) + "/hooks/" + s.webhookToken(u.Key, def.ID),
		SharedLimits: s.limitNames(),
	}
	if problems := s.limitProblems(def); len(problems) > 0 {
		return in, errors.New(problems[0].Message)
	}
	if ids := def.SubflowIDs(); len(ids) > 0 {
		all, err := s.flows(ctx, u)
		if err != nil {
			return in, err
		}
		byID := map[string]flow.Definition{}
		for _, f := range all {
			byID[f.ID] = f
		}
		in.Subflows = map[string]flow.Definition{}
		for len(ids) > 0 {
			id := ids[0]
			ids = ids[1:]
			if _, done := in.Subflows[id]; done {
				continue
			}
			sub, ok := byID[id]
			if !ok {
				return in, fmt.Errorf("flow %q runs flow %q, which doesn't exist", def.Name, id)
			}
			if len(in.Subflows) >= 10 {
				return in, errors.New("a run can include at most 10 child flows")
			}
			in.Subflows[id] = sub
			ids = append(ids, sub.SubflowIDs()...)
		}
	}
	return in, nil
}

func runMemo(in flow.RunInput) map[string]any {
	return map[string]any{
		"flowId": in.Flow.ID, "flowName": in.Flow.Name, "flowVersion": in.Flow.Version,
		"source": in.Source, "key": in.Key,
	}
}

// started is what starting a run returns: its ID, and for a flow that
// responds early, the main path's Respond value.
type started struct {
	WorkflowID string `json:"workflowId"`
	Response   any    `json:"response,omitempty"`
	Responded  bool   `json:"responded,omitempty"`
}

// start begins a run. A keyed flow has one run per key: what starting it
// again does is up to its Workflow ID policies (by default, it reaches the
// existing run instead of creating a second one).
func (s *Server) start(ctx context.Context, r *http.Request, u user, def flow.Definition, input any, source string) (started, error) {
	in, err := s.runInput(ctx, r, u, def, input, source)
	if err != nil {
		return started{}, err
	}
	if def.Settings.KeyedBy != "" {
		if in.Key, err = keyOf(def, in.Input); err != nil {
			return started{}, err
		}
	}
	opts := s.startOptions(u, def, in)
	if d := triggerOf(def).StartDelay; d != nil {
		// Delayed start: the run exists now, with its ID and callback URL,
		// but its first Workflow Task waits for the delay.
		opts.StartDelay = d.DelayDuration()
	}
	if def.RespondsEarly() {
		return s.startAndWaitForResponse(ctx, opts, def, in)
	}
	// Starting needs only the Temporal service, not a worker: with no worker
	// running, the run is accepted and waits on the task queue.
	run, err := s.tc.ExecuteWorkflow(ctx, opts, workflows.FlowWorkflow, in)
	if err != nil {
		return started{}, err
	}
	s.log.Info("run started", "workflow_id", run.GetID(), "flow", def.ID, "version", def.Version, "source", source)
	return started{WorkflowID: run.GetID()}, nil
}

// startAndWaitForResponse is early return: one Update-With-Start call starts
// the run and waits for its main path to reach a Respond step. The caller
// gets that value in the same request, while the run carries on.
func (s *Server) startAndWaitForResponse(ctx context.Context, opts client.StartWorkflowOptions, def flow.Definition, in flow.RunInput) (started, error) {
	if opts.WorkflowIDConflictPolicy == enumspb.WORKFLOW_ID_CONFLICT_POLICY_UNSPECIFIED {
		opts.WorkflowIDConflictPolicy = enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	handle, err := s.tc.UpdateWithStartWorkflow(ctx, client.UpdateWithStartWorkflowOptions{
		StartWorkflowOperation: s.tc.NewWithStartWorkflowOperation(opts, workflows.FlowWorkflow, in),
		UpdateOptions: client.UpdateWorkflowOptions{
			UpdateName: flow.UpdateEarlyResponse, WaitForStage: client.WorkflowUpdateStageCompleted,
		},
	})
	if err != nil {
		return started{}, err
	}
	res := started{WorkflowID: opts.ID}
	s.log.Info("run started, waiting for its response", "workflow_id", opts.ID, "flow", def.ID, "version", def.Version, "source", in.Source)
	if err := handle.Get(ctx, &res.Response); err != nil {
		// The run started; it just didn't answer. Say so with its ID.
		return res, fmt.Errorf("run %s started but did not respond: %w", opts.ID, err)
	}
	res.Responded = true
	return res, nil
}

func (s *Server) startRun(w http.ResponseWriter, r *http.Request, u user) {
	var body struct {
		Input any `json:"input"`
		// With sends the input as a Signal or an Update with start, to the
		// run for its key: "signal" or "update", named by Message.
		With    string `json:"with,omitempty"`
		Message string `json:"message,omitempty"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	def, ok, err := s.findFlow(r.Context(), u, r.PathValue("id"))
	if err != nil || !ok {
		writeError(w, http.StatusNotFound, errors.New("flow not found"))
		return
	}
	switch body.With {
	case "":
	case "signal":
		wid, err := s.signalWithStart(r.Context(), r, u, def, "", body.Message, body.Input, "event")
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusOK, started{WorkflowID: wid})
		return
	case "update":
		res, err := s.updateWithStart(r.Context(), r, u, def, "", body.Message, body.Input, "update")
		if err != nil && res.WorkflowID == "" {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err != nil {
			// The run may have started without handling the Update: say so
			// with its ID, as an early response that didn't come does.
			writeJSON(w, http.StatusOK, map[string]any{"workflowId": res.WorkflowID, "responseError": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, res)
		return
	default:
		writeError(w, http.StatusBadRequest, errors.New(`with must be "signal" or "update"`))
		return
	}
	res, err := s.start(r.Context(), r, u, def, body.Input, "manual")
	if err != nil && res.WorkflowID == "" {
		writeError(w, startErrorStatus(err), err)
		return
	}
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"workflowId": res.WorkflowID, "responseError": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

type runSummary struct {
	WorkflowID  string     `json:"workflowId"`
	FlowID      string     `json:"flowId"`
	FlowName    string     `json:"flowName"`
	FlowVersion int        `json:"flowVersion"`
	Source      string     `json:"source"`
	Key         string     `json:"key,omitempty"`
	Status      string     `json:"status"`
	StartTime   time.Time  `json:"startTime"`
	CloseTime   *time.Time `json:"closeTime,omitempty"`
}

var allowedStatuses = []string{"Running", "Completed", "Failed", "Canceled", "Terminated", "TimedOut", "ContinuedAsNew"}

// runs lists the user's flow runs from Visibility. Child runs share their
// parent flow's ID prefix, so the flow filter uses the memo instead.
func (s *Server) runs(ctx context.Context, u user, flowID, status string, limit int) ([]runSummary, error) {
	query := fmt.Sprintf("WorkflowType = 'FlowWorkflow' AND WorkflowId STARTS_WITH '%s'", s.prefix(u.Key))
	if slices.Contains(allowedStatuses, status) {
		query += fmt.Sprintf(" AND ExecutionStatus = '%s'", status)
	}
	resp, err := s.tc.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{Query: query, PageSize: int32(limit)})
	if err != nil {
		return nil, err
	}
	dc := converter.GetDefaultDataConverter()
	out := []runSummary{}
	for _, e := range resp.GetExecutions() {
		rs := runSummary{
			WorkflowID: e.GetExecution().GetWorkflowId(),
			Status:     e.GetStatus().String(),
			StartTime:  e.GetStartTime().AsTime(),
		}
		if e.GetCloseTime() != nil {
			t := e.GetCloseTime().AsTime()
			rs.CloseTime = &t
		}
		fields := e.GetMemo().GetFields()
		_ = decodeField(dc, fields, "flowId", &rs.FlowID)
		_ = decodeField(dc, fields, "flowName", &rs.FlowName)
		_ = decodeField(dc, fields, "flowVersion", &rs.FlowVersion)
		_ = decodeField(dc, fields, "source", &rs.Source)
		_ = decodeField(dc, fields, "key", &rs.Key)
		if flowID != "" && rs.FlowID != flowID {
			continue
		}
		out = append(out, rs)
	}
	slices.SortFunc(out, func(a, b runSummary) int { return b.StartTime.Compare(a.StartTime) })
	return out, nil
}

func decodeField(dc converter.DataConverter, fields map[string]*commonpb.Payload, name string, v any) error {
	p, ok := fields[name]
	if !ok {
		return errors.New("missing")
	}
	return dc.FromPayload(p, v)
}

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request, u user) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	list, err := s.runs(r.Context(), u, r.URL.Query().Get("flowId"), r.URL.Query().Get("status"), limit)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) listFlowRuns(w http.ResponseWriter, r *http.Request, u user) {
	list, err := s.runs(r.Context(), u, r.PathValue("id"), "", 100)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	if len(list) > 25 {
		list = list[:25]
	}
	writeJSON(w, http.StatusOK, list)
}

// ownRun authorizes a Workflow ID against the user before anything touches it.
// Agent loops are child Workflows of a different type and are reached
// through their step instead; copilot conversations through their flow.
func (s *Server) ownRun(w http.ResponseWriter, r *http.Request, u user) (string, bool) {
	wid := r.PathValue("wid")
	if !strings.HasPrefix(wid, s.prefix(u.Key)) || strings.Contains(wid, "-agent-") || strings.HasSuffix(wid, copilotSuffix) {
		writeError(w, http.StatusNotFound, errors.New("run not found"))
		return "", false
	}
	return wid, true
}

// startInput reads a run's input from the first event in its history, which
// needs no worker.
func (s *Server) startInput(ctx context.Context, wid, runID string) (flow.RunInput, error) {
	cacheKey := wid + "/" + runID
	if v, ok := s.starts.Load(cacheKey); ok {
		return v.(flow.RunInput), nil
	}
	iter := s.tc.GetWorkflowHistory(ctx, wid, runID, false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	if !iter.HasNext() {
		return flow.RunInput{}, errors.New("run has no history")
	}
	event, err := iter.Next()
	if err != nil {
		return flow.RunInput{}, err
	}
	var in flow.RunInput
	payloads := event.GetWorkflowExecutionStartedEventAttributes().GetInput()
	if err := converter.GetDefaultDataConverter().FromPayloads(payloads, &in); err != nil {
		return in, err
	}
	in.Subflows, in.Resume = nil, nil // not needed by the run view, and the cache stays small
	s.starts.Store(cacheKey, in)
	return in, nil
}

// getRun is the run view. Everything comes from Temporal's service, never
// from a worker: the flow snapshot from the start event, progress from the
// memo the Workflow keeps up to date, retries from the pending Activities. On
// serverless workers, watching a run never wakes one.
func (s *Server) getRun(w http.ResponseWriter, r *http.Request, u user) {
	wid, ok := s.ownRun(w, r, u)
	if !ok {
		return
	}
	ctx := r.Context()
	desc, err := s.tc.DescribeWorkflowExecution(ctx, wid, "")
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	info := desc.GetWorkflowExecutionInfo()
	in, err := s.startInput(ctx, wid, info.GetExecution().GetRunId())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	execution := map[string]any{
		"status":        info.GetStatus().String(),
		"startTime":     info.GetStartTime().AsTime(),
		"historyLength": info.GetHistoryLength(),
	}
	// A delayed start does nothing until its execution time.
	if t := info.GetExecutionTime(); t != nil && t.AsTime().After(info.GetStartTime().AsTime().Add(time.Second)) {
		execution["executionTime"] = t.AsTime()
	}
	if info.GetCloseTime() != nil {
		execution["closeTime"] = info.GetCloseTime().AsTime()
	}
	var progress *flow.Progress
	var p flow.Progress
	if decodeField(converter.GetDefaultDataConverter(), info.GetMemo().GetFields(), flow.MemoProgress, &p) == nil {
		progress = &p
	}
	pending := []map[string]any{}
	for _, a := range desc.GetPendingActivities() {
		// A step running for one item of a For each has the item's index in
		// its Activity ID: "fetch#3".
		nodeID, item, inItem := strings.Cut(a.GetActivityId(), flow.ItemSeparator)
		pa := map[string]any{
			"nodeId":       nodeID,
			"activityType": a.GetActivityType().GetName(),
			"attempt":      a.GetAttempt(),
			"maxAttempts":  a.GetMaximumAttempts(),
			"state":        strings.TrimPrefix(a.GetState().String(), "Pending"),
		}
		if inItem {
			if i, err := strconv.Atoi(item); err == nil {
				pa["item"] = i
			}
		}
		if f := a.GetLastFailure(); f != nil {
			pa["lastFailure"] = f.GetMessage()
		}
		if t := a.GetNextAttemptScheduleTime(); t != nil {
			pa["nextAttemptTime"] = t.AsTime()
		}
		if t := a.GetLastHeartbeatTime(); t != nil {
			pa["lastHeartbeatTime"] = t.AsTime()
			var hb workflows.Heartbeat
			if converter.GetDefaultDataConverter().FromPayloads(a.GetHeartbeatDetails(), &hb) == nil {
				pa["heartbeat"] = hb
			}
		}
		pending = append(pending, pa)
	}
	callback := ""
	if in.CallbackBase != "" {
		callback = in.CallbackBase + "/runs/" + wid
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"workflowId":        wid,
		"temporalUrl":       fmt.Sprintf("%s/namespaces/%s/workflows/%s", s.cfg.TemporalUIURL, s.cfg.Namespace, wid),
		"execution":         execution,
		"flow":              in.Flow,
		"callbackUrl":       callback,
		"key":               in.Key,
		"source":            in.Source,
		"progress":          progress,
		"pendingActivities": pending,
	})
}

// getRunDetails returns the full state, including step outputs. A completed
// run's result is in history (no worker needed); otherwise the "state" Query
// asks a worker, which on serverless may start one.
func (s *Server) getRunDetails(w http.ResponseWriter, r *http.Request, u user) {
	wid, ok := s.ownRun(w, r, u)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	desc, err := s.tc.DescribeWorkflowExecution(ctx, wid, "")
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	var st flow.RunState
	if desc.GetWorkflowExecutionInfo().GetStatus() == enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED {
		err = s.tc.GetWorkflow(ctx, wid, "").Get(ctx, &st)
	} else {
		var v converter.EncodedValue
		if v, err = s.tc.QueryWorkflow(ctx, wid, "", flow.QueryState); err == nil {
			err = v.Get(&st)
		}
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) getRunVars(w http.ResponseWriter, r *http.Request, u user) {
	wid, ok := s.ownRun(w, r, u)
	if !ok {
		return
	}
	s.queryVars(w, r, wid)
}

func (s *Server) queryVars(w http.ResponseWriter, r *http.Request, wid string) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	v, err := s.tc.QueryWorkflow(ctx, wid, "", flow.QueryVars)
	var vars map[string]any
	if err == nil {
		err = v.Get(&vars)
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	writeJSON(w, http.StatusOK, vars)
}

func (s *Server) signalRun(w http.ResponseWriter, r *http.Request, u user) {
	wid, ok := s.ownRun(w, r, u)
	if !ok {
		return
	}
	var body struct {
		Name    string `json:"name"`
		Payload any    `json:"payload"`
	}
	if err := decodeBody(r, &body); err != nil || !namePattern.MatchString(body.Name) {
		writeError(w, http.StatusBadRequest, errors.New("signal needs a lower_snake_case name"))
		return
	}
	// Signals are recorded even with no worker running; the run picks them up
	// when a worker next runs it.
	if err := s.tc.SignalWorkflow(r.Context(), wid, "", body.Name, body.Payload); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) updateRun(w http.ResponseWriter, r *http.Request, u user) {
	wid, ok := s.ownRun(w, r, u)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if !namePattern.MatchString(name) || name == flow.UpdateApprove {
		writeError(w, http.StatusBadRequest, errors.New("unknown update"))
		return
	}
	var body struct {
		Payload any `json:"payload"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.update(w, r, wid, name, body.Payload)
}

func (s *Server) approve(w http.ResponseWriter, r *http.Request, u user) {
	wid, ok := s.ownRun(w, r, u)
	if !ok {
		return
	}
	var d flow.ApprovalDecision
	if err := decodeBody(r, &d); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	d.By = u.Name
	if d.By == "You" {
		d.By = "presenter"
	}
	s.update(w, r, wid, flow.UpdateApprove, d)
}

// fixStep resumes a step paused after its retries ran out: retry it, perhaps
// with a corrected URL or body, or let it fail.
func (s *Server) fixStep(w http.ResponseWriter, r *http.Request, u user) {
	wid, ok := s.ownRun(w, r, u)
	if !ok {
		return
	}
	var d flow.FixDecision
	if err := decodeBody(r, &d); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	d.By = u.Name
	s.update(w, r, wid, flow.UpdateFix, d)
}

func (s *Server) cancelRun(w http.ResponseWriter, r *http.Request, u user) {
	wid, ok := s.ownRun(w, r, u)
	if !ok {
		return
	}
	if err := s.tc.CancelWorkflow(r.Context(), wid, ""); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// agentID finds the child Workflow behind an agent step from the run's
// progress, which needs no worker.
func (s *Server) agentID(w http.ResponseWriter, r *http.Request, u user) (string, bool) {
	wid, ok := s.ownRun(w, r, u)
	if !ok {
		return "", false
	}
	desc, err := s.tc.DescribeWorkflowExecution(r.Context(), wid, "")
	var p flow.Progress
	if err == nil {
		err = decodeField(converter.GetDefaultDataConverter(), desc.GetWorkflowExecutionInfo().GetMemo().GetFields(), flow.MemoProgress, &p)
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return "", false
	}
	child := p.Nodes[r.PathValue("nodeId")].Child
	if child == "" {
		writeError(w, http.StatusNotFound, errors.New("agent has not started"))
		return "", false
	}
	return child, true
}

func (s *Server) getAgent(w http.ResponseWriter, r *http.Request, u user) {
	child, ok := s.agentID(w, r, u)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	v, err := s.tc.QueryWorkflow(ctx, child, "", flow.QueryAgent)
	var st flow.AgentState
	if err == nil {
		err = v.Get(&st)
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) answerAgent(w http.ResponseWriter, r *http.Request, u user) {
	child, ok := s.agentID(w, r, u)
	if !ok {
		return
	}
	var body struct {
		Answer string `json:"answer"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.update(w, r, child, flow.UpdateAnswer, body.Answer)
}
