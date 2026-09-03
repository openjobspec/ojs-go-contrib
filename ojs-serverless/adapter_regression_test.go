package serverless

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testPushSecret = "test-push-secret"

func newInsecureHandler(opts ...Option) *LambdaHandler {
	opts = append(opts, WithInsecureAllowUnsignedPushForLocalDevelopment())
	return NewLambdaHandler(opts...)
}

func signPushBody(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func signedPushHeaders(body []byte) map[string]string {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	return map[string]string{
		PushTimestampHeader:  timestamp,
		PushSignatureHeader:  signPushBody(testPushSecret, timestamp, body),
		PushDeliveryIDHeader: "delivery-1",
		PushJobIDHeader:      "job-1",
	}
}

var signedPushBody = []byte(
	`{"job":{"id":"job-1","type":"email.send","queue":"default","args":[],"attempt":1},` +
		`"worker_id":"worker-1","delivery_id":"delivery-1"}`,
)

func TestHandleHTTP_PushAuthenticationAndBodyLimit(t *testing.T) {
	secure := NewLambdaHandler(WithPushSigningSecrets(testPushSecret))
	secure.Register("email.send", func(context.Context, JobEvent) error { return nil })

	unsigned := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(signedPushBody)))
	unsignedRecorder := httptest.NewRecorder()
	secure.HandleHTTP().ServeHTTP(unsignedRecorder, unsigned)
	if unsignedRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned status = %d, want %d", unsignedRecorder.Code, http.StatusUnauthorized)
	}

	headers := signedPushHeaders(signedPushBody)
	signed := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(signedPushBody)))
	for name, value := range headers {
		signed.Header.Set(name, value)
	}
	signedRecorder := httptest.NewRecorder()
	secure.HandleHTTP().ServeHTTP(signedRecorder, signed)
	if signedRecorder.Code != http.StatusOK {
		t.Fatalf("signed status = %d, want %d; body=%s", signedRecorder.Code, http.StatusOK, signedRecorder.Body.String())
	}

	limited := NewLambdaHandler(
		WithPushSigningSecrets(testPushSecret),
		WithMaxBodySize(16),
	)
	limited.Register("email.send", func(context.Context, JobEvent) error { return nil })
	limitedRequest := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(signedPushBody)))
	for name, value := range headers {
		limitedRequest.Header.Set(name, value)
	}
	limitedRecorder := httptest.NewRecorder()
	limited.HandleHTTP().ServeHTTP(limitedRecorder, limitedRequest)
	if limitedRecorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("limited status = %d, want %d", limitedRecorder.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestHandleHTTP_RejectsHeaderBodyMismatch(t *testing.T) {
	h := NewLambdaHandler(WithPushSigningSecrets(testPushSecret))
	h.Register("email.send", func(context.Context, JobEvent) error { return nil })

	headers := signedPushHeaders(signedPushBody)
	headers[PushDeliveryIDHeader] = "different-delivery"
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(signedPushBody)))
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	rec := httptest.NewRecorder()
	h.HandleHTTP().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleAPIGateway_Base64HeadersAndContext(t *testing.T) {
	h := NewLambdaHandler(WithPushSigningSecrets(testPushSecret))
	var requestID string
	var trigger TriggerType
	h.Register("email.send", func(ctx context.Context, job JobEvent) error {
		lambdaContext, ok := LambdaContextFromContext(ctx)
		if !ok {
			return errors.New("lambda context missing")
		}
		requestID = lambdaContext.RequestID
		trigger = TriggerTypeFromContext(ctx)
		return nil
	})

	headers := signedPushHeaders(signedPushBody)
	event := APIGatewayEvent{
		HTTPMethod:      http.MethodPost,
		Body:            base64.StdEncoding.EncodeToString(signedPushBody),
		IsBase64Encoded: true,
		Headers: map[string]string{
			strings.ToLower(PushTimestampHeader):  headers[PushTimestampHeader],
			strings.ToLower(PushSignatureHeader):  headers[PushSignatureHeader],
			strings.ToLower(PushDeliveryIDHeader): headers[PushDeliveryIDHeader],
			strings.ToLower(PushJobIDHeader):      headers[PushJobIDHeader],
		},
		RequestContext: APIGatewayRequestContext{RequestID: "request-123"},
	}

	resp, err := h.HandleAPIGateway(context.Background(), event)
	if err != nil {
		t.Fatalf("HandleAPIGateway() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, resp.Body)
	}
	if requestID != "request-123" || trigger != TriggerAPIGateway {
		t.Fatalf("context request ID = %q, trigger = %q", requestID, trigger)
	}

	event.Body = "%%%"
	resp, err = h.HandleAPIGateway(context.Background(), event)
	if err != nil {
		t.Fatalf("invalid base64 error = %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid base64 status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestHandleSQS_PanicIsolationConcurrencyAndContext(t *testing.T) {
	h := NewLambdaHandler(WithSQSConcurrency(3))
	var successes atomic.Int64
	h.Register("ok.job", func(ctx context.Context, job JobEvent) error {
		if TriggerTypeFromContext(ctx) != TriggerSQS {
			return errors.New("missing SQS trigger context")
		}
		successes.Add(1)
		return nil
	})
	h.Register("panic.job", func(context.Context, JobEvent) error {
		panic("boom")
	})

	body := func(id, jobType string) string {
		raw, err := json.Marshal(JobEvent{ID: id, Type: jobType, Args: json.RawMessage(`[]`)})
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		return string(raw)
	}
	event := SQSEvent{Records: []SQSMessage{
		{MessageID: "message-1", Body: body("job-1", "ok.job")},
		{MessageID: "message-2", Body: body("job-2", "panic.job")},
		{MessageID: "message-3", Body: body("job-3", "missing.job")},
		{MessageID: "message-4", Body: body("job-4", "ok.job")},
	}}

	resp, err := h.HandleSQS(context.Background(), event)
	if err != nil {
		t.Fatalf("HandleSQS() error = %v", err)
	}
	if successes.Load() != 2 {
		t.Fatalf("successful handlers = %d, want 2", successes.Load())
	}
	if len(resp.BatchItemFailures) != 2 ||
		resp.BatchItemFailures[0].ItemIdentifier != "message-2" ||
		resp.BatchItemFailures[1].ItemIdentifier != "message-3" {
		t.Fatalf("failures = %+v, want message-2 then message-3", resp.BatchItemFailures)
	}
}

func TestColdStartWarmup_IsPerHandlerAndConcurrentSafe(t *testing.T) {
	var first atomic.Int64
	var second atomic.Int64
	firstHandler := NewLambdaHandler(
		WithColdStartWarmup(func() { first.Add(1) }),
		WithDefaultHandler(func(context.Context, JobEvent) error { return nil }),
	)
	secondHandler := NewLambdaHandler(
		WithColdStartWarmup(func() { second.Add(1) }),
		WithDefaultHandler(func(context.Context, JobEvent) error { return nil }),
	)
	event := EventBridgeEvent{
		ID:         "event-1",
		Source:     "ojs.scheduler",
		DetailType: "OJS Job",
		Detail:     json.RawMessage(`{"id":"job-1","type":"any.job","args":[]}`),
	}

	done := make(chan error, 20)
	for range 10 {
		go func() { done <- firstHandler.HandleEventBridge(context.Background(), event) }()
		go func() { done <- secondHandler.HandleEventBridge(context.Background(), event) }()
	}
	for range 20 {
		if err := <-done; err != nil {
			t.Fatalf("HandleEventBridge() error = %v", err)
		}
	}
	if first.Load() != 1 || second.Load() != 1 {
		t.Fatalf("warmup counts = %d, %d; want 1, 1", first.Load(), second.Load())
	}
}

func TestHandleRaw_CountsOneInvocation(t *testing.T) {
	h := NewLambdaHandler()
	h.Register("email.send", func(context.Context, JobEvent) error { return nil })
	payload := json.RawMessage(
		`{"Records":[{"messageId":"message-1","body":"{\"id\":\"job-1\",\"type\":\"email.send\",\"args\":[]}"}]}`,
	)

	before := InvocationCount()
	if _, err := h.HandleRaw(context.Background(), payload); err != nil {
		t.Fatalf("HandleRaw() error = %v", err)
	}
	if delta := InvocationCount() - before; delta != 1 {
		t.Fatalf("invocation count delta = %d, want 1", delta)
	}
}

func TestHandlerOptionsDoNotClearExplicitPushSettings(t *testing.T) {
	h := NewLambdaHandler(
		WithPushSigningSecrets(testPushSecret),
		WithInsecureAllowUnsignedPushForLocalDevelopment(),
		WithHandlerOptions(HandlerOptions{Timeout: time.Second}),
	)

	if len(h.pushSigningSecrets) != 1 {
		t.Fatalf("push signing secrets = %d, want 1", len(h.pushSigningSecrets))
	}
	if !h.insecureAllowUnsignedPushForLocalDevelopment {
		t.Fatal("insecure local-development setting was unexpectedly cleared")
	}
}
