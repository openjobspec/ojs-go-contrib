package serverless

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
)

type contextKey string

const (
	ctxKeyRequestID    contextKey = "ojs.lambda.request_id"
	ctxKeyFunctionName contextKey = "ojs.lambda.function_name"
	ctxKeyTriggerType  contextKey = "ojs.lambda.trigger_type"
	ctxKeyInvokedARN   contextKey = "ojs.lambda.invoked_arn"
	ctxKeyDeadlineMs   contextKey = "ojs.lambda.deadline_ms"
)

// TriggerType identifies the Lambda event source.
type TriggerType string

const (
	TriggerSQS         TriggerType = "sqs"
	TriggerAPIGateway  TriggerType = "api_gateway"
	TriggerEventBridge TriggerType = "eventbridge"
	TriggerUnknown     TriggerType = "unknown"
)

// APIGatewayEvent represents an API Gateway REST proxy event.
type APIGatewayEvent struct {
	HTTPMethod            string                   `json:"httpMethod"`
	Path                  string                   `json:"path"`
	Body                  string                   `json:"body"`
	IsBase64Encoded       bool                     `json:"isBase64Encoded"`
	Headers               map[string]string        `json:"headers,omitempty"`
	QueryStringParameters map[string]string        `json:"queryStringParameters,omitempty"`
	RequestContext        APIGatewayRequestContext `json:"requestContext,omitempty"`
}

// APIGatewayRequestContext provides request metadata from API Gateway.
type APIGatewayRequestContext struct {
	RequestID string `json:"requestId"`
	Stage     string `json:"stage"`
	APIID     string `json:"apiId"`
}

// APIGatewayResponse is the API Gateway REST proxy response.
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

var invocationCount atomic.Int64

// HandleAPIGateway processes a signed API Gateway push delivery.
func (h *LambdaHandler) HandleAPIGateway(
	ctx context.Context,
	event APIGatewayEvent,
) (APIGatewayResponse, error) {
	ctx, err := h.beginInvocation(ctx, TriggerAPIGateway, event.RequestContext.RequestID)
	if err != nil {
		return APIGatewayResponse{}, err
	}
	return h.handleAPIGateway(ctx, event)
}

func (h *LambdaHandler) handleAPIGateway(
	ctx context.Context,
	event APIGatewayEvent,
) (APIGatewayResponse, error) {
	if !strings.EqualFold(event.HTTPMethod, http.MethodPost) {
		response, err := apiResponse(http.StatusMethodNotAllowed, pushFailure(
			pushCodeMethodNotAllowed,
			"only POST is accepted",
			false,
		))
		if err == nil {
			response.Headers["Allow"] = http.MethodPost
		}
		return response, err
	}

	rawBody := []byte(event.Body)
	if event.IsBase64Encoded {
		decoded, err := base64.StdEncoding.DecodeString(event.Body)
		if err != nil {
			return apiResponse(http.StatusBadRequest, pushFailure(
				pushCodeInvalidRequest,
				"failed to decode base64 request body",
				false,
			))
		}
		rawBody = decoded
	}
	if int64(len(rawBody)) > h.maxBodySize {
		return apiResponse(http.StatusRequestEntityTooLarge, pushFailure(
			pushCodeInvalidRequest,
			"request body exceeds the configured size limit",
			false,
		))
	}

	status, response := h.handlePush(ctx, rawBody, apiGatewayHeaders(event))
	return apiResponse(status, response)
}

// HandleEventBridge processes an EventBridge event containing an OJS job.
func (h *LambdaHandler) HandleEventBridge(ctx context.Context, event EventBridgeEvent) error {
	ctx, err := h.beginInvocation(ctx, TriggerEventBridge, event.ID)
	if err != nil {
		return err
	}
	return h.handleEventBridge(ctx, event)
}

func (h *LambdaHandler) handleEventBridge(ctx context.Context, event EventBridgeEvent) error {
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

// HandleRaw detects and routes a raw Lambda event with one invocation count.
func (h *LambdaHandler) HandleRaw(
	ctx context.Context,
	payload json.RawMessage,
) (json.RawMessage, error) {
	switch detectTriggerType(payload) {
	case TriggerSQS:
		var event SQSEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			return nil, fmt.Errorf("failed to unmarshal SQS event: %w", err)
		}
		ctx, err := h.beginInvocation(ctx, TriggerSQS, "")
		if err != nil {
			return nil, err
		}
		return json.Marshal(h.handleSQS(ctx, event))

	case TriggerAPIGateway:
		var event APIGatewayEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			return nil, fmt.Errorf("failed to unmarshal API Gateway event: %w", err)
		}
		ctx, err := h.beginInvocation(ctx, TriggerAPIGateway, event.RequestContext.RequestID)
		if err != nil {
			return nil, err
		}
		response, err := h.handleAPIGateway(ctx, event)
		if err != nil {
			return nil, err
		}
		return json.Marshal(response)

	case TriggerEventBridge:
		var event EventBridgeEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			return nil, fmt.Errorf("failed to unmarshal EventBridge event: %w", err)
		}
		ctx, err := h.beginInvocation(ctx, TriggerEventBridge, event.ID)
		if err != nil {
			return nil, err
		}
		if err := h.handleEventBridge(ctx, event); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]string{"status": "ok"})

	default:
		var job JobEvent
		if err := json.Unmarshal(payload, &job); err != nil {
			return nil, fmt.Errorf("unable to detect trigger type or parse as job: %w", err)
		}
		if job.Type == "" || job.ID == "" {
			return nil, fmt.Errorf("unable to detect trigger type: payload missing 'type' and 'id' fields")
		}
		ctx, err := h.beginInvocation(ctx, TriggerUnknown, job.ID)
		if err != nil {
			return nil, err
		}
		if err := h.processJob(ctx, job); err != nil {
			return nil, fmt.Errorf("job %s processing failed: %w", job.ID, err)
		}
		return json.Marshal(map[string]string{"status": "ok", "job_id": job.ID})
	}
}

// InvocationCount returns total Lambda-style invocations in this process.
func InvocationCount() int64 {
	return invocationCount.Load()
}

// LambdaContextFromContext extracts Lambda metadata from a context.
func LambdaContextFromContext(ctx context.Context) (LambdaContext, bool) {
	if ctx == nil {
		return LambdaContext{}, false
	}
	requestID, _ := ctx.Value(ctxKeyRequestID).(string)
	if requestID == "" {
		return LambdaContext{}, false
	}
	return LambdaContext{
		RequestID:    requestID,
		FunctionName: contextString(ctx, ctxKeyFunctionName),
		InvokedARN:   contextString(ctx, ctxKeyInvokedARN),
		DeadlineMs:   contextInt64(ctx, ctxKeyDeadlineMs),
	}, true
}

// TriggerTypeFromContext returns the trigger type from the context.
func TriggerTypeFromContext(ctx context.Context) TriggerType {
	if ctx != nil {
		if value, ok := ctx.Value(ctxKeyTriggerType).(TriggerType); ok {
			return value
		}
	}
	return TriggerUnknown
}

func (h *LambdaHandler) beginInvocation(
	ctx context.Context,
	trigger TriggerType,
	requestID string,
) (context.Context, error) {
	invocationCount.Add(1)
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = h.enrichContext(ctx, trigger, requestID)
	if err := h.runWarmup(); err != nil {
		return ctx, err
	}
	return ctx, nil
}

func (h *LambdaHandler) enrichContext(
	ctx context.Context,
	trigger TriggerType,
	requestID string,
) context.Context {
	ctx = context.WithValue(ctx, ctxKeyTriggerType, trigger)
	if requestID != "" {
		ctx = context.WithValue(ctx, ctxKeyRequestID, requestID)
	}
	if functionName := os.Getenv("AWS_LAMBDA_FUNCTION_NAME"); functionName != "" {
		ctx = context.WithValue(ctx, ctxKeyFunctionName, functionName)
	}
	if invokedARN := os.Getenv("AWS_LAMBDA_FUNCTION_INVOKED_ARN"); invokedARN != "" {
		ctx = context.WithValue(ctx, ctxKeyInvokedARN, invokedARN)
	}
	if deadline, ok := ctx.Deadline(); ok {
		ctx = context.WithValue(ctx, ctxKeyDeadlineMs, deadline.UnixMilli())
	}
	return ctx
}

func detectTriggerType(payload json.RawMessage) TriggerType {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return TriggerUnknown
	}
	if records, ok := fields["Records"]; ok {
		var decoded []json.RawMessage
		if json.Unmarshal(records, &decoded) == nil {
			return TriggerSQS
		}
	}
	if method, ok := fields["httpMethod"]; ok {
		var decoded string
		if json.Unmarshal(method, &decoded) == nil && decoded != "" {
			return TriggerAPIGateway
		}
	}
	if _, detail := fields["detail"]; detail {
		if _, detailType := fields["detail-type"]; detailType {
			return TriggerEventBridge
		}
		if _, source := fields["source"]; source {
			return TriggerEventBridge
		}
	}
	return TriggerUnknown
}

func apiGatewayHeaders(event APIGatewayEvent) http.Header {
	headers := make(http.Header)
	for name, value := range event.Headers {
		headers.Set(name, value)
	}
	return headers
}

func apiResponse(statusCode int, body any) (APIGatewayResponse, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return APIGatewayResponse{}, fmt.Errorf("encoding API Gateway response: %w", err)
	}
	return APIGatewayResponse{
		StatusCode: statusCode,
		Headers: map[string]string{
			"Content-Type": "application/json",
		},
		Body: string(payload),
	}, nil
}

func contextString(ctx context.Context, key contextKey) string {
	if value, ok := ctx.Value(key).(string); ok {
		return value
	}
	return ""
}

func contextInt64(ctx context.Context, key contextKey) int64 {
	if value, ok := ctx.Value(key).(int64); ok {
		return value
	}
	return 0
}
