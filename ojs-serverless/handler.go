package serverless

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
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

// SQSEvent represents an AWS SQS event containing one or more messages.
type SQSEvent struct {
	Records []SQSMessage `json:"Records"`
}

// SQSMessage represents a single SQS message containing an OJS job.
type SQSMessage struct {
	MessageID     string            `json:"messageId"`
	Body          string            `json:"body"`
	Attributes    map[string]string `json:"attributes,omitempty"`
	MD5OfBody     string            `json:"md5OfBody,omitempty"`
	EventSourceID string            `json:"eventSource,omitempty"`
	ReceiptHandle string            `json:"receiptHandle,omitempty"`
}

// SQSBatchResponse is the response format for SQS batch item failures.
// Returning failed message IDs tells SQS to retry only those messages.
type SQSBatchResponse struct {
	BatchItemFailures []BatchItemFailure `json:"batchItemFailures"`
}

// BatchItemFailure identifies a single failed message in an SQS batch.
type BatchItemFailure struct {
	ItemIdentifier string `json:"itemIdentifier"`
}

// PushDeliveryRequest is the HTTP body sent by an OJS server for push delivery.
type PushDeliveryRequest struct {
	Job        JobEvent `json:"job"`
	WorkerID   string   `json:"worker_id"`
	DeliveryID string   `json:"delivery_id"`
}

// PushDeliveryResponse is the HTTP response body for push delivery.
type PushDeliveryResponse struct {
	Status string          `json:"status"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *PushError      `json:"error,omitempty"`
}

// PushError describes a job processing failure.
type PushError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

// Option configures the LambdaHandler.
type Option func(*LambdaHandler)

// WithOJSURL sets the OJS server URL for callback operations.
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

// LambdaHandler processes OJS jobs delivered via SQS or HTTP push.
type LambdaHandler struct {
	handlers map[string]HandlerFunc
	mu       sync.RWMutex
	ojsURL   string
	logger   *slog.Logger
}

// NewLambdaHandler creates a new serverless handler with the given options.
func NewLambdaHandler(opts ...Option) *LambdaHandler {
	h := &LambdaHandler{
		handlers: make(map[string]HandlerFunc),
		logger:   slog.Default(),
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Register associates a handler function with a job type.
func (h *LambdaHandler) Register(jobType string, handler HandlerFunc) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.handlers[jobType] = handler
}

// HandleSQS processes an SQS event containing OJS jobs.
// It returns partial batch failures so SQS only retries failed messages.
func (h *LambdaHandler) HandleSQS(ctx context.Context, event SQSEvent) (SQSBatchResponse, error) {
	var failures []BatchItemFailure

	for _, record := range event.Records {
		var job JobEvent
		if err := json.Unmarshal([]byte(record.Body), &job); err != nil {
			h.logger.Error("failed to unmarshal SQS message",
				"message_id", record.MessageID,
				"error", err,
			)
			failures = append(failures, BatchItemFailure{
				ItemIdentifier: record.MessageID,
			})
			continue
		}

		if err := h.processJob(ctx, job); err != nil {
			h.logger.Error("job processing failed",
				"job_id", job.ID,
				"job_type", job.Type,
				"error", err,
			)
			failures = append(failures, BatchItemFailure{
				ItemIdentifier: record.MessageID,
			})
			continue
		}

		h.logger.Info("job completed",
			"job_id", job.ID,
			"job_type", job.Type,
		)
	}

	return SQSBatchResponse{BatchItemFailures: failures}, nil
}

// HandleHTTP returns an http.HandlerFunc for OJS push delivery.
// The OJS server POSTs job payloads to this endpoint.
func (h *LambdaHandler) HandleHTTP() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req PushDeliveryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, PushDeliveryResponse{
				Status: "failed",
				Error: &PushError{
					Code:      "invalid_request",
					Message:   "failed to decode request body",
					Retryable: false,
				},
			})
			return
		}

		if err := h.processJob(r.Context(), req.Job); err != nil {
			writeJSON(w, http.StatusOK, PushDeliveryResponse{
				Status: "failed",
				Error: &PushError{
					Code:      "handler_error",
					Message:   err.Error(),
					Retryable: true,
				},
			})
			return
		}

		writeJSON(w, http.StatusOK, PushDeliveryResponse{
			Status: "completed",
		})
	}
}

func (h *LambdaHandler) processJob(ctx context.Context, job JobEvent) error {
	h.mu.RLock()
	handler, ok := h.handlers[job.Type]
	h.mu.RUnlock()

	if !ok {
		return fmt.Errorf("no handler registered for job type: %s", job.Type)
	}

	return handler(ctx, job)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
