package flow

import (
	"encoding/json"
	"time"
)

// The copilot is a chat in the builder that edits the open flow with you. Its
// conversation is a Workflow (one per user and flow); the tools that change
// the canvas are callback tools: the Workflow publishes the call, the browser
// carries it out on the unsaved flow and returns the result by Update.

// Copilot message names.
const (
	QueryCopilot         = "copilot"
	UpdateCopilotMessage = "copilot_message"
	UpdateCopilotResult  = "copilot_result"
	UpdateCopilotStop    = "copilot_stop"
)

// CopilotMessage is something the person asked.
type CopilotMessage struct {
	// ID is the browser's, so it can match the turn to the canvas it had
	// before the copilot changed it (Undo this turn).
	ID   string `json:"id"`
	Text string `json:"text"`
	// Flow is the canvas as the browser has it, unsaved changes included,
	// in the copilot's compact form.
	Flow json.RawMessage `json:"flow,omitempty"`
	// ClientID is the browser tab that sent it; that tab carries out the
	// turn's tool calls.
	ClientID string `json:"clientId"`
}

// CopilotResult answers one tool call.
type CopilotResult struct {
	CallID string          `json:"callId"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// CopilotState is what the "copilot" Query returns.
type CopilotState struct {
	// Status is idle, thinking, or editing (waiting for the browser).
	Status  string         `json:"status"`
	Entries []CopilotEntry `json:"entries"`
	Pending []CopilotCall  `json:"pending"`
	// Queued counts messages waiting for the current turn to finish.
	Queued int `json:"queued,omitempty"`
}

// CopilotEntry is one line of the conversation.
type CopilotEntry struct {
	Seq int `json:"seq"`
	// Kind is user, assistant, tool, or error.
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
	// MessageID is set on user entries: the browser's CopilotMessage.ID.
	MessageID string `json:"messageId,omitempty"`
	// Tool entries: the call, its status (running, done, failed) and a
	// short summary of what it did.
	Tool   string          `json:"tool,omitempty"`
	CallID string          `json:"callId,omitempty"`
	Input  json.RawMessage `json:"input,omitempty"`
	Status string          `json:"status,omitempty"`
	At     time.Time       `json:"at"`
}

// CopilotCall is a tool call waiting for the browser.
type CopilotCall struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Input    json.RawMessage `json:"input"`
	ClientID string          `json:"clientId"`
}
