package serverless

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestHandleAPIGateway_Success(t *testing.T) {
	h := NewLambdaHandler()
	h.Register("email.send", func(ctx context.Context, job JobEvent) error {
		return nil
	})

	event := APIGatewayEvent{
		HTTPMethod: "POST",
		Body:       `{"job":{"id":"job-1","type":"email.send","queue":"default","args":[],"attempt":1},"worker_id":"w1","delivery_id":"d1"}`,
		RequestContext: APIGatewayRequestContext{
			RequestID: "req-123",
		},
	}

	resp, err := h.HandleAPIGateway(context.Background(), event)
	if err != nil {
		t.Fatalf("HandleAPIGateway returned error: %v", err)
	}

	if resp.StatusCode != 200 {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	var body PushDeliveryResponse
	if err := json.Unmarshal([]byte(resp.Body), &body); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}

	if body.Status != "completed" {
		t.Errorf("expected status 'completed', got '%s'", body.Status)
	}

	if resp.Headers["Content-Type"] != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", resp.Headers["Content-Type"])
	}
}

func TestHandleAPIGateway_MethodNotAllowed(t *testing.T) {
	h := NewLambdaHandler()

	event := APIGatewayEvent{
		HTTPMethod: "GET",
	}

	resp, err := h.HandleAPIGateway(context.Background(), event)
	if err != nil {
		t.Fatalf("HandleAPIGateway returned error: %v", err)
	}

	if resp.StatusCode != 405 {
		t.Errorf("expected status 405, got %d", resp.StatusCode)
	}
}

func TestHandleAPIGateway_InvalidBody(t *testing.T) {
	h := NewLambdaHandler()

	event := APIGatewayEvent{
		HTTPMethod: "POST",
		Body:       `{invalid`,
		RequestContext: APIGatewayRequestContext{
			RequestID: "req-456",
		},
	}

	resp, err := h.HandleAPIGateway(context.Background(), event)
	if err != nil {
		t.Fatalf("HandleAPIGateway returned error: %v", err)
	}

	if resp.StatusCode != 400 {
		t.Errorf("expected status 400, got %d", resp.StatusCode)
	}
}

func TestHandleAPIGateway_HandlerError(t *testing.T) {
	h := NewLambdaHandler()
	h.Register("email.send", func(ctx context.Context, job JobEvent) error {
		return errors.New("processing failed")
	})

	event := APIGatewayEvent{
		HTTPMethod: "POST",
		Body:       `{"job":{"id":"job-1","type":"email.send","queue":"default","args":[],"attempt":1},"worker_id":"w1","delivery_id":"d1"}`,
		RequestContext: APIGatewayRequestContext{
			RequestID: "req-789",
		},
	}

	resp, err := h.HandleAPIGateway(context.Background(), event)
	if err != nil {
		t.Fatalf("HandleAPIGateway returned error: %v", err)
	}

	if resp.StatusCode != 200 {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	var body PushDeliveryResponse
	if err := json.Unmarshal([]byte(resp.Body), &body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if body.Status != "failed" {
		t.Errorf("expected status 'failed', got '%s'", body.Status)
	}

	if body.Error == nil || !body.Error.Retryable {
		t.Error("expected retryable error in response")
	}
}

func TestHandleEventBridge_Success(t *testing.T) {
	h := NewLambdaHandler()

	var processedID string
	h.Register("report.generate", func(ctx context.Context, job JobEvent) error {
		processedID = job.ID
		return nil
	})

	event := EventBridgeEvent{
		ID:         "eb-event-1",
		Source:     "ojs.scheduler",
		DetailType: "OJS Job",
		Detail:     json.RawMessage(`{"id":"job-1","type":"report.generate","queue":"reports","args":[],"attempt":1}`),
	}

	err := h.HandleEventBridge(context.Background(), event)
	if err != nil {
		t.Fatalf("HandleEventBridge returned error: %v", err)
	}

	if processedID != "job-1" {
		t.Errorf("expected processed job ID 'job-1', got '%s'", processedID)
	}
}

func TestHandleEventBridge_InvalidDetail(t *testing.T) {
	h := NewLambdaHandler()

	event := EventBridgeEvent{
		ID:         "eb-event-2",
		Source:     "ojs.scheduler",
		DetailType: "OJS Job",
		Detail:     json.RawMessage(`{invalid`),
	}

	err := h.HandleEventBridge(context.Background(), event)
	if err == nil {
		t.Fatal("expected error for invalid detail")
	}
}

func TestHandleEventBridge_HandlerError(t *testing.T) {
	h := NewLambdaHandler()
	h.Register("report.generate", func(ctx context.Context, job JobEvent) error {
		return errors.New("report generation failed")
	})

	event := EventBridgeEvent{
		ID:         "eb-event-3",
		Source:     "ojs.scheduler",
		DetailType: "OJS Job",
		Detail:     json.RawMessage(`{"id":"job-1","type":"report.generate","queue":"reports","args":[],"attempt":1}`),
	}

	err := h.HandleEventBridge(context.Background(), event)
	if err == nil {
		t.Fatal("expected error from handler")
	}
}

func TestHandleRaw_DetectsSQS(t *testing.T) {
	h := NewLambdaHandler()
	h.Register("email.send", func(ctx context.Context, job JobEvent) error {
		return nil
	})

	payload := json.RawMessage(`{
		"Records": [{
			"messageId": "msg-1",
			"body": "{\"id\":\"job-1\",\"type\":\"email.send\",\"queue\":\"default\",\"args\":[],\"attempt\":1}"
		}]
	}`)

	resp, err := h.HandleRaw(context.Background(), payload)
	if err != nil {
		t.Fatalf("HandleRaw returned error: %v", err)
	}

	var sqsResp SQSBatchResponse
	if err := json.Unmarshal(resp, &sqsResp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(sqsResp.BatchItemFailures) != 0 {
		t.Errorf("expected 0 failures, got %d", len(sqsResp.BatchItemFailures))
	}
}

func TestHandleRaw_DetectsAPIGateway(t *testing.T) {
	h := NewLambdaHandler()
	h.Register("email.send", func(ctx context.Context, job JobEvent) error {
		return nil
	})

	payload := json.RawMessage(`{
		"httpMethod": "POST",
		"body": "{\"job\":{\"id\":\"job-1\",\"type\":\"email.send\",\"queue\":\"default\",\"args\":[],\"attempt\":1},\"worker_id\":\"w1\",\"delivery_id\":\"d1\"}",
		"requestContext": {"requestId": "req-1"}
	}`)

	resp, err := h.HandleRaw(context.Background(), payload)
	if err != nil {
		t.Fatalf("HandleRaw returned error: %v", err)
	}

	var apiResp APIGatewayResponse
	if err := json.Unmarshal(resp, &apiResp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if apiResp.StatusCode != 200 {
		t.Errorf("expected status 200, got %d", apiResp.StatusCode)
	}
}

func TestHandleRaw_DetectsEventBridge(t *testing.T) {
	h := NewLambdaHandler()
	h.Register("report.generate", func(ctx context.Context, job JobEvent) error {
		return nil
	})

	payload := json.RawMessage(`{
		"id": "eb-1",
		"source": "ojs.scheduler",
		"detail-type": "OJS Job",
		"detail": {"id":"job-1","type":"report.generate","queue":"reports","args":[],"attempt":1}
	}`)

	resp, err := h.HandleRaw(context.Background(), payload)
	if err != nil {
		t.Fatalf("HandleRaw returned error: %v", err)
	}

	var result map[string]string
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if result["status"] != "ok" {
		t.Errorf("expected status 'ok', got '%s'", result["status"])
	}
}

func TestHandleRaw_DirectJobPayload(t *testing.T) {
	h := NewLambdaHandler()
	h.Register("email.send", func(ctx context.Context, job JobEvent) error {
		return nil
	})

	payload := json.RawMessage(`{"id":"job-1","type":"email.send","queue":"default","args":[],"attempt":1}`)

	resp, err := h.HandleRaw(context.Background(), payload)
	if err != nil {
		t.Fatalf("HandleRaw returned error: %v", err)
	}

	var result map[string]string
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if result["job_id"] != "job-1" {
		t.Errorf("expected job_id 'job-1', got '%s'", result["job_id"])
	}
}

func TestHandleRaw_UnknownPayload(t *testing.T) {
	h := NewLambdaHandler()

	payload := json.RawMessage(`{"unknown": "format"}`)

	_, err := h.HandleRaw(context.Background(), payload)
	if err == nil {
		t.Fatal("expected error for unknown payload format")
	}
}

func TestContextPropagation_APIGateway(t *testing.T) {
	h := NewLambdaHandler()

	var capturedTrigger TriggerType
	h.Register("email.send", func(ctx context.Context, job JobEvent) error {
		capturedTrigger = TriggerTypeFromContext(ctx)
		return nil
	})

	event := APIGatewayEvent{
		HTTPMethod: "POST",
		Body:       `{"job":{"id":"job-1","type":"email.send","queue":"default","args":[],"attempt":1},"worker_id":"w1","delivery_id":"d1"}`,
		RequestContext: APIGatewayRequestContext{
			RequestID: "req-ctx-test",
		},
	}

	_, err := h.HandleAPIGateway(context.Background(), event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedTrigger != TriggerAPIGateway {
		t.Errorf("expected trigger 'api_gateway', got '%s'", capturedTrigger)
	}
}

func TestContextPropagation_EventBridge(t *testing.T) {
	h := NewLambdaHandler()

	var capturedTrigger TriggerType
	h.Register("report.generate", func(ctx context.Context, job JobEvent) error {
		capturedTrigger = TriggerTypeFromContext(ctx)
		return nil
	})

	event := EventBridgeEvent{
		ID:         "eb-ctx",
		Source:     "ojs.scheduler",
		DetailType: "OJS Job",
		Detail:     json.RawMessage(`{"id":"job-1","type":"report.generate","queue":"reports","args":[],"attempt":1}`),
	}

	if err := h.HandleEventBridge(context.Background(), event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedTrigger != TriggerEventBridge {
		t.Errorf("expected trigger 'eventbridge', got '%s'", capturedTrigger)
	}
}

func TestColdStartWarmup(t *testing.T) {
	// Reset global state for this test.
	coldStartOnce = syncOnceForTest()

	var warmupCalled atomic.Int32
	h := NewLambdaHandler(
		WithColdStartWarmup(func() {
			warmupCalled.Add(1)
		}),
	)
	h.Register("email.send", func(ctx context.Context, job JobEvent) error {
		return nil
	})

	event := EventBridgeEvent{
		ID:         "eb-warmup",
		Source:     "ojs.scheduler",
		DetailType: "OJS Job",
		Detail:     json.RawMessage(`{"id":"job-1","type":"email.send","queue":"default","args":[],"attempt":1}`),
	}

	// Invoke twice — warmup should run only once.
	_ = h.HandleEventBridge(context.Background(), event)
	_ = h.HandleEventBridge(context.Background(), event)

	if warmupCalled.Load() != 1 {
		t.Errorf("expected warmup to be called once, got %d", warmupCalled.Load())
	}
}

func TestDefaultHandler(t *testing.T) {
	var handled bool
	h := NewLambdaHandler(
		WithDefaultHandler(func(ctx context.Context, job JobEvent) error {
			handled = true
			return nil
		}),
	)

	event := EventBridgeEvent{
		ID:         "eb-default",
		Source:     "ojs.scheduler",
		DetailType: "OJS Job",
		Detail:     json.RawMessage(`{"id":"job-1","type":"unregistered.type","queue":"default","args":[],"attempt":1}`),
	}

	if err := h.HandleEventBridge(context.Background(), event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !handled {
		t.Error("expected default handler to be called")
	}
}

func TestDetectTriggerType(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    TriggerType
	}{
		{
			name:    "SQS event",
			payload: `{"Records":[{"messageId":"1","body":"{}"}]}`,
			want:    TriggerSQS,
		},
		{
			name:    "API Gateway event",
			payload: `{"httpMethod":"POST","body":"{}"}`,
			want:    TriggerAPIGateway,
		},
		{
			name:    "EventBridge event",
			payload: `{"detail-type":"OJS Job","source":"ojs","detail":{}}`,
			want:    TriggerEventBridge,
		},
		{
			name:    "Unknown event",
			payload: `{"foo":"bar"}`,
			want:    TriggerUnknown,
		},
		{
			name:    "Invalid JSON",
			payload: `{invalid`,
			want:    TriggerUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectTriggerType(json.RawMessage(tt.payload))
			if got != tt.want {
				t.Errorf("detectTriggerType() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLambdaContextFromContext_NotSet(t *testing.T) {
	_, ok := LambdaContextFromContext(context.Background())
	if ok {
		t.Error("expected ok to be false for empty context")
	}
}

func TestLambdaContextFromContext_WithValues(t *testing.T) {
	ctx := context.Background()
	ctx = context.WithValue(ctx, ctxKeyRequestID, "req-test")
	ctx = context.WithValue(ctx, ctxKeyFunctionName, "my-func")
	ctx = context.WithValue(ctx, ctxKeyInvokedARN, "arn:aws:lambda:us-east-1:123:function:my-func")
	ctx = context.WithValue(ctx, ctxKeyDeadlineMs, int64(1700000000000))

	lc, ok := LambdaContextFromContext(ctx)
	if !ok {
		t.Fatal("expected ok to be true")
	}
	if lc.RequestID != "req-test" {
		t.Errorf("expected RequestID 'req-test', got '%s'", lc.RequestID)
	}
	if lc.FunctionName != "my-func" {
		t.Errorf("expected FunctionName 'my-func', got '%s'", lc.FunctionName)
	}
	if lc.InvokedARN != "arn:aws:lambda:us-east-1:123:function:my-func" {
		t.Errorf("expected InvokedARN, got '%s'", lc.InvokedARN)
	}
	if lc.DeadlineMs != 1700000000000 {
		t.Errorf("expected DeadlineMs 1700000000000, got %d", lc.DeadlineMs)
	}
}

// syncOnceForTest returns a fresh sync.Once for testing cold start behavior.
func syncOnceForTest() syncOnce {
	return syncOnce{}
}

type syncOnce = sync.Once
