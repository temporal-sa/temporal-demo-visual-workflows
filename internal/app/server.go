// Package app is the builder's backend: it serves the UI, keeps each user's
// flows in a library Workflow, shares templates through a gallery Workflow,
// and starts, inspects, and talks to flow runs.
package app

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/connectors"
	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
)

type Config struct {
	TaskQueue        string
	LibraryTaskQueue string
	// LibraryScope names the libraries and gallery: the demo's base task
	// queue, so a crashable workspace shares them with the demo.
	LibraryScope    string
	Namespace       string
	TemporalUIURL   string
	SandboxURL      string
	CallbackBaseURL string
	// WorkerMode is "serverless" (scales to zero) or
	// "long-lived".
	WorkerMode string
	// SharedLimits are the rate limits shared by every run (RATE_LIMITS)
	// that steps can use.
	SharedLimits     []flow.SharedLimit
	SessionSecret    []byte
	TrustAuthHeaders bool
}

type Server struct {
	cfg    Config
	tc     client.Client
	log    *slog.Logger
	static fs.FS
	// starts caches each run's start input (its flow snapshot). A run's
	// input never changes, so this is a cache, not state.
	starts sync.Map
	// started dates the built-in templates.
	started time.Time
}

func NewServer(cfg Config, tc client.Client, logger *slog.Logger, static fs.FS) *Server {
	cfg.LibraryScope = cmp.Or(cfg.LibraryScope, cfg.TaskQueue)
	return &Server{cfg: cfg, tc: tc, log: logger, static: static, started: time.Now()}
}

var (
	flowIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)
	namePattern   = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	// newFlowIDPattern is the ID a client may give a flow it creates.
	newFlowIDPattern = regexp.MustCompile(`^f-[0-9a-f]{6,16}$`)
)

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })

	mux.HandleFunc("GET /api/session", s.withUser(s.getSession))
	mux.HandleFunc("GET /api/worker", s.getWorker)

	mux.HandleFunc("GET /api/flows", s.withUser(s.listFlows))
	mux.HandleFunc("POST /api/flows", s.withUser(s.createFlow))
	mux.HandleFunc("GET /api/flows/{id}", s.withUser(s.getFlow))
	mux.HandleFunc("PUT /api/flows/{id}", s.withUser(s.saveFlow))
	mux.HandleFunc("DELETE /api/flows/{id}", s.withUser(s.deleteFlow))
	mux.HandleFunc("POST /api/flows/{id}/reset", s.withUser(s.resetFlow))
	mux.HandleFunc("POST /api/flows/validate", s.withUser(s.validateFlow))
	mux.HandleFunc("GET /api/flows/{id}/copilot", s.withUser(s.getCopilot))
	mux.HandleFunc("POST /api/flows/{id}/copilot/messages", s.withUser(s.sendCopilot))
	mux.HandleFunc("POST /api/flows/{id}/copilot/results", s.withUser(s.copilotResult))
	mux.HandleFunc("POST /api/flows/{id}/copilot/stop", s.withUser(s.stopCopilot))
	mux.HandleFunc("POST /api/flows/{id}/copilot/reset", s.withUser(s.resetCopilot))
	mux.HandleFunc("GET /api/flows/{id}/versions", s.withUser(s.listVersions))
	mux.HandleFunc("GET /api/flows/{id}/versions/{version}", s.withUser(s.getVersion))
	mux.HandleFunc("POST /api/flows/{id}/runs", s.withUser(s.startRun))
	mux.HandleFunc("GET /api/flows/{id}/runs", s.withUser(s.listFlowRuns))
	mux.HandleFunc("POST /api/flows/{id}/schedule", s.withUser(s.setSchedule))

	mux.HandleFunc("GET /api/runs", s.withUser(s.listRuns))
	mux.HandleFunc("GET /api/runs/{wid}", s.withUser(s.getRun))
	mux.HandleFunc("GET /api/runs/{wid}/details", s.withUser(s.getRunDetails))
	mux.HandleFunc("GET /api/runs/{wid}/vars", s.withUser(s.getRunVars))
	mux.HandleFunc("POST /api/runs/{wid}/signal", s.withUser(s.signalRun))
	mux.HandleFunc("POST /api/runs/{wid}/updates/{name}", s.withUser(s.updateRun))
	mux.HandleFunc("POST /api/runs/{wid}/approve", s.withUser(s.approve))
	mux.HandleFunc("POST /api/runs/{wid}/fix", s.withUser(s.fixStep))
	mux.HandleFunc("POST /api/runs/{wid}/cancel", s.withUser(s.cancelRun))
	mux.HandleFunc("GET /api/runs/{wid}/agents/{nodeId}", s.withUser(s.getAgent))
	mux.HandleFunc("POST /api/runs/{wid}/agents/{nodeId}/answer", s.withUser(s.answerAgent))

	mux.HandleFunc("GET /api/schedules", s.withUser(s.listSchedules))

	mux.HandleFunc("GET /api/templates", s.withUser(s.listTemplates))
	mux.HandleFunc("POST /api/templates", s.withUser(s.publishTemplate))
	mux.HandleFunc("GET /api/templates/{id}", s.withUser(s.getTemplate))
	mux.HandleFunc("POST /api/templates/{id}/use", s.withUser(s.useTemplate))
	mux.HandleFunc("DELETE /api/templates/{id}", s.withUser(s.unpublishTemplate))

	mux.HandleFunc("POST /api/connectors/preview", s.withUser(s.previewConnector))
	mux.HandleFunc("POST /api/transform/preview", s.withUser(s.previewTransform))
	mux.HandleFunc("GET /api/patterns", s.withUser(s.listPatterns))
	mux.HandleFunc("GET /api/patterns/{id}", s.withUser(s.getPattern))
	mux.HandleFunc("POST /api/patterns/{id}/use", s.withUser(s.usePattern))

	mux.HandleFunc("POST /hooks/{token}", s.hookStart)
	mux.HandleFunc("POST /hooks/{token}/runs/{wid}/events/{name}", s.hookEvent)
	mux.HandleFunc("POST /hooks/{token}/runs/{wid}/updates/{name}", s.hookUpdate)
	mux.HandleFunc("GET /hooks/{token}/runs/{wid}/vars", s.hookVars)
	mux.HandleFunc("POST /hooks/{token}/keys/{key}/events/{name}", s.hookKeyEvent)
	mux.HandleFunc("POST /hooks/{token}/keys/{key}/updates/{name}", s.hookKeyUpdate)

	mux.Handle("/", s.spa())
	return mux
}

type userHandler func(w http.ResponseWriter, r *http.Request, u user)

func (s *Server) withUser(h userHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h(w, r, s.identify(w, r))
	}
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request, u user) {
	writeJSON(w, http.StatusOK, map[string]any{
		"sessionKey":    u.Key,
		"displayName":   u.Name,
		"signedIn":      u.SignedIn,
		"taskQueue":     s.cfg.TaskQueue,
		"namespace":     s.cfg.Namespace,
		"temporalUiUrl": s.cfg.TemporalUIURL,
		"workerMode":    s.cfg.WorkerMode,
		"rateLimits":    s.cfg.SharedLimits,
		// The business connectors' specs, for the step forms.
		"connectors": connectors.All,
	})
}

// getWorker reports whether a flow worker is running. Workers send a
// heartbeat every few seconds (see temporalx.Dial), so a killed worker shows as
// gone within seconds. Where worker heartbeats aren't available, fall back to
// the task queue's pollers: an idle worker re-polls at least once a minute, so
// a poller older than that has gone away (slower to notice a crash). On
// serverless workers, none running is the normal idle state.
func (s *Server) getWorker(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	workers, err := s.tc.WorkflowService().ListWorkers(ctx, &workflowservice.ListWorkersRequest{
		Namespace: s.cfg.Namespace,
		Query:     fmt.Sprintf("TaskQueue = %q", s.cfg.TaskQueue),
	})
	if err == nil && len(workers.GetWorkersInfo()) > 0 {
		live := 0
		for _, info := range workers.GetWorkersInfo() {
			hb := info.GetWorkerHeartbeat()
			if hb.GetStatus() == enumspb.WORKER_STATUS_RUNNING && time.Since(hb.GetHeartbeatTime().AsTime()) < 15*time.Second {
				live++
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"mode": s.cfg.WorkerMode, "available": live > 0, "workers": live, "source": "heartbeat"})
		return
	}

	resp, err := s.tc.DescribeTaskQueue(ctx, s.cfg.TaskQueue, enumspb.TASK_QUEUE_TYPE_WORKFLOW)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"mode": s.cfg.WorkerMode, "available": false, "workers": 0, "error": err.Error()})
		return
	}
	live := 0
	for _, p := range resp.GetPollers() {
		if time.Since(p.GetLastAccessTime().AsTime()) < 70*time.Second {
			live++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"mode": s.cfg.WorkerMode, "available": live > 0, "workers": live, "source": "pollers"})
}

// eachConcurrently calls fn for 0..n-1, several at a time, and waits for all.
// For a Temporal call per item: one after another, they add up.
func eachConcurrently(n int, fn func(i int)) {
	var wg sync.WaitGroup
	limit := make(chan struct{}, 16)
	for i := range n {
		wg.Add(1)
		limit <- struct{}{}
		go func() {
			defer func() { <-limit; wg.Done() }()
			fn(i)
		}()
	}
	wg.Wait()
}

// update sends an Update and waits for its result. A validator rejection or a
// failed lane becomes 409, so the UI can show why.
func (s *Server) update(w http.ResponseWriter, r *http.Request, wid, name string, arg any) {
	// Updates need a worker to run; on serverless that may include a cold
	// start. Don't hang the request forever if none comes.
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	handle, err := s.tc.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID: wid, UpdateName: name, Args: []any{arg}, WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	var result any
	if err == nil {
		err = handle.Get(ctx, &result)
	}
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{"result": result})
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusServiceUnavailable, errors.New("no worker answered in time; try again"))
	default:
		writeError(w, http.StatusConflict, err)
	}
}

// spa serves the built UI, falling back to index.html for client routes.
func (s *Server) spa() http.Handler {
	files := http.FileServerFS(s.static)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only page loads fall back to the UI. A mistyped API or callback URL
		// must fail loudly, or a caller would think its event was delivered.
		if r.Method != http.MethodGet || strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/hooks/") {
			writeError(w, http.StatusNotFound, errors.New("not found"))
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" {
			if _, err := fs.Stat(s.static, path); err == nil {
				files.ServeHTTP(w, r)
				return
			}
		}
		http.ServeFileFS(w, r, s.static, "index.html")
	})
}

// baseURL is derived from the request, so links work on any hostname,
// including an isolated workspace's own.
func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	host := r.Host
	if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
		host = fwd
	}
	return scheme + "://" + host
}

// callbackBaseURL is the address outside systems use to call back into the
// app. CALLBACK_BASE_URL overrides the request's host, for callers that reach
// the app on an internal address (the sandbox in-cluster, in Compose).
func (s *Server) callbackBaseURL(r *http.Request) string {
	if s.cfg.CallbackBaseURL != "" {
		return strings.TrimRight(s.cfg.CallbackBaseURL, "/")
	}
	return baseURL(r)
}

func decodeBody(r *http.Request, v any) error {
	if r.ContentLength == 0 {
		return nil
	}
	err := json.NewDecoder(r.Body).Decode(v)
	if err != nil && err.Error() == "EOF" {
		return nil
	}
	return err
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	msg := err.Error()
	var appErr *temporal.ApplicationError
	if errors.As(err, &appErr) {
		msg = appErr.Message()
	}
	writeJSON(w, status, map[string]any{"error": msg})
}
