package workflows

import (
	"cmp"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"go.temporal.io/sdk/workflow"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/githubmock"
	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/salesforcemock"
	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/servicenowmock"
	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/slackmock"
	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/stripemock"
)

// Registry is what both worker entry points (long-lived and AWS Lambda)
// register on.
type Registry interface {
	RegisterWorkflowWithOptions(w any, options workflow.RegisterOptions)
	RegisterActivity(a any)
}

// Register adds the flow Workflows, the copilot, and their Activities. All
// are Pinned: a run finishes on the worker build it started on, so a new
// build never replays an old run's history and no patching is needed. Keyed
// runs move to the newest build when they continue as new.
func Register(r Registry, acts *Activities) {
	pinned := workflow.RegisterOptions{VersioningBehavior: workflow.VersioningBehaviorPinned}
	r.RegisterWorkflowWithOptions(FlowWorkflow, pinned)
	r.RegisterWorkflowWithOptions(AgentWorkflow, pinned)
	r.RegisterWorkflowWithOptions(CopilotWorkflow, pinned)
	r.RegisterActivity(acts)
}

// RegisterActivities adds only the Activities: for a shared limit's task
// queue, which runs requests, never Workflows.
func RegisterActivities(r Registry, acts *Activities) {
	r.RegisterActivity(acts)
}

// NewActivities wires the Activities' clients from the environment.
func NewActivities(logger *slog.Logger) *Activities {
	acts := &Activities{
		HTTP:  &http.Client{Timeout: 25 * time.Second},
		Model: os.Getenv("ANTHROPIC_MODEL"),
	}
	if acts.Model == "" {
		acts.Model = "claude-opus-5-5"
	}
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		// Temporal owns retries (see the agent's retry policy), so the SDK's
		// own retry loop is turned off to keep each attempt visible.
		claude := anthropic.NewClient(option.WithMaxRetries(0))
		acts.Claude = &claude
	} else {
		logger.Warn("ANTHROPIC_API_KEY is not set; agent steps will fail until it is")
	}
	connect(acts, os.Getenv)
	return acts
}

// connect sets up the business connectors' connections from the environment.
// A connector whose credentials aren't set uses the sandbox's stand-in for
// its service (SANDBOX_URL), with that stand-in's demo credentials, so every
// connector works out of the box. Once credentials are set it talks to the
// real service: Stripe, Slack and GitHub at their public APIs unless their
// URL is set too; Salesforce and ServiceNow at the org or instance in their
// URL.
func connect(acts *Activities, env func(string) string) {
	sandbox := strings.TrimRight(cmp.Or(env("SANDBOX_URL"), "http://localhost:8090"), "/")
	// url picks a connector's address: its own variable, else the real API
	// when its credentials are set, else the sandbox.
	url := func(variable, real, sandboxPath string, credentialsSet bool) string {
		switch {
		case env(variable) != "":
			return strings.TrimRight(env(variable), "/")
		case credentialsSet:
			return real
		}
		return sandbox + sandboxPath
	}
	client := acts.HTTP

	sfOwn := env("SALESFORCE_CLIENT_ID") != "" || env("SALESFORCE_ACCESS_TOKEN") != ""
	acts.Salesforce = &SalesforceConnection{
		URL:          url("SALESFORCE_URL", "", "/salesforce", sfOwn),
		ClientID:     or(sfOwn, env("SALESFORCE_CLIENT_ID"), salesforcemock.ClientID),
		ClientSecret: or(sfOwn, env("SALESFORCE_CLIENT_SECRET"), salesforcemock.ClientSecret),
		AccessToken:  env("SALESFORCE_ACCESS_TOKEN"),
		HTTP:         client,
	}
	stripeOwn := env("STRIPE_API_KEY") != ""
	acts.Stripe = &StripeConnection{
		URL:    url("STRIPE_API_URL", "https://api.stripe.com", "/stripe", stripeOwn),
		APIKey: or(stripeOwn, env("STRIPE_API_KEY"), stripemock.APIKey),
		HTTP:   client,
	}
	snOwn := env("SERVICENOW_USER") != ""
	acts.ServiceNow = &ServiceNowConnection{
		URL:      url("SERVICENOW_URL", "", "/servicenow", snOwn),
		User:     or(snOwn, env("SERVICENOW_USER"), servicenowmock.User),
		Password: or(snOwn, env("SERVICENOW_PASSWORD"), servicenowmock.Password),
		HTTP:     client,
	}
	slackOwn := env("SLACK_BOT_TOKEN") != ""
	acts.Slack = &SlackConnection{
		URL:   url("SLACK_API_URL", "https://slack.com/api", "/slack/api", slackOwn),
		Token: or(slackOwn, env("SLACK_BOT_TOKEN"), slackmock.Token),
		HTTP:  client,
	}
	ghOwn := env("GITHUB_TOKEN") != ""
	acts.GitHub = &GitHubConnection{
		URL:   url("GITHUB_API_URL", "https://api.github.com", "/github", ghOwn),
		Token: or(ghOwn, env("GITHUB_TOKEN"), githubmock.Token),
		HTTP:  client,
	}
}

func or(own bool, value, demo string) string {
	if own {
		return value
	}
	return demo
}
