package workflows

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

// Activities hold everything that touches the outside world. Workflow code
// renders templates and decides what to call; Activities only do the I/O.
type Activities struct {
	HTTP *http.Client
	// Claude is nil when ANTHROPIC_API_KEY is not configured; agent nodes then
	// fail with a clear, non-retryable error instead of retrying forever.
	Claude *anthropic.Client
	Model  string
	// The worker's connections to the business connectors' services. A
	// connector step names an operation; these hold the credentials.
	Salesforce *SalesforceConnection
	Stripe     *StripeConnection
	ServiceNow *ServiceNowConnection
	Slack      *SlackConnection
	GitHub     *GitHubConnection
}

type HTTPRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
	// NonRetryableStatus lists extra HTTP statuses that end retries.
	NonRetryableStatus []int `json:"nonRetryableStatus,omitempty"`
}

type HTTPResponse struct {
	Status int `json:"status"`
	// Body is parsed JSON when the response is JSON, otherwise text.
	Body any `json:"body"`
}

// Heartbeat is what a running attempt reports while it waits on the outside
// world. The run view shows it.
type Heartbeat struct {
	Phase   string `json:"phase"`
	Seconds int    `json:"seconds"` // how long this attempt has been waiting
}

// HeartbeatTimeout is how long Temporal waits for a heartbeat before it treats
// the attempt as lost (its worker crashed, or the Lambda invocation ended) and
// retries it. Without heartbeats that takes the whole StartToClose timeout.
// It also bounds how quickly a cancelled attempt finds out (see keepAlive).
// A variable only so tests can shorten it.
var HeartbeatTimeout = 10 * time.Second

// heartbeatEvery is how often an attempt heartbeats. The SDK sends at most
// one heartbeat to the server every 80% of HeartbeatTimeout, with the latest
// details, so this only bounds how fresh those details are. A cancellation
// reaches the attempt within about 80% of HeartbeatTimeout.
const heartbeatEvery = 2 * time.Second

// keepAlive heartbeats until stop is called.
//
// Heartbeats are also how an attempt learns that it was cancelled. When the
// Workflow cancels the Activity (it lost a First of race, or the run was
// cancelled), the server answers the next heartbeat with the cancellation,
// the SDK cancels ctx, and the in-flight HTTP request or Claude call is
// aborted. Without heartbeats the attempt would run to the end, unseen.
func keepAlive(ctx context.Context, phase string) (stop func()) {
	started := time.Now()
	beat := func() {
		if ctx.Err() != nil {
			return
		}
		activity.RecordHeartbeat(ctx, Heartbeat{Phase: phase, Seconds: int(time.Since(started).Seconds())})
	}
	beat()
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(heartbeatEvery)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				beat()
			}
		}
	}()
	return func() { close(done) }
}

// Responses are stored in Workflow history and passed to later steps, so keep
// them well under the 2 MB payload limit.
const maxBodyBytes = 64 << 10

// HTTPRequest performs one attempt. Temporal's retry policy (configured on the
// node) decides whether there is another.
func (a *Activities) HTTPRequest(ctx context.Context, req HTTPRequest) (HTTPResponse, error) {
	method := strings.ToUpper(req.Method)
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if req.Body != "" {
		body = strings.NewReader(req.Body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, req.URL, body)
	if err != nil {
		// A malformed URL won't fix itself on retry.
		return HTTPResponse{}, temporal.NewNonRetryableApplicationError(err.Error(), "InvalidRequest", nil)
	}
	if req.Body != "" {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}

	defer keepAlive(ctx, "waiting for "+method+" "+truncate(req.URL, 80))()
	resp, err := a.HTTP.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			activity.GetLogger(ctx).Info("http request abandoned: the step was cancelled", "method", method, "url", req.URL)
			return HTTPResponse{}, ctx.Err()
		}
		return HTTPResponse{}, err // network errors are worth retrying
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return HTTPResponse{}, err
	}
	out := HTTPResponse{Status: resp.StatusCode, Body: decodeBody(raw)}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		activity.GetLogger(ctx).Info("http request succeeded", "method", method, "url", req.URL, "status", resp.StatusCode)
		return out, nil
	}
	msg := fmt.Sprintf("%s %s returned %s", method, req.URL, resp.Status)
	if text := strings.TrimSpace(string(raw)); text != "" {
		msg += ": " + truncate(text, 200)
	}
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
}

// maxRetryAfter keeps a misbehaving server from parking a step for days.
const maxRetryAfter = 10 * time.Minute

// retryAfter reads a Retry-After header: delay-seconds or an HTTP date.
func retryAfter(header string, now time.Time) (time.Duration, bool) {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0, false
	}
	var d time.Duration
	if secs, err := strconv.Atoi(header); err == nil {
		d = time.Duration(secs) * time.Second
	} else if at, err := http.ParseTime(header); err == nil {
		d = at.Sub(now)
	} else {
		return 0, false
	}
	return min(max(d, time.Second), maxRetryAfter), true
}

// LLMRequest is one turn of the agent loop. Messages are raw Messages API JSON
// so that assistant turns (including thinking blocks) go back to the model
// exactly as they came out of it.
type LLMRequest struct {
	System   string            `json:"system"`
	Tools    []LLMTool         `json:"tools"`
	Messages []json.RawMessage `json:"messages"`
	// Cache marks the prompt for caching up to its last block: a long
	// conversation resends the same system prompt, tools and history.
	Cache bool `json:"cache,omitempty"`
}

type LLMTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Properties  map[string]any `json:"properties"`
	Required    []string       `json:"required"`
}

type LLMResponse struct {
	// Assistant is the full assistant message to append to the conversation.
	Assistant  json.RawMessage `json:"assistant"`
	StopReason string          `json:"stopReason"`
	Reasoning  []string        `json:"reasoning,omitempty"`
	Text       string          `json:"text,omitempty"`
	ToolCalls  []ToolCall      `json:"toolCalls,omitempty"`
}

type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// CallClaude asks the model for the agent's next step.
func (a *Activities) CallClaude(ctx context.Context, req LLMRequest) (LLMResponse, error) {
	if a.Claude == nil {
		return LLMResponse{}, temporal.NewNonRetryableApplicationError(
			"ANTHROPIC_API_KEY is not set on the worker", "AgentNotConfigured", nil)
	}

	tools := make([]anthropic.BetaToolUnionParam, 0, len(req.Tools))
	for _, t := range req.Tools {
		tools = append(tools, anthropic.BetaToolUnionParam{OfTool: &anthropic.BetaToolParam{
			Name:        t.Name,
			Description: anthropic.String(t.Description),
			InputSchema: anthropic.BetaToolInputSchemaParam{Properties: t.Properties, Required: t.Required},
		}})
	}
	messages := make([]anthropic.BetaMessageParam, 0, len(req.Messages))
	for _, m := range req.Messages {
		messages = append(messages, param.Override[anthropic.BetaMessageParam](m))
	}

	// A model turn can take a minute or more, so heartbeat while it runs.
	defer keepAlive(ctx, "waiting for Claude")()
	var cache anthropic.BetaCacheControlEphemeralParam
	if req.Cache {
		cache = anthropic.NewBetaCacheControlEphemeralParam()
	}
	resp, err := a.Claude.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
		CacheControl: cache,
		Model:        anthropic.Model(a.Model),
		MaxTokens:    16000,
		System:       []anthropic.BetaTextBlockParam{{Text: req.System}},
		Tools:        tools,
		Messages:     messages,
		// Summarized thinking gives the run view a readable reasoning trace.
		Thinking: anthropic.BetaThinkingConfigParamUnion{OfAdaptive: &anthropic.BetaThinkingConfigAdaptiveParam{
			Display: anthropic.BetaThinkingConfigAdaptiveDisplaySummarized,
		}},
		OutputConfig: anthropic.BetaOutputConfigParam{Effort: anthropic.BetaOutputConfigEffortMedium},
		// If a safety classifier declines the request, the API re-serves it on
		// a fallback model inside the same call.
		Fallbacks: anthropic.BetaFallbacksParamUnion{OfDefault: constant.ValueOf[constant.Default]()},
		Betas:     []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01},
	})
	if err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) {
			switch {
			case apiErr.StatusCode == 408, apiErr.StatusCode == 429, apiErr.StatusCode >= 500:
				return LLMResponse{}, temporal.NewApplicationError(apiErr.Error(), "ClaudeUnavailable")
			default:
				return LLMResponse{}, temporal.NewNonRetryableApplicationError(apiErr.Error(), "ClaudeRequestRejected", nil)
			}
		}
		return LLMResponse{}, err
	}
	if resp.StopReason == anthropic.BetaStopReasonRefusal {
		return LLMResponse{}, temporal.NewNonRetryableApplicationError(
			"the model declined this request", "AgentRefused", nil)
	}

	assistant, err := json.Marshal(resp.ToParam())
	if err != nil {
		return LLMResponse{}, err
	}
	out := LLMResponse{Assistant: assistant, StopReason: string(resp.StopReason)}
	for _, block := range resp.Content {
		switch b := block.AsAny().(type) {
		case anthropic.BetaThinkingBlock:
			if strings.TrimSpace(b.Thinking) != "" {
				out.Reasoning = append(out.Reasoning, b.Thinking)
			}
		case anthropic.BetaTextBlock:
			out.Text += b.Text
		case anthropic.BetaToolUseBlock:
			out.ToolCalls = append(out.ToolCalls, ToolCall{ID: b.ID, Name: b.Name, Input: json.RawMessage(b.JSON.Input.Raw())})
		}
	}
	activity.GetLogger(ctx).Info("agent model turn", "stop_reason", out.StopReason, "tool_calls", len(out.ToolCalls))
	return out, nil
}

func decodeBody(raw []byte) any {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var v any
	if json.Unmarshal(raw, &v) == nil {
		return v
	}
	return string(raw)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
