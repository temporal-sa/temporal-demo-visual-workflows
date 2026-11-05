// Command app serves the builder UI and API. It also polls the library task
// queue, so saving and loading flows keeps working while the flow worker is
// down. It never executes flow runs.
package main

import (
	"crypto/rand"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"go.temporal.io/sdk/worker"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/app"
	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/temporalx"
	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/workflows"
	"github.com/temporal-sa/temporal-demo-visual-workflows/web"
)

func main() {
	logger := temporalx.Logger()
	c, err := temporalx.Dial(logger)
	if err != nil {
		logger.Error("temporal client", "error", err)
		os.Exit(1)
	}
	defer c.Close()

	secret := []byte(os.Getenv("SESSION_SECRET"))
	if len(secret) == 0 {
		// Fine for one local process. Hosted replicas must share a secret,
		// or a session cookie signed by one replica is rejected by another.
		logger.Warn("SESSION_SECRET is not set; using a random per-process secret")
		secret = make([]byte, 32)
		_, _ = rand.Read(secret)
	}
	namespace := os.Getenv("TEMPORAL_NAMESPACE")
	if namespace == "" {
		namespace = "default"
	}
	limits, err := temporalx.SharedLimits()
	if err != nil {
		logger.Error("rate limits", "error", err)
		os.Exit(1)
	}
	cfg := app.Config{
		SharedLimits:     limits,
		TaskQueue:        temporalx.TaskQueue(),
		LibraryTaskQueue: temporalx.LibraryTaskQueue(),
		LibraryScope:     temporalx.BaseTaskQueue(),
		Namespace:        namespace,
		TemporalUIURL:    strings.TrimRight(envOr("TEMPORAL_UI_URL", "http://localhost:8233"), "/"),
		UINamespace:      envOr("TEMPORAL_UI_NAMESPACE", namespace),
		SandboxURL:       envOr("SANDBOX_URL", "http://localhost:8090"),
		CallbackBaseURL:  os.Getenv("CALLBACK_BASE_URL"),
		WorkerMode:       envOr("WORKER_MODE", "long-lived"),
		SessionSecret:    secret,
		TrustAuthHeaders: os.Getenv("TRUST_AUTH_HEADERS") == "true",
	}

	// Start the library worker in the background and keep retrying, so the UI
	// and health check are served even while Temporal is unreachable.
	go func() {
		for {
			lib := worker.New(c, cfg.LibraryTaskQueue, worker.Options{})
			lib.RegisterWorkflow(workflows.LibraryWorkflow)
			lib.RegisterWorkflow(workflows.GalleryWorkflow)
			err := lib.Start()
			if err == nil {
				return
			}
			logger.Warn("library worker could not start; retrying", "error", err)
			time.Sleep(5 * time.Second)
		}
	}()

	addr := envOr("APP_ADDR", ":8080")
	srv := &http.Server{
		Addr:              addr,
		Handler:           app.NewServer(cfg, c, logger, web.Build()).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	logger.Info("app listening", "addr", addr, "task_queue", cfg.TaskQueue, "library_task_queue", cfg.LibraryTaskQueue)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("app stopped", "error", err)
		os.Exit(1)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
