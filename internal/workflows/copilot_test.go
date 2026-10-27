package workflows

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
)

func copilotEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(CopilotWorkflow)
	env.RegisterActivity(&Activities{})
	return env
}

// send sends an Update at a point in (test) time, recording its outcome.
func send(env *testsuite.TestWorkflowEnvironment, at time.Duration, name string, arg any, outcome *error) {
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(name, "", &testsuite.TestUpdateCallback{
			OnReject:   func(err error) { *outcome = err },
			OnAccept:   func() {},
			OnComplete: func(_ any, err error) { *outcome = err },
		}, arg)
	}, at)
}

func copilotState(t *testing.T, env *testsuite.TestWorkflowEnvironment) flow.CopilotState {
	t.Helper()
	v, err := env.QueryWorkflow(flow.QueryCopilot)
	require.NoError(t, err)
	var st flow.CopilotState
	require.NoError(t, v.Get(&st))
	return st
}

func toolResults(t *testing.T, msg json.RawMessage) []map[string]any {
	t.Helper()
	var m struct {
		Content []map[string]any `json:"content"`
	}
	require.NoError(t, json.Unmarshal(msg, &m))
	return m.Content
}

func TestCopilotRunsCallbackToolsInTheBrowser(t *testing.T) {
	env := copilotEnv(t)
	var a *Activities
	assistant := json.RawMessage(`{"role":"assistant","content":[]}`)
	var requests []LLMRequest
	env.OnActivity(a.CallClaude, mock.Anything, mock.Anything).Return(func(_ context.Context, req LLMRequest) (LLMResponse, error) {
		requests = append(requests, req)
		if len(requests) == 1 {
			// A reference tool (answered in the Workflow), then two edits.
			return LLMResponse{Assistant: assistant, ToolCalls: []ToolCall{
				{ID: "t1", Name: "describe_step_type", Input: json.RawMessage(`{"type":"http"}`)},
				{ID: "t2", Name: "add_step", Input: json.RawMessage(`{"type":"http","label":"Charge card"}`)},
				{ID: "t3", Name: "connect", Input: json.RawMessage(`{"from":"trigger","to":"charge_card"}`)},
			}}, nil
		}
		return LLMResponse{Assistant: assistant, Text: "Added Charge card after the trigger."}, nil
	})

	var sent, first, second, duplicate, unknown error
	send(env, time.Second, flow.UpdateCopilotMessage, flow.CopilotMessage{ID: "m1", Text: "charge a card", Flow: json.RawMessage(`{"steps":[]}`), ClientID: "tab-1"}, &sent)
	env.RegisterDelayedCallback(func() {
		st := copilotState(t, env)
		require.Equal(t, "editing", st.Status)
		require.Len(t, st.Pending, 1, "one browser call at a time, in order")
		require.Equal(t, "add_step", st.Pending[0].Name)
		require.Equal(t, "tab-1", st.Pending[0].ClientID)
	}, time.Minute)
	send(env, 2*time.Minute, flow.UpdateCopilotResult, flow.CopilotResult{CallID: "t2", Result: json.RawMessage(`{"summary":"Added Charge card","key":"charge_card"}`)}, &first)
	send(env, 3*time.Minute, flow.UpdateCopilotResult, flow.CopilotResult{CallID: "t2", Result: json.RawMessage(`{}`)}, &duplicate)
	send(env, 3*time.Minute, flow.UpdateCopilotResult, flow.CopilotResult{CallID: "nope"}, &unknown)
	send(env, 4*time.Minute, flow.UpdateCopilotResult, flow.CopilotResult{CallID: "t3", Error: "no step charge_card"}, &second)
	env.RegisterDelayedCallback(func() {
		st := copilotState(t, env)
		require.Equal(t, "idle", st.Status)
		kinds := []string{}
		for _, e := range st.Entries {
			kinds = append(kinds, e.Kind+":"+e.Status)
		}
		require.Equal(t, []string{"user:", "tool:done", "tool:done", "tool:failed", "assistant:"}, kinds)
		require.Equal(t, "Added Charge card", st.Entries[2].Text)
		require.Equal(t, "m1", st.Entries[0].MessageID)
	}, 5*time.Minute)

	env.ExecuteWorkflow(CopilotWorkflow, CopilotInput{FlowID: "f-1"})
	require.NoError(t, env.GetWorkflowError(), "it completes once idle")
	require.NoError(t, sent)
	require.NoError(t, first)
	require.NoError(t, second)
	require.ErrorContains(t, duplicate, "already answered")
	require.ErrorContains(t, unknown, "no tool call")

	require.Len(t, requests, 2)
	require.True(t, requests[0].Cache)
	require.Contains(t, string(requests[0].Messages[0]), "The canvas now")
	results := toolResults(t, requests[1].Messages[2])
	require.Len(t, results, 3)
	require.Contains(t, results[0]["content"], "HTTPClientError", "describe_step_type answered from the catalog")
	require.Equal(t, false, results[1]["is_error"])
	require.Equal(t, true, results[2]["is_error"])
	require.Equal(t, "no step charge_card", results[2]["content"])
}

func TestCopilotToolCallTimesOutWithoutFailingTheTurn(t *testing.T) {
	env := copilotEnv(t)
	var a *Activities
	assistant := json.RawMessage(`{"role":"assistant","content":[]}`)
	var requests []LLMRequest
	env.OnActivity(a.CallClaude, mock.Anything, mock.Anything).Return(func(_ context.Context, req LLMRequest) (LLMResponse, error) {
		requests = append(requests, req)
		if len(requests) == 1 {
			return LLMResponse{Assistant: assistant, ToolCalls: []ToolCall{{ID: "t1", Name: "get_flow", Input: json.RawMessage(`{}`)}}}, nil
		}
		return LLMResponse{Assistant: assistant, Text: "The editor seems closed."}, nil
	})
	var sent error
	send(env, time.Second, flow.UpdateCopilotMessage, flow.CopilotMessage{ID: "m1", Text: "what's here?", ClientID: "tab-1"}, &sent)

	env.ExecuteWorkflow(CopilotWorkflow, CopilotInput{FlowID: "f-1"})
	require.NoError(t, env.GetWorkflowError())
	require.Len(t, requests, 2)
	results := toolResults(t, requests[1].Messages[2])
	require.Equal(t, true, results[0]["is_error"])
	require.Contains(t, results[0]["content"], "didn't answer in time")
}

func TestCopilotStopEndsTheTurn(t *testing.T) {
	env := copilotEnv(t)
	var a *Activities
	calls := 0
	env.OnActivity(a.CallClaude, mock.Anything, mock.Anything).Return(func(_ context.Context, req LLMRequest) (LLMResponse, error) {
		calls++
		return LLMResponse{Assistant: json.RawMessage(`{"role":"assistant","content":[]}`),
			ToolCalls: []ToolCall{{ID: "t1", Name: "tidy_up", Input: json.RawMessage(`{}`)}}}, nil
	})
	var sent, stopped error
	send(env, time.Second, flow.UpdateCopilotMessage, flow.CopilotMessage{ID: "m1", Text: "tidy", ClientID: "tab-1"}, &sent)
	send(env, time.Minute, flow.UpdateCopilotStop, nil, &stopped)
	env.RegisterDelayedCallback(func() {
		st := copilotState(t, env)
		require.Equal(t, "idle", st.Status)
		require.Empty(t, st.Pending)
		last := st.Entries[len(st.Entries)-1]
		require.Equal(t, "Stopped.", last.Text)
	}, 2*time.Minute)

	env.ExecuteWorkflow(CopilotWorkflow, CopilotInput{FlowID: "f-1"})
	require.NoError(t, env.GetWorkflowError())
	require.NoError(t, stopped)
	require.Equal(t, 1, calls, "no model call after Stop")
}

func TestTrimConversationCutsBeforeAPersonsMessage(t *testing.T) {
	user := func(text string) json.RawMessage {
		b, _ := json.Marshal(map[string]any{"role": "user", "content": text})
		return b
	}
	asst := json.RawMessage(`{"role":"assistant","content":[{"type":"tool_use","id":"t1"}]}`)
	results := json.RawMessage(`{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1"}]}`)
	msgs := []json.RawMessage{user(strings.Repeat("a", 500)), asst, results, asst, user("second"), asst}
	trimmed := trimConversation(msgs, 300)
	require.Equal(t, msgs[4:], trimmed, "starts at the person's next message, never at a tool result")
	require.Equal(t, msgs, trimConversation(msgs, 100_000))
}
