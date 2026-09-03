package ojschi

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	ojs "github.com/openjobspec/ojs-go-sdk"
)

// WorkerManager manages the lifecycle of an OJS worker alongside a Chi server.
type WorkerManager struct {
	worker        *ojs.Worker
	options       WorkerOptions
	workerOptions []ojs.WorkerOption

	mu      sync.RWMutex
	cancel  context.CancelFunc
	done    chan struct{}
	runErr  error
	running bool
	started bool
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
	return NewWorkerManagerWithSDKOptions(opts)
}

// NewWorkerManagerWithSDKOptions creates a worker manager and appends SDK
// worker options after the framework defaults. This supports SDK features such
// as worker authentication and custom HTTP transports without changing the
// stable WorkerOptions struct.
func NewWorkerManagerWithSDKOptions(opts WorkerOptions, sdkOpts ...ojs.WorkerOption) *WorkerManager {
	if len(opts.Queues) == 0 {
		opts.Queues = []string{"default"}
	} else {
		opts.Queues = append([]string(nil), opts.Queues...)
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 10
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 1000
	}
	if opts.ShutdownTimeout <= 0 {
		opts.ShutdownTimeout = 30
	}
	return &WorkerManager{
		options:       opts,
		workerOptions: append([]ojs.WorkerOption(nil), sdkOpts...),
	}
}

// Register registers a handler for a specific job type.
// Must be called before Start.
func (wm *WorkerManager) Register(jobType string, handler JobHandlerFunc) {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	if wm.worker == nil {
		var opts []ojs.WorkerOption
		opts = append(opts,
			ojs.WithQueues(wm.options.Queues...),
			ojs.WithConcurrency(wm.options.Concurrency),
			ojs.WithPollInterval(time.Duration(wm.options.PollInterval)*time.Millisecond),
			ojs.WithGracePeriod(time.Duration(wm.options.ShutdownTimeout)*time.Second),
		)
		opts = append(opts, wm.workerOptions...)
		wm.worker = ojs.NewWorker(wm.options.URL, opts...)
	}
	wm.worker.Register(jobType, func(ctx ojs.JobContext) error {
		return handler(ctx.Context(), &ctx)
	})
}

// Start begins processing jobs. This is a blocking call.
// Use StartAsync for non-blocking operation.
func (wm *WorkerManager) Start(ctx context.Context) error {
	worker, runCtx, done, err := wm.prepareStart(ctx)
	if err != nil {
		return err
	}
	return wm.run(worker, runCtx, done)
}

// StartAsync starts the worker in a goroutine and returns immediately.
// The worker will stop when the context is cancelled.
func (wm *WorkerManager) StartAsync(ctx context.Context) error {
	worker, runCtx, done, err := wm.prepareStart(ctx)
	if err != nil {
		return err
	}
	go func() {
		_ = wm.run(worker, runCtx, done)
	}()
	return nil
}

// Stop gracefully shuts down the worker.
func (wm *WorkerManager) Stop() error {
	wm.mu.RLock()
	if !wm.started {
		wm.mu.RUnlock()
		return nil
	}
	cancel := wm.cancel
	done := wm.done
	timeout := time.Duration(wm.options.ShutdownTimeout) * time.Second
	wm.mu.RUnlock()

	if cancel != nil {
		cancel()
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return wm.Err()
	case <-timer.C:
		return fmt.Errorf("ojschi: worker shutdown exceeded %s", timeout)
	}
}

// Wait blocks until a worker started with StartAsync exits and returns its
// terminal error.
func (wm *WorkerManager) Wait() error {
	wm.mu.RLock()
	if !wm.started {
		wm.mu.RUnlock()
		return fmt.Errorf("ojschi: worker has not been started")
	}
	done := wm.done
	wm.mu.RUnlock()

	<-done
	return wm.Err()
}

// Err returns the worker's terminal error after it exits.
func (wm *WorkerManager) Err() error {
	wm.mu.RLock()
	defer wm.mu.RUnlock()
	return wm.runErr
}

// HealthHandler returns an http.HandlerFunc that reports worker health.
// Returns 200 if the worker is running, 503 otherwise.
// Compatible with Chi route handlers.
func (wm *WorkerManager) HealthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if wm.isRunning() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"healthy","worker":"running"}`))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"unhealthy","worker":"not started"}`))
	}
}

// GracefulShutdown sets up signal handling for graceful shutdown of both
// the Chi server and OJS worker. Returns a context that is cancelled on
// SIGTERM or SIGINT.
func GracefulShutdown() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func (wm *WorkerManager) prepareStart(ctx context.Context) (*ojs.Worker, context.Context, chan struct{}, error) {
	if ctx == nil {
		return nil, nil, nil, fmt.Errorf("ojschi: context must not be nil")
	}

	wm.mu.Lock()
	defer wm.mu.Unlock()
	if wm.worker == nil {
		return nil, nil, nil, fmt.Errorf("ojschi: no handlers registered; call Register before Start")
	}
	if wm.started {
		return nil, nil, nil, fmt.Errorf("ojschi: worker has already been started")
	}

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	wm.cancel = cancel
	wm.done = done
	wm.runErr = nil
	wm.running = true
	wm.started = true
	return wm.worker, runCtx, done, nil
}

func (wm *WorkerManager) run(worker *ojs.Worker, ctx context.Context, done chan struct{}) error {
	err := worker.Start(ctx)

	wm.mu.Lock()
	wm.runErr = err
	wm.running = false
	wm.cancel = nil
	close(done)
	wm.mu.Unlock()
	return err
}

func (wm *WorkerManager) isRunning() bool {
	wm.mu.RLock()
	defer wm.mu.RUnlock()
	return wm.running
}
