package serverless

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

const (
	// DefaultTimeout is the maximum duration for processing a job.
	DefaultTimeout = 30 * time.Second
	// DefaultMaxBodySize is the maximum decoded HTTP push body size.
	DefaultMaxBodySize int64 = 1 << 20
	// DefaultPushFreshnessWindow is the maximum accepted push timestamp skew.
	DefaultPushFreshnessWindow = 5 * time.Minute
)

// JobEvent represents an OJS job delivered to a serverless function.
type JobEvent struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Queue    string          `json:"queue"`
	Args     json.RawMessage `json:"args"`
	Attempt  int             `json:"attempt"`
	Meta     json.RawMessage `json:"meta,omitempty"`
	Priority int             `json:"priority,omitempty"`
}

// HandlerFunc is a function that processes an OJS job in a serverless context.
type HandlerFunc func(ctx context.Context, job JobEvent) error

// Option configures the LambdaHandler.
type Option func(*LambdaHandler)

// HandlerOptions contains shared execution and HTTP push settings.
type HandlerOptions struct {
	Timeout                                      time.Duration
	MaxBodySize                                  int64
	SQSConcurrency                               int
	PushSigningSecrets                           []string
	PushFreshnessWindow                          time.Duration
	InsecureAllowUnsignedPushForLocalDevelopment bool
}

// WithHandlerOptions applies shared serverless handler settings.
func WithHandlerOptions(options HandlerOptions) Option {
	return func(h *LambdaHandler) {
		if options.Timeout != 0 {
			h.timeout = options.Timeout
		}
		if options.MaxBodySize != 0 {
			h.maxBodySize = options.MaxBodySize
		}
		if options.SQSConcurrency != 0 {
			h.sqsConcurrency = options.SQSConcurrency
		}
		if len(options.PushSigningSecrets) > 0 {
			h.setPushSigningSecrets(options.PushSigningSecrets)
		}
		if options.PushFreshnessWindow != 0 {
			h.pushFreshnessWindow = options.PushFreshnessWindow
		}
		if options.InsecureAllowUnsignedPushForLocalDevelopment {
			h.insecureAllowUnsignedPushForLocalDevelopment = true
		}
	}
}

// WithOJSURL sets the OJS server URL reserved for callback operations.
func WithOJSURL(url string) Option {
	return func(h *LambdaHandler) {
		h.ojsURL = url
	}
}

// WithLogger sets a custom slog logger.
func WithLogger(logger *slog.Logger) Option {
	return func(h *LambdaHandler) {
		h.logger = logger
	}
}

// WithTimeout sets the maximum job processing duration. Zero disables it.
func WithTimeout(timeout time.Duration) Option {
	return func(h *LambdaHandler) {
		h.timeout = timeout
	}
}

// WithMaxBodySize sets the maximum decoded HTTP push body size.
func WithMaxBodySize(size int64) Option {
	return func(h *LambdaHandler) {
		h.maxBodySize = size
	}
}

// WithSQSConcurrency sets the maximum records processed concurrently. The
// default is one, preserving SQS event order.
func WithSQSConcurrency(concurrency int) Option {
	return func(h *LambdaHandler) {
		h.sqsConcurrency = concurrency
	}
}

// WithPushSigningSecrets replaces the secrets accepted for OJS push
// signatures. Multiple values support rotation without downtime.
func WithPushSigningSecrets(secrets ...string) Option {
	return func(h *LambdaHandler) {
		h.setPushSigningSecrets(secrets)
	}
}

// WithPushFreshnessWindow sets the permitted past or future timestamp skew.
func WithPushFreshnessWindow(window time.Duration) Option {
	return func(h *LambdaHandler) {
		h.pushFreshnessWindow = window
	}
}

// WithInsecureAllowUnsignedPushForLocalDevelopment disables HTTP push
// authentication. It must only be used for local development and tests.
func WithInsecureAllowUnsignedPushForLocalDevelopment() Option {
	return func(h *LambdaHandler) {
		h.insecureAllowUnsignedPushForLocalDevelopment = true
	}
}

// WithColdStartWarmup configures a warmup function that runs once per handler.
func WithColdStartWarmup(fn func()) Option {
	return func(h *LambdaHandler) {
		h.warmupFn = fn
	}
}

// WithDefaultHandler sets a fallback handler for unregistered job types.
func WithDefaultHandler(handler HandlerFunc) Option {
	return func(h *LambdaHandler) {
		h.defaultHandler = handler
	}
}

// LambdaHandler processes OJS jobs delivered via SQS, HTTP push, API Gateway,
// EventBridge, or direct Lambda invocation.
type LambdaHandler struct {
	handlers       map[string]HandlerFunc
	defaultHandler HandlerFunc
	mu             sync.RWMutex

	ojsURL         string
	logger         *slog.Logger
	timeout        time.Duration
	maxBodySize    int64
	sqsConcurrency int
	initialized    time.Time

	pushSigningSecrets                           [][]byte
	pushFreshnessWindow                          time.Duration
	insecureAllowUnsignedPushForLocalDevelopment bool

	warmupFn   func()
	warmupOnce sync.Once
	warmupErr  error
}

// NewLambdaHandler creates a new serverless handler with the given options.
func NewLambdaHandler(opts ...Option) *LambdaHandler {
	h := &LambdaHandler{
		handlers:            make(map[string]HandlerFunc),
		logger:              slog.Default(),
		timeout:             DefaultTimeout,
		maxBodySize:         DefaultMaxBodySize,
		sqsConcurrency:      1,
		initialized:         time.Now(),
		pushFreshnessWindow: DefaultPushFreshnessWindow,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(h)
		}
	}
	if h.logger == nil {
		h.logger = slog.Default()
	}
	if h.maxBodySize <= 0 {
		h.maxBodySize = DefaultMaxBodySize
	}
	if h.sqsConcurrency <= 0 {
		h.sqsConcurrency = 1
	}
	return h
}

func (h *LambdaHandler) setPushSigningSecrets(secrets []string) {
	copied := make([][]byte, 0, len(secrets))
	for _, secret := range secrets {
		if secret != "" {
			copied = append(copied, []byte(secret))
		}
	}
	h.pushSigningSecrets = copied
}

// Register associates a handler function with a job type.
func (h *LambdaHandler) Register(jobType string, handler HandlerFunc) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.handlers[jobType] = handler
}

// Initialized returns when this reusable handler instance was created.
func (h *LambdaHandler) Initialized() time.Time {
	return h.initialized
}

// OJSURL returns the configured OJS callback server URL.
func (h *LambdaHandler) OJSURL() string {
	return h.ojsURL
}

func (h *LambdaHandler) processJob(ctx context.Context, job JobEvent) error {
	if job.ID == "" || job.Type == "" {
		return fmt.Errorf("job id and type are required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	h.mu.RLock()
	handler, ok := h.handlers[job.Type]
	if !ok {
		handler = h.defaultHandler
	}
	h.mu.RUnlock()
	if handler == nil {
		return fmt.Errorf("no handler registered for job type: %s", job.Type)
	}

	if h.timeout > 0 {
		timedCtx, cancel := context.WithTimeout(ctx, h.timeout)
		defer cancel()
		ctx = timedCtx
	}
	return invokeHandler(ctx, handler, job)
}

func invokeHandler(ctx context.Context, handler HandlerFunc, job JobEvent) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic in job handler for %s: %v", job.Type, recovered)
		}
	}()
	return handler(ctx, job)
}

func (h *LambdaHandler) runWarmup() error {
	h.warmupOnce.Do(func() {
		if h.warmupFn == nil {
			return
		}
		started := time.Now()
		defer func() {
			if recovered := recover(); recovered != nil {
				h.warmupErr = fmt.Errorf("cold start warmup panic: %v", recovered)
			}
		}()
		h.warmupFn()
		h.logger.Info("cold start warmup completed",
			"duration_ms", time.Since(started).Milliseconds(),
		)
	})
	return h.warmupErr
}
