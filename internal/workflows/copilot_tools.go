package workflows

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/connectors"
	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
)

// The copilot's tools. The browser carries out the ones that read or change
// the canvas (callback tools); the Workflow answers the reference ones
// itself, from the step and connector catalogs compiled into it.

// browserTools run in the browser, on the unsaved flow.
var browserTools = map[string]bool{
	"get_flow": true, "add_step": true, "update_step": true, "remove_step": true,
	"connect": true, "disconnect": true, "set_saga_member": true, "remove_saga_member": true,
	"update_flow": true, "tidy_up": true, "check_flow": true,
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func tool(name, desc string, props map[string]any, required ...string) LLMTool {
	if props == nil {
		props = map[string]any{}
	}
	return LLMTool{Name: name, Description: desc, Properties: props, Required: required}
}

var anyObject = map[string]any{"type": "object", "description": "A JSON object.", "additionalProperties": true}

// copilotTools are the tools offered to the model.
var copilotTools = []LLMTool{
	tool("list_step_types", "List the step types you can add, with what each does and its outcomes.", nil),
	tool("describe_step_type", "Describe one step type: its config fields, outcomes, and how to use it. Read this before adding or configuring a step of that type.",
		map[string]any{"type": str("A step type, e.g. http, condition, forEach.")}, "type"),
	tool("list_connectors", "List the business connectors (Salesforce, Stripe, ServiceNow, Slack, GitHub) and their operations.", nil),
	tool("describe_operation", "Describe a connector operation: its settings, the inputs to map, and its outputs. Read this before adding or configuring a connector step.",
		map[string]any{"connector": str("The connector's step type, e.g. salesforce."), "operation": str("The operation's name, e.g. upsert.")}, "connector", "operation"),

	tool("get_flow", "Read the flow as it is on the canvas now, unsaved changes included: its steps (by key), edges, sagas and variables.", nil),
	tool("add_step", "Add a step to the canvas. Returns its key, outcomes and config.",
		map[string]any{
			"type":      str("The step type."),
			"operation": str("For a connector step: the operation (fixed once the step exists)."),
			"label":     str("The step's label on the canvas."),
			"key":       str("Optional output key (lower_snake_case); later steps read its output as steps.<key>. Defaults from the label."),
			"config":    map[string]any{"type": "object", "description": "Config fields to set, merged over the type's defaults (see describe_step_type).", "additionalProperties": true},
			"after": map[string]any{"type": "object", "description": "Connect it after an existing step.", "properties": map[string]any{
				"step":    str("The existing step's key."),
				"outcome": str("Which of that step's outcomes leads here; defaults to its main outcome."),
			}, "required": []string{"step"}},
		}, "type"),
	tool("update_step", "Change a step: its label, its key, or its config. config is a merge patch: fields you give replace those values, null removes a field, objects merge.",
		map[string]any{
			"step":   str("The step's key."),
			"label":  str("A new label."),
			"key":    str("A new key (later steps' templates are not rewritten)."),
			"config": anyObject,
		}, "step"),
	tool("remove_step", "Remove a step and its edges (and its saga links).", map[string]any{"step": str("The step's key.")}, "step"),
	tool("connect", "Connect one step's outcome to another step.",
		map[string]any{"from": str("The source step's key."), "to": str("The target step's key."), "outcome": str("The source's outcome; defaults to its main outcome.")}, "from", "to"),
	tool("disconnect", "Remove an edge.",
		map[string]any{"from": str("The source step's key."), "to": str("The target step's key."), "outcome": str("Only the edge from this outcome; default any.")}, "from", "to"),
	tool("set_saga_member", "Put a step on the main path into a Saga step, or change its part in it.",
		map[string]any{
			"saga":    str("The Saga step's key."),
			"step":    str("The member step's key."),
			"unwinds": map[string]any{"type": "boolean", "description": "true: the step's failure unwinds the saga. false: its failure is handled and the run carries on."},
			"undo":    str(`What undoes the step when the saga unwinds: another step's key (a compensation step, not connected to anything before it), "builtin" for a connector operation's built-in rollback, or "none".`),
		}, "saga", "step", "unwinds"),
	tool("remove_saga_member", "Take a step out of a saga.", map[string]any{"saga": str("The Saga step's key."), "step": str("The step's key.")}, "saga", "step"),
	tool("update_flow", "Change the flow's name, description or variables.",
		map[string]any{
			"name":        str("A new name."),
			"description": str("A new description."),
			"variables": map[string]any{"type": "array", "description": "Replaces the flow's variables.", "items": map[string]any{"type": "object", "properties": map[string]any{
				"name":    str("lower_snake_case name; read as vars.<name>."),
				"initial": str("Starting value as JSON text, e.g. 0, \"\", []."),
				"visible": map[string]any{"type": "boolean", "description": "Readable from outside the run."},
			}, "required": []string{"name"}}},
		}),
	tool("tidy_up", "Lay the canvas out neatly. Do it after adding or connecting several steps.", nil),
	tool("check_flow", "Check the flow for problems, as saving would. Do this after your edits and fix what it reports.", nil),
}

// stepDoc describes a step type for the copilot.
type stepDoc struct {
	Summary string `json:"summary"`
	Config  string `json:"config"`
	Notes   string `json:"notes,omitempty"`
}

// stepDocs is the copilot's reference for each step type. Outcomes come from
// flow.Handles; the config fields mirror the flow package's config structs.
var stepDocs = map[flow.NodeType]stepDoc{
	flow.TypeTrigger: {"Starts the main path. A flow has exactly one.",
		`{"kind": "manual"|"webhook"|"schedule", "sampleInput": "<JSON text used as input for manual runs>", "schedule": {"every": 5, "everyUnit": "minutes"} | {"cron": "0 9 * * 1-5"} | {"calendar": {"days": ["mon"], "hour": 9, "minute": 0}}, "timezone": "America/New_York"}`,
		"The run's input is `input`. A webhook's JSON body becomes the input."},
	flow.TypeOnStart: {"Begins the init lane: runs once when the run starts, before the main path and any event lanes.", `{}`, ""},
	flow.TypeOnSignal: {"Begins an event lane that runs each time a Signal with this name arrives.", `{"signalName": "order_status"}`,
		"The event's payload is `events.<signalName>` in templates."},
	flow.TypeOnUpdate: {"Begins an event lane for an Update: a request that gets an answer from a Respond step in the lane.", `{"updateName": "change_address", "validator": "<optional expression over request; false rejects it>"}`,
		"The Update's argument is `request`."},
	flow.TypeHTTP: {"Calls an HTTP API as an Activity with a retry policy.",
		`{"method": "GET"|"POST"|"PUT"|"PATCH"|"DELETE", "url": "https://...{{ input.id }}", "headers": {"Content-Type": "application/json"}, "body": "<JSON text template>", "activity": {"startToCloseSeconds": 30, "retry": {"maxAttempts": 5, "initialIntervalSeconds": 1, "backoffCoefficient": 2, "nonRetryableErrorTypes": ["HTTPClientError"]}}, "pauseForFix": false}`,
		"Output: {status, body, headers}; read steps.<key>.body. 4xx is HTTPClientError, 5xx HTTPServerError. The error outcome takes failures after retries."},
	flow.TypeWaitCondition: {"Waits until an expression over the run's data is true (re-checked when events arrive or variables change).", `{"condition": "vars.paid == true", "signalName": "<optional event it is about>", "timeoutSeconds": 0}`, "timeoutSeconds 0 waits for ever."},
	flow.TypeWaitSignal: {"Waits for a Signal (an event) by name.", `{"signalName": "payment_settled", "timeoutSeconds": 3600, "collect": {"quietSeconds": 5, "maxEvents": 10}, "match": "<in a For each body: expression over event and item>"}`,
		"Output is the event's payload (or {events, count} when collecting)."},
	flow.TypeApproval:  {"Waits for a person to approve or reject (an Update) in the run view.", `{"title": "Approve refund of {{ input.amount }}?", "details": "...", "timeoutSeconds": 86400}`, ""},
	flow.TypeDelay:     {"A durable timer.", `{"mode": "duration", "amount": 10, "unit": "seconds"|"minutes"|"hours"|"days"} or {"mode": "expression", "expression": "duration(\"1h\") * 2 | a time to wait until"}`, ""},
	flow.TypeCondition: {"Branches on an expression.", `{"expression": "input.amount > 100"}`, "Expressions use expr-lang syntax over input, steps, vars, events, item, run."},
	flow.TypeTransform: {"Reshapes data in Workflow code, deterministically. Either a JSON template, or typed fields with a mapping.",
		`{"output": "{\"total\": \"{{ steps.order.body.total }}\"}"} or {"fields": [{"name": "total", "type": "number"}], "mapping": {"total": "{{ steps.order.body.total }}"}}`,
		"Prefer the JSON template form: a template whose whole value is one {{ expression }} keeps its type."},
	flow.TypeSetVariable: {"Sets run variables (declared in the flow's variables).", `{"assignments": [{"name": "count", "value": "{{ vars.count + 1 }}"}]}`, ""},
	flow.TypeFirstOf:     {"Races the branches leading into it: the first to arrive (or to succeed) continues, the rest are cancelled.", `{"wins": "arrives"|"succeeds"}`, "Connect two or more branches into it."},
	flow.TypeRespond:     {"Returns a value to the caller: an Update's caller in an On update lane, or on the main path whoever started the run (early return).", `{"value": "{\"ok\": true}"}`, ""},
	flow.TypeAgent: {"Runs a Claude agent as a child Workflow, with HTTP tools and optionally a question for a person.",
		`{"instructions": "...", "tools": [{"name": "get_order", "description": "...", "method": "GET", "url": "https://.../orders/{{ args.id }}", "params": [{"name": "id", "description": "..."}]}], "allowAskHuman": true, "maxTurns": 10}`, ""},
	flow.TypeRunFlow: {"Runs another saved flow as a child Workflow.", `{"flowId": "<flow id>", "input": "<JSON template>", "wait": "result"|"start", "parentClosePolicy": "terminate"|"request_cancel"|"abandon"}`, ""},
	flow.TypeForEach: {"Runs something once per item of a list: a saved flow as child Workflows, or the steps on its body outcome inside this run.",
		`{"items": "steps.query.records", "run": "steps"|"flow", "flowId": "<for run=flow>", "input": "<for run=flow>", "concurrency": 5, "finishWhen": "all"|"first"|"count"}`,
		"With run=steps, connect its body outcome to the first step of the body, and the body's last step back into the For each (that edge means: next item). Body steps see item and index. Its out outcome continues once every item is done."},
	flow.TypeLoop: {"Repeats the steps on its body outcome until a condition holds (repeat-until).", `{"until": "steps.check.body.status == \"shipped\"", "maxPasses": 20, "pause": {"mode": "duration", "amount": 5, "unit": "seconds"}}`,
		"Connect body to the first body step and the last body step back into the Loop. done follows when until is true; limit when maxPasses runs out."},
	flow.TypeSaga: {"Groups steps on the main path whose work can be undone; members link to it with set_saga_member.", `{"members": []} (manage members with set_saga_member, not config)`,
		"When a member whose failure unwinds the saga fails, the registered undos run newest first; then the Saga takes compensated (or failed if an undo failed). With nothing connected there, the run fails. A Saga step is an entry: don't connect anything into it."},
	flow.TypeEnd: {"Ends the run.", `{}`, ""},
}

// connectorTypes are the connector step types, in palette order.
func connectorTypes() []string {
	var out []string
	for _, c := range connectors.All {
		out = append(out, c.Type)
	}
	return out
}

// workflowTool answers a reference tool from the catalogs. ok is false for a
// tool the browser carries out.
func workflowTool(name string, input json.RawMessage) (content string, isError, ok bool) {
	var args map[string]string
	_ = json.Unmarshal(input, &args)
	reply := func(v any) (string, bool, bool) {
		b, _ := json.Marshal(v)
		return string(b), false, true
	}
	fail := func(format string, a ...any) (string, bool, bool) { return fmt.Sprintf(format, a...), true, true }

	switch name {
	case "list_step_types":
		type row struct {
			Type     string   `json:"type"`
			Summary  string   `json:"summary"`
			Outcomes []string `json:"outcomes"`
		}
		var rows []row
		for t, d := range stepDocs {
			rows = append(rows, row{string(t), d.Summary, flow.Handles[t]})
		}
		slices.SortFunc(rows, func(a, b row) int { return strings.Compare(a.Type, b.Type) })
		for _, c := range connectors.All {
			rows = append(rows, row{c.Type, c.Label + " connector: one operation per step (see list_connectors).", flow.Handles[flow.TypeHTTP]})
		}
		return reply(rows)

	case "describe_step_type":
		t := flow.NodeType(args["type"])
		if slices.Contains(connectorTypes(), string(t)) {
			return reply(map[string]any{
				"summary":  "A connector step runs one operation as an Activity, with the worker's credentials.",
				"config":   `{"operation": "<name, set when adding>", "settings": {"<setting>": "<value>"}, "mapping": {"<input path, e.g. values.Name>": "{{ template }}"}}`,
				"outcomes": flow.Handles[flow.TypeHTTP],
				"notes":    "Use describe_operation for the settings, inputs and outputs. Output fields are read as steps.<key>.<field>.",
			})
		}
		d, ok := stepDocs[t]
		if !ok {
			return fail("no step type %q; see list_step_types", t)
		}
		return reply(map[string]any{"summary": d.Summary, "config": d.Config, "outcomes": flow.Handles[t], "notes": d.Notes})

	case "list_connectors":
		type op struct {
			Name  string `json:"name"`
			Label string `json:"label"`
		}
		var out []map[string]any
		for _, c := range connectors.All {
			var ops []op
			for _, o := range c.Operations {
				ops = append(ops, op{o.Name, o.Label})
			}
			out = append(out, map[string]any{"type": c.Type, "label": c.Label, "operations": ops})
		}
		return reply(out)

	case "describe_operation":
		c, found := connectors.Get(args["connector"])
		if !found {
			return fail("no connector %q; see list_connectors", args["connector"])
		}
		o, found := c.Operation(args["operation"])
		if !found {
			return fail("%s has no operation %q; see list_connectors", c.Label, args["operation"])
		}
		desc := map[string]any{
			"operation": o.Name, "label": o.Label, "settings": o.Settings, "inputs": o.Inputs, "outputs": o.Outputs, "note": o.Note,
		}
		if o.Compensation != nil {
			desc["builtInRollback"] = o.Compensation.Note
		}
		if len(c.Catalog) > 0 {
			names := make([]string, 0, len(c.Catalog))
			for n := range c.Catalog {
				names = append(names, n)
			}
			slices.Sort(names)
			desc["catalog"] = map[string]any{"of": c.CatalogOf, "names": names, "fields": c.Catalog}
		}
		return reply(desc)
	}
	return "", false, false
}
