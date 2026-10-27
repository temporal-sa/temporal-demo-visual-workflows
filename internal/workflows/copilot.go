package workflows

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
)

// CopilotInput starts a copilot conversation, or carries one across
// Continue-As-New.
type CopilotInput struct {
	FlowID string `json:"flowId"`
	// FlowName labels the conversation's Activities in the event history.
	FlowName string                `json:"flowName,omitempty"`
	Messages []json.RawMessage     `json:"messages,omitempty"`
	Entries  []flow.CopilotEntry   `json:"entries,omitempty"`
	Inbox    []flow.CopilotMessage `json:"inbox,omitempty"`
}

const (
	// copilotIdle ends a conversation nobody has written to for this long;
	// the next message starts a fresh one.
	copilotIdle = time.Hour
	// copilotEditorWait is how long a tool call waits for the browser:
	// long enough to reload the page or reopen the editor.
	copilotEditorWait = 10 * time.Minute
	// copilotModelCalls bounds the model calls for one message.
	copilotModelCalls = 40
	// copilotKeptEntries is how much of the transcript survives
	// Continue-As-New.
	copilotKeptEntries = 100
	// copilotHistoryBytes bounds the conversation carried to the model.
	copilotHistoryBytes = 300_000
)

// CopilotWorkflow is the builder's copilot: a conversation in which Claude
// edits the open flow. Model turns are Activities. The tools that read or
// change the canvas are callback tools: the call waits here, durably, until
// the browser that asked carries it out on its unsaved flow and returns the
// result with an Update. Closing the tab mid-turn leaves the call waiting;
// reopening the editor answers it and the turn carries on.
func CopilotWorkflow(ctx workflow.Context, in CopilotInput) error {
	var a *Activities
	c := &copilot{in: in, messages: in.Messages, inbox: in.Inbox, results: map[string]*flow.CopilotResult{}}
	c.state = flow.CopilotState{Status: "idle", Entries: in.Entries, Pending: []flow.CopilotCall{}}
	if c.state.Entries == nil {
		c.state.Entries = []flow.CopilotEntry{}
	}
	for _, e := range c.state.Entries {
		c.seq = max(c.seq, e.Seq)
	}

	if err := workflow.SetQueryHandler(ctx, flow.QueryCopilot, func() (flow.CopilotState, error) {
		st := c.state
		st.Queued = len(c.inbox)
		return st, nil
	}); err != nil {
		return err
	}
	if err := workflow.SetUpdateHandlerWithOptions(ctx, flow.UpdateCopilotMessage,
		func(ctx workflow.Context, m flow.CopilotMessage) error {
			c.inbox = append(c.inbox, m)
			return nil
		},
		workflow.UpdateHandlerOptions{Validator: func(m flow.CopilotMessage) error {
			switch {
			case strings.TrimSpace(m.Text) == "":
				return fmt.Errorf("the message is empty")
			case len(m.Text) > 8000:
				return fmt.Errorf("the message is too long")
			case len(m.Flow) > 200_000:
				return fmt.Errorf("the flow is too large for the copilot")
			case len(c.inbox) >= 5:
				return fmt.Errorf("the copilot is still working through earlier messages")
			}
			return nil
		}},
	); err != nil {
		return err
	}
	if err := workflow.SetUpdateHandlerWithOptions(ctx, flow.UpdateCopilotResult,
		func(ctx workflow.Context, r flow.CopilotResult) error {
			c.results[r.CallID] = &r
			return nil
		},
		workflow.UpdateHandlerOptions{Validator: func(r flow.CopilotResult) error {
			if c.results[r.CallID] != nil {
				return temporal.NewApplicationError("tool call "+r.CallID+" is already answered", "CallbackAlreadyResolved")
			}
			if !slices.ContainsFunc(c.state.Pending, func(p flow.CopilotCall) bool { return p.ID == r.CallID }) {
				return temporal.NewApplicationError("no tool call "+r.CallID+" is waiting", "UnknownCallback")
			}
			return nil
		}},
	); err != nil {
		return err
	}
	if err := workflow.SetUpdateHandlerWithOptions(ctx, flow.UpdateCopilotStop,
		func(ctx workflow.Context) error {
			c.stop = true
			c.inbox = nil
			return nil
		},
		workflow.UpdateHandlerOptions{Validator: func() error {
			if c.state.Status == "idle" && len(c.inbox) == 0 {
				return fmt.Errorf("the copilot isn't working on anything")
			}
			return nil
		}},
	); err != nil {
		return err
	}

	for {
		if len(c.inbox) == 0 {
			if workflow.GetInfo(ctx).GetContinueAsNewSuggested() {
				return c.continueAsNew(ctx)
			}
			more, err := workflow.AwaitWithOptions(ctx, workflow.AwaitOptions{
				Timeout: copilotIdle, TimerOptions: workflow.TimerOptions{Summary: "Copilot idle: ends after an hour without a message"},
			}, func() bool {
				return len(c.inbox) > 0 || workflow.GetInfo(ctx).GetContinueAsNewSuggested()
			})
			if err != nil {
				return err
			}
			if !more {
				// Let a message that arrived in the same task finish its handler.
				if err := workflow.Await(ctx, func() bool { return workflow.AllHandlersFinished(ctx) }); err != nil {
					return err
				}
				if len(c.inbox) == 0 {
					return nil
				}
			}
			continue
		}
		m := c.inbox[0]
		c.inbox = c.inbox[1:]
		if err := c.turn(ctx, a, m); err != nil {
			return err
		}
	}
}

type copilot struct {
	in       CopilotInput
	state    flow.CopilotState
	seq      int
	messages []json.RawMessage
	inbox    []flow.CopilotMessage
	results  map[string]*flow.CopilotResult
	stop     bool
}

func (c *copilot) add(ctx workflow.Context, e flow.CopilotEntry) int {
	c.seq++
	e.Seq, e.At = c.seq, workflow.Now(ctx)
	c.state.Entries = append(c.state.Entries, e)
	return len(c.state.Entries) - 1
}

func (c *copilot) say(role string, content any) {
	b, _ := json.Marshal(map[string]any{"role": role, "content": content})
	c.messages = append(c.messages, b)
}

// turn answers one message: model calls until the model replies without
// tools, each tool batch answered in order.
func (c *copilot) turn(ctx workflow.Context, a *Activities, m flow.CopilotMessage) error {
	c.stop = false
	c.add(ctx, flow.CopilotEntry{Kind: "user", Text: m.Text, MessageID: m.ID})
	text := m.Text
	if len(m.Flow) > 0 {
		text += "\n\nThe canvas now (unsaved changes included):\n```json\n" + string(m.Flow) + "\n```"
	}
	c.say("user", text)
	defer func() { c.state.Status = "idle" }()

	for call := 1; ; call++ {
		if c.stop {
			c.add(ctx, flow.CopilotEntry{Kind: "error", Text: "Stopped."})
			c.say("assistant", "(The person stopped me here.)")
			return nil
		}
		if call > copilotModelCalls {
			c.add(ctx, flow.CopilotEntry{Kind: "error", Text: fmt.Sprintf("Stopped after %d model calls. Ask me to carry on if there's more to do.", copilotModelCalls)})
			c.say("assistant", "(I stopped here: too many steps for one message.)")
			return nil
		}
		c.state.Status = "thinking"
		var resp LLMResponse
		opts := activityOptions(fmt.Sprintf("copilot-%d-%d", c.seq, call), truncate(fmt.Sprintf("Copilot · %s · model turn %d", cmpLabel(c.in.FlowName, c.in.FlowID), call), maxSummary), agentModelDefaults)
		err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, opts), a.CallClaude,
			LLMRequest{System: copilotSystemPrompt, Tools: copilotTools, Messages: c.messages, Cache: true}).Get(ctx, &resp)
		if err != nil {
			if temporal.IsCanceledError(err) {
				return err
			}
			// The conversation carries on: the person can ask again.
			c.add(ctx, flow.CopilotEntry{Kind: "error", Text: "The model call failed: " + describeError(err)})
			c.say("assistant", "(My last attempt failed with an error.)")
			return nil
		}
		c.messages = append(c.messages, resp.Assistant)
		if strings.TrimSpace(resp.Text) != "" {
			c.add(ctx, flow.CopilotEntry{Kind: "assistant", Text: strings.TrimSpace(resp.Text)})
		}
		if len(resp.ToolCalls) == 0 {
			return nil
		}

		results := make([]map[string]any, 0, len(resp.ToolCalls))
		for _, tc := range resp.ToolCalls {
			content, isError, err := c.run(ctx, tc, m.ClientID)
			if err != nil {
				return err
			}
			results = append(results, map[string]any{"type": "tool_result", "tool_use_id": tc.ID, "content": content, "is_error": isError})
		}
		c.say("user", results)
	}
}

// run carries out one tool call: in the Workflow for a reference tool, in
// the browser otherwise.
func (c *copilot) run(ctx workflow.Context, tc ToolCall, clientID string) (string, bool, error) {
	at := c.add(ctx, flow.CopilotEntry{Kind: "tool", Tool: tc.Name, CallID: tc.ID, Input: clip(tc.Input, 2000), Status: "running"})
	finish := func(content string, isError bool) (string, bool, error) {
		e := &c.state.Entries[at]
		e.Status, e.Text = "done", summaryOf(content)
		if isError {
			e.Status, e.Text = "failed", truncate(content, 300)
		}
		return truncate(content, 30_000), isError, nil
	}
	if c.stop {
		return finish("not run: the person stopped the copilot", true)
	}
	if content, isError, ok := workflowTool(tc.Name, tc.Input); ok {
		return finish(content, isError)
	}
	if !browserTools[tc.Name] {
		return finish("unknown tool "+tc.Name, true)
	}

	c.state.Pending = append(c.state.Pending, flow.CopilotCall{ID: tc.ID, Name: tc.Name, Input: tc.Input, ClientID: clientID})
	c.state.Status = "editing"
	defer func() {
		c.state.Pending = slices.DeleteFunc(c.state.Pending, func(p flow.CopilotCall) bool { return p.ID == tc.ID })
		c.state.Status = "thinking"
	}()
	// Durable wait for the browser: across reloads and worker restarts.
	answered, err := workflow.AwaitWithOptions(ctx, workflow.AwaitOptions{
		Timeout: copilotEditorWait, TimerOptions: workflow.TimerOptions{Summary: truncate("Waiting for the editor: "+tc.Name+" "+string(clip(tc.Input, 120)), maxSummary)},
	}, func() bool { return c.results[tc.ID] != nil || c.stop })
	if err != nil {
		return "", false, err
	}
	r := c.results[tc.ID]
	switch {
	case r != nil && r.Error != "":
		return finish(r.Error, true)
	case r != nil:
		return finish(string(r.Result), false)
	case c.stop:
		return finish("not run: the person stopped the copilot", true)
	case !answered:
		return finish("the editor didn't answer in time (it may have been closed); nothing was changed", true)
	}
	return finish("no result", true)
}

func (c *copilot) continueAsNew(ctx workflow.Context) error {
	if err := workflow.Await(ctx, func() bool { return workflow.AllHandlersFinished(ctx) }); err != nil {
		return err
	}
	entries := c.state.Entries
	if n := len(entries); n > copilotKeptEntries {
		entries = entries[n-copilotKeptEntries:]
	}
	return workflow.NewContinueAsNewError(ctx, CopilotWorkflow, CopilotInput{
		FlowID: c.in.FlowID, FlowName: c.in.FlowName,
		Messages: trimConversation(c.messages, copilotHistoryBytes), Entries: entries, Inbox: c.inbox,
	})
}

// trimConversation drops the oldest exchanges until the conversation fits,
// cutting only before a message the person wrote (so every tool result
// keeps its tool call).
func trimConversation(messages []json.RawMessage, limit int) []json.RawMessage {
	size := 0
	for _, m := range messages {
		size += len(m)
	}
	if size <= limit {
		return messages
	}
	for i := 1; i < len(messages); i++ {
		size -= len(messages[i-1])
		if personWrote(messages[i]) && size <= limit {
			return messages[i:]
		}
	}
	return messages
}

func personWrote(m json.RawMessage) bool {
	var v struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	}
	_ = json.Unmarshal(m, &v)
	_, text := v.Content.(string)
	return v.Role == "user" && text
}

// summaryOf is a tool result's summary field, which the browser's tools
// fill in, for the transcript.
func summaryOf(content string) string {
	var v struct {
		Summary string `json:"summary"`
	}
	if json.Unmarshal([]byte(content), &v) == nil && v.Summary != "" {
		return truncate(v.Summary, 300)
	}
	return ""
}

func clip(raw json.RawMessage, n int) json.RawMessage {
	if len(raw) <= n {
		return raw
	}
	b, _ := json.Marshal(truncate(string(raw), n))
	return b
}

func cmpLabel(name, id string) string {
	if name != "" {
		return name
	}
	return id
}
