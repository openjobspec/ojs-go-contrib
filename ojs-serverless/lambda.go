package serverless

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// contextKey is an unexported type used for context values.
type contextKey string

const (
	// Context keys for Lambda metadata.
	ctxKeyRequestID   contextKey = "ojs.lambda.request_id"
	ctxKeyFunctionName contextKey = "ojs.lambda.function_name"
	ctxKeyTriggerType  contextKey = "ojs.lambda.trigger_type"
	ctxKeyInvokedARN   contextKey = "ojs.lambda.invoked_arn"
	ctxKeyDeadlineMs   contextKey = "ojs.lambda.deadline_ms"
)

// TriggerType identifies the Lambda event source.
type TriggerType string

const (
	TriggerSQS          TriggerType = "sqs"
	TriggerAPIGateway   TriggerType = "api_gateway"
	TriggerEventBridge  TriggerType = "eventbridge"
	TriggerUnknown      TriggerType = "unknown"
)

// APIGatewayEvent represents an API Gateway proxy event containing an OJS job.
type APIGatewayEvent struct {
	HTTPMethod            string                       `json:"httpMethod"`
	Path                  string                       `json:"path"`
	Body                  string                       `json:"body"`
	IsBase64Encoded       bool                         `json:"isBase64Encoded"`
	Headers               map[string]string            `json:"headers,omitempty"`
	QueryStringParameters map[string]string            `json:"queryStringParameters,omitempty"`
	RequestContext        APIGatewayRequestContext      `json:"requestContext,omitempty"`
}

// APIGatewayRequestContext provides request metadata from API Gateway.
type APIGatewayRequestContext struct {
	RequestID string `json:"requestId"`
	Stage     string `json:"stage"`
	APIID     string `json:"apiId"`
}

// APIGatewayResponse is the response format for API Gateway proxy integration.
type APIGatewayResponse struct {
	StatusCode int               `json:"statusCode"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body"`
}

// EventBridgeEvent represents an EventBridge event delivering an OJS job.
type EventBridgeEvent struct {
	Version    string          `json:"version"`
	ID         string          `json:"id"`
	Source     string          `json:"source"`
	DetailType string          `json:"detail-type"`
	Detail     json.RawMessage `json:"detail"`
	Account    string          `json:"account,omitempty"`
	Region     string          `json:"region,omitempty"`
	Time       string          `json:"time,omitempty"`
	Resources  []string        `json:"resources,omitempty"`
}

// LambdaContext holds metadata from the Lambda execution environment.
type LambdaContext struct {
	RequestID    string
	FunctionName string
	InvokedARN   string
	DeadlineMs   int64
}

// WithColdStartWarmup configures a warmup handler that runs on first invocation
// to pre-initialize resources (DB connections, HTTP clients, etc).
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

// coldStartOnce ensures warmup runs exactly once.
var coldStartOnce sync.Once

// invocationCount tracks total invocations for observability.
var invocationCount atomic.Int64

// HandleAPIGateway processes an API Gateway proxy event containing an OJS job.
// The job payload is expected in the request body as a PushDeliveryRequest.
func (h *LambdaHandler) HandleAPIGateway(ctx context.Context, event APIGatewayEvent) (APIGatewayResponse, error) {
	h.runWarmup()
	invocationCount.Add(1)

	ctx = h.enrichContext(ctx, TriggerAPIGateway, event.RequestContext.RequestID)

	if event.HTTPMethod != "POST" {
		return apiResponse(405, PushDeliveryResponse{
			Status: "failed",
			Error: &PushError{
				Code:      "method_not_allowed",
				Message:   "only POST is accepted",
				Retryable: false,
			},
		}), nil
	}

	var req PushDeliveryRequest
	if err := json.Unmarshal([]byte(event.Body), &req); err != nil {
		h.logger.Error("failed to decode API Gateway body",
			"request_id", event.RequestContext.RequestID,
			"error", err,
		)
		return apiResponse(400, PushDeliveryResponse{
			Status: "failed",
			Error: &PushError{
				Code:      "invalid_request",
				Message:   "failed to decode request body",
				Retryable: false,
			},
		}), nil
	}

	if err := h.processJob(ctx, req.Job); err != nil {
		h.logger.Error("job processing failed",
			"job_id", req.Job.ID,
			"job_type", req.Job.Type,
			"trigger", TriggerAPIGateway,
			"error", err,
		)
		return apiResponse(200, PushDeliveryResponse{
			Status: "failed",
			Error: &PushError{
				Code:      "handler_error",
				Message:   err.Error(),
				Retryable: true,
			},
		}), nil
	}

	h.logger.Info("job completed",
		"job_id", req.Job.ID,
		"job_type", req.Job.Type,
		"trigger", TriggerAPIGateway,
	)

	return apiResponse(200, PushDeliveryResponse{
		Status: "completed",
	}), nil
}

// HandleEventBridge processes an EventBridge event containing an OJS job.
// The job payload is expected in the event's Detail field.
func (h *LambdaHandler) HandleEventBridge(ctx context.Context, event EventBridgeEvent) error {
	h.runWarmup()
	invocationCount.Add(1)

	ctx = h.enrichContext(ctx, TriggerEventBridge, event.ID)

	var job JobEvent
	if err := json.Unmarshal(event.Detail, &job); err != nil {
		h.logger.Error("failed to unmarshal EventBridge detail",
			"event_id", event.ID,
			"source", event.Source,
			"error", err,
		)
		return fmt.Errorf("invalid EventBridge detail: %w", err)
	}

	if err := h.processJob(ctx, job); err != nil {
		h.logger.Error("job processing failed",
			"job_id", job.ID,
			"job_type", job.Type,
			"event_id", event.ID,
			"trigger", TriggerEventBridge,
			"error", err,
		)
		return fmt.Errorf("job %s processing failed: %w", job.ID, err)
	}

	h.logger.Info("job completed",
		"job_id", job.ID,
		"job_type", job.Type,
		"event_id", event.ID,
		"trigger", TriggerEventBridge,
	)

	return nil
}

// HandleRaw detects the trigger type from a raw Lambda event payload and
// routes to the appropriate handler. This is useful when a single Lambda
// receives events from multiple sources.
func (h *LambdaHandler) HandleRaw(ctx context.Context, payload json.RawMessage) (json.RawMessage, error) {
	h.runWarmup()
	invocationCount.Add(1)

	triggerType := detectTriggerType(payload)
	ctx = h.enrichContext(ctx, triggerType, "")

	h.logger.Debug("detected trigger type",
		"trigger", triggerType,
	)

	switch triggerType {
	case TriggerSQS:
		var event SQSEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			return nil, fmt.Errorf("failed to unmarshal SQS event: %w", err)
		}
		resp, err := h.HandleSQS(ctx, event)
		if err != nil {
			return nil, err
		}
		return json.Marshal(resp)

	case TriggerAPIGateway:
		var event APIGatewayEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			return nil, fmt.Errorf("failed to unmarshal API Gateway event: %w", err)
		}
		resp, err := h.HandleAPIGateway(ctx, event)
		if err != nil {
			return nil, err
		}
		return json.Marshal(resp)

	case TriggerEventBridge:
		var event EventBridgeEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			return nil, fmt.Errorf("failed to unmarshal EventBridge event: %w", err)
		}
		err := h.HandleEventBridge(ctx, event)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]string{"status": "ok"})

	default:
		// Attempt to treat it as a direct job payload.
		var job JobEvent
		if err := json.Unmarshal(payload, &job); err != nil {
			return nil, fmt.Errorf("unable to detect trigger type or parse as job: %w", err)
		}
		if job.Type == "" || job.ID == "" {
			return nil, fmt.Errorf("unable to detect trigger type: payload missing 'type' and 'id' fields")
		}
		if err := h.processJob(ctx, job); err != nil {
			return nil, fmt.Errorf("job %s processing failed: %w", job.ID, err)
		}
		return json.Marshal(map[string]string{"status": "ok", "job_id": job.ID})
	}
}

// InvocationCount returns the total number of invocations since cold start.
func InvocationCount() int64 {
	return invocationCount.Load()
}

// LambdaContextFromContext extracts Lambda metadata from a context.
func LambdaContextFromContext(ctx context.Context) (LambdaContext, bool) {
	reqID, _ := ctx.Value(ctxKeyRequestID).(string)
	if reqID == "" {
		return LambdaContext{}, false
	}
	return LambdaContext{
		RequestID:    reqID,
		FunctionName: contextString(ctx, ctxKeyFunctionName),
		InvokedARN:   contextString(ctx, ctxKeyInvokedARN),
		DeadlineMs:   contextInt64(ctx, ctxKeyDeadlineMs),
	}, true
}

// TriggerTypeFromContext returns the trigger type from the context.
func TriggerTypeFromContext(ctx context.Context) TriggerType {
	if v, ok := ctx.Value(ctxKeyTriggerType).(TriggerType); ok {
		return v
	}
	return TriggerUnknown
}

// enrichContext adds Lambda execution metadata to the context.
func (h *LambdaHandler) enrichContext(ctx context.Context, trigger TriggerType, requestID string) context.Context {
	ctx = context.WithValue(ctx, ctxKeyTriggerType, trigger)
	if requestID != "" {
		ctx = context.WithValue(ctx, ctxKeyRequestID, requestID)
	}
	if deadline, ok := ctx.Deadline(); ok {
		ctx = context.WithValue(ctx, ctxKeyDeadlineMs, deadline.UnixMilli())
	}
	return ctx
}

// runWarmup executes the cold start warmup function exactly once.
func (h *LambdaHandler) runWarmup() {
	if h.warmupFn != nil {
		coldStartOnce.Do(func() {
			start := time.Now()
			h.warmupFn()
			h.logger.Info("cold start warmup completed",
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

// detectTriggerType inspects a raw payload to determine the event source.
func detectTriggerType(payload json.RawMessage) TriggerType {
	var probe struct {
		Records    json.RawMessage `json:"Records"`
		HTTPMethod string          `json:"httpMethod"`
		DetailType string          `json:"detail-type"`
		Source     string          `json:"source"`
	}
	if err := json.Unmarshal(payload, &probe); err != nil {
		return TriggerUnknown
	}

	if len(probe.Records) > 0 {
		return TriggerSQS
	}
	if probe.HTTPMethod != "" {
		return TriggerAPIGateway
	}
	if probe.DetailType != "" || probe.Source != "" {
		return TriggerEventBridge
	}
	return TriggerUnknown
}

// apiResponse builds an APIGatewayResponse with JSON body.
func apiResponse(statusCode int, body any) APIGatewayResponse {
	b, _ := json.Marshal(body)
	return APIGatewayResponse{
		StatusCode: statusCode,
		Headers: map[string]string{
			"Content-Type": "application/json",
		},
		Body: string(b),
	}
}

func contextString(ctx context.Context, key contextKey) string {
	if v, ok := ctx.Value(key).(string); ok {
		return v
	}
	return ""
}

func contextInt64(ctx context.Context, key contextKey) int64 {
	if v, ok := ctx.Value(key).(int64); ok {
		return v
	}
	return 0
}
