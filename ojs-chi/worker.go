package ojschi

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	ojs "github.com/openjobspec/ojs-go-sdk"
)

// WorkerManager manages the lifecycle of an OJS worker alongside a Chi server.
type WorkerManager struct {
	worker  *ojs.Worker
	options WorkerOptions
}

// WorkerOptions configures the OJS worker.
type WorkerOptions struct {
	// URL of the OJS server.
	URL string
	// Queues to process (default: ["default"]).
	Queues []string
	// Concurrency is the number of concurrent job processors.
	Concurrency int
	// PollInterval in milliseconds between polling for jobs.
	PollInterval int
	// ShutdownTimeout in seconds for graceful shutdown.
	ShutdownTimeout int
}

// JobHandlerFunc is the signature for job handler functions.
type JobHandlerFunc func(ctx context.Context, job *ojs.JobContext) error

// NewWorkerManager creates a new worker manager with the given options.
func NewWorkerManager(opts WorkerOptions) *WorkerManager {
	if len(opts.Queues) == 0 {
		opts.Queues = []string{"default"}
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 10
	}
	return &WorkerManager{options: opts}
}

// Register registers a handler for a specific job type.
// Must be called before Start.
func (wm *WorkerManager) Register(jobType string, handler JobHandlerFunc) {
	if wm.worker == nil {
		wm.worker = ojs.NewWorker(ojs.WorkerConfig{
			URL:          wm.options.URL,
			Queues:       wm.options.Queues,
			Concurrency:  wm.options.Concurrency,
			PollInterval: wm.options.PollInterval,
		})
	}
	wm.worker.Register(jobType, ojs.HandlerFunc(func(ctx *ojs.JobContext) error {
		return handler(ctx.Context(), ctx)
	}))
}

// Start begins processing jobs. This is a blocking call.
// Use StartAsync for non-blocking operation.
func (wm *WorkerManager) Start(ctx context.Context) error {
	if wm.worker == nil {
		return fmt.Errorf("ojschi: no handlers registered; call Register before Start")
	}
	return wm.worker.Start(ctx)
}

// StartAsync starts the worker in a goroutine and returns immediately.
// The worker will stop when the context is cancelled.
func (wm *WorkerManager) StartAsync(ctx context.Context) error {
	go func() {
		if err := wm.Start(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "ojschi: worker error: %v\n", err)
		}
	}()
	return nil
}

// Stop gracefully shuts down the worker.
func (wm *WorkerManager) Stop() error {
	if wm.worker == nil {
		return nil
	}
	return wm.worker.Stop()
}

// HealthHandler returns an http.HandlerFunc that reports worker health.
// Returns 200 if the worker is running, 503 otherwise.
// Compatible with Chi route handlers.
func (wm *WorkerManager) HealthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if wm.worker != nil {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"healthy","worker":"running"}`))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"status":"unhealthy","worker":"not started"}`))
	}
}

// GracefulShutdown sets up signal handling for graceful shutdown of both
// the Chi server and OJS worker. Returns a context that is cancelled on
// SIGTERM or SIGINT.
func GracefulShutdown() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
		<-sigCh
		cancel()
	}()
	return ctx, cancel
}

