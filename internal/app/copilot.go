package app

import (
	"context"
	"errors"
	"net/http"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/workflows"
)

// The copilot's conversation for a flow is a CopilotWorkflow, one per user
// and flow, on the flow worker's task queue (it has the Anthropic key). Its ID
// is derived here from the user, never taken from the request.

// copilotSuffix ends a copilot's Workflow ID. Flow IDs can't contain ".", so
// no run of a flow ends this way.
const copilotSuffix = ".copilot"

func (s *Server) copilotID(u user, flowID string) string {
	return s.prefix(u.Key) + flowID + copilotSuffix
}

// getCopilot returns the conversation, or an empty one when none is running.
func (s *Server) getCopilot(w http.ResponseWriter, r *http.Request, u user) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	empty := flow.CopilotState{Status: "idle", Entries: []flow.CopilotEntry{}, Pending: []flow.CopilotCall{}}
	resp, err := s.tc.QueryWorkflowWithOptions(ctx, &client.QueryWorkflowWithOptionsRequest{
		WorkflowID: s.copilotID(u, r.PathValue("id")), QueryType: flow.QueryCopilot,
		QueryRejectCondition: enumspb.QUERY_REJECT_CONDITION_NOT_OPEN,
	})
	var missing *serviceerror.NotFound
	switch {
	case errors.As(err, &missing), err == nil && resp.QueryRejected != nil:
		writeJSON(w, http.StatusOK, empty)
		return
	case err != nil:
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	var st flow.CopilotState
	if err := resp.QueryResult.Get(&st); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// sendCopilot hands the copilot a message, starting the conversation if
// there isn't one (Update-With-Start). It returns once the message is
// accepted; the browser follows the turn by polling getCopilot.
func (s *Server) sendCopilot(w http.ResponseWriter, r *http.Request, u user) {
	var m flow.CopilotMessage
	if err := decodeBody(r, &m); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	def, ok, err := s.findFlow(r.Context(), u, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("flow not found"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	start := s.tc.NewWithStartWorkflowOperation(client.StartWorkflowOptions{
		ID:                       s.copilotID(u, def.ID),
		TaskQueue:                s.cfg.TaskQueue,
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
		StaticSummary:            "Copilot · " + def.Name,
		StaticDetails:            "Builds and edits the flow " + def.Name + " with " + u.Name + ".",
	}, workflows.CopilotWorkflow, workflows.CopilotInput{FlowID: def.ID, FlowName: def.Name})
	handle, err := s.tc.UpdateWithStartWorkflow(ctx, client.UpdateWithStartWorkflowOptions{
		StartWorkflowOperation: start,
		UpdateOptions: client.UpdateWorkflowOptions{
			UpdateName: flow.UpdateCopilotMessage, Args: []any{m}, WaitForStage: client.WorkflowUpdateStageAccepted,
		},
	})
	if err == nil {
		err = handle.Get(ctx, nil)
	}
	switch {
	case err == nil:
		w.WriteHeader(http.StatusAccepted)
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusServiceUnavailable, errors.New("no worker answered in time; try again"))
	default:
		writeError(w, http.StatusConflict, err)
	}
}

// copilotResult returns a tool call's result from the browser.
func (s *Server) copilotResult(w http.ResponseWriter, r *http.Request, u user) {
	var res flow.CopilotResult
	if err := decodeBody(r, &res); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.update(w, r, s.copilotID(u, r.PathValue("id")), flow.UpdateCopilotResult, res)
}

func (s *Server) stopCopilot(w http.ResponseWriter, r *http.Request, u user) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	handle, err := s.tc.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID: s.copilotID(u, r.PathValue("id")), UpdateName: flow.UpdateCopilotStop, WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	if err == nil {
		err = handle.Get(ctx, nil)
	}
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// resetCopilot ends the conversation; the next message starts a new one.
func (s *Server) resetCopilot(w http.ResponseWriter, r *http.Request, u user) {
	err := s.tc.TerminateWorkflow(r.Context(), s.copilotID(u, r.PathValue("id")), "", "new conversation")
	var missing *serviceerror.NotFound
	if err != nil && !errors.As(err, &missing) {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// validateFlow checks a flow as saving would, without saving it.
func (s *Server) validateFlow(w http.ResponseWriter, r *http.Request, u user) {
	var def flow.Definition
	if err := decodeBody(r, &def); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	problems := append(def.Validate(), s.limitProblems(def)...)
	if problems == nil {
		problems = []flow.Problem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"problems": problems})
}
