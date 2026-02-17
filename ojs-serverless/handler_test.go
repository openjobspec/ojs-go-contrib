package serverless

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleSQS_Success(t *testing.T) {
	h := NewLambdaHandler()

	var processedIDs []string
	h.Register("email.send", func(ctx context.Context, job JobEvent) error {
		processedIDs = append(processedIDs, job.ID)
		return nil
	})

	event := SQSEvent{
		Records: []SQSMessage{
			{
				MessageID: "msg-1",
				Body:      `{"id":"job-1","type":"email.send","queue":"default","args":[{"to":"a@b.com"}],"attempt":1}`,
			},
			{
				MessageID: "msg-2",
				Body:      `{"id":"job-2","type":"email.send","queue":"default","args":[{"to":"c@d.com"}],"attempt":1}`,
			},
		},
	}

	resp, err := h.HandleSQS(context.Background(), event)
	if err != nil {
		t.Fatalf("HandleSQS returned error: %v", err)
	}

	if len(resp.BatchItemFailures) != 0 {
		t.Errorf("expected 0 failures, got %d", len(resp.BatchItemFailures))
	}

	if len(processedIDs) != 2 {
		t.Errorf("expected 2 processed jobs, got %d", len(processedIDs))
	}
}

func TestHandleSQS_PartialFailure(t *testing.T) {
	h := NewLambdaHandler()

	h.Register("email.send", func(ctx context.Context, job JobEvent) error {
		return nil
	})
	// No handler for "unknown.type" — will fail

	event := SQSEvent{
		Records: []SQSMessage{
			{
				MessageID: "msg-1",
				Body:      `{"id":"job-1","type":"email.send","queue":"default","args":[],"attempt":1}`,
			},
			{
				MessageID: "msg-2",
				Body:      `{"id":"job-2","type":"unknown.type","queue":"default","args":[],"attempt":1}`,
			},
		},
	}

	resp, err := h.HandleSQS(context.Background(), event)
	if err != nil {
		t.Fatalf("HandleSQS returned error: %v", err)
	}

	if len(resp.BatchItemFailures) != 1 {
		t.Fatalf("expected 1 failure, got %d", len(resp.BatchItemFailures))
	}

	if resp.BatchItemFailures[0].ItemIdentifier != "msg-2" {
		t.Errorf("expected failed message msg-2, got %s", resp.BatchItemFailures[0].ItemIdentifier)
	}
}

func TestHandleSQS_InvalidJSON(t *testing.T) {
	h := NewLambdaHandler()

	event := SQSEvent{
		Records: []SQSMessage{
			{
				MessageID: "msg-1",
				Body:      `{invalid json`,
			},
		},
	}

	resp, err := h.HandleSQS(context.Background(), event)
	if err != nil {
		t.Fatalf("HandleSQS returned error: %v", err)
	}

	if len(resp.BatchItemFailures) != 1 {
		t.Errorf("expected 1 failure for invalid JSON, got %d", len(resp.BatchItemFailures))
	}
}

func TestHandleHTTP_Success(t *testing.T) {
	h := NewLambdaHandler()
	h.Register("email.send", func(ctx context.Context, job JobEvent) error {
		return nil
	})

	body := `{"job":{"id":"job-1","type":"email.send","queue":"default","args":[],"attempt":1},"worker_id":"w1","delivery_id":"d1"}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleHTTP().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp PushDeliveryResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "completed" {
		t.Errorf("expected status 'completed', got '%s'", resp.Status)
	}
}

func TestHandleHTTP_HandlerError(t *testing.T) {
	h := NewLambdaHandler()
	// No handler registered

	body := `{"job":{"id":"job-1","type":"unknown.type","queue":"default","args":[],"attempt":1},"worker_id":"w1","delivery_id":"d1"}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleHTTP().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp PushDeliveryResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "failed" {
		t.Errorf("expected status 'failed', got '%s'", resp.Status)
	}

	if resp.Error == nil {
		t.Fatal("expected error in response")
	}

	if !resp.Error.Retryable {
		t.Error("expected retryable error")
	}
}

func TestHandleHTTP_MethodNotAllowed(t *testing.T) {
	h := NewLambdaHandler()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	h.HandleHTTP().ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", w.Code)
	}
}

func TestJobEvent_ArgsDeserialization(t *testing.T) {
	raw := `{"id":"j1","type":"test","queue":"q","args":[{"key":"value"}],"attempt":1,"priority":5}`
	var job JobEvent
	if err := json.Unmarshal([]byte(raw), &job); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if job.ID != "j1" {
		t.Errorf("expected id j1, got %s", job.ID)
	}
	if job.Priority != 5 {
		t.Errorf("expected priority 5, got %d", job.Priority)
	}
}
