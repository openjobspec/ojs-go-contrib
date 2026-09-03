package ojschi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ojs "github.com/openjobspec/ojs-go-sdk"
)

func TestMiddleware_NilClientIsMissing(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if client, ok := ClientFromRequest(r); ok || client != nil {
			t.Fatalf("ClientFromRequest() = %v, %v; want nil, false", client, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	})

	rec := httptest.NewRecorder()
	Middleware(nil)(next).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestHealthCheckHandler_NilClient(t *testing.T) {
	rec := httptest.NewRecorder()
	HealthCheckHandler(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if contentType := rec.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", contentType)
	}
}

func TestRegisterCrons_RejectsNilClientAndWhitespace(t *testing.T) {
	valid := []CronConfig{{Name: "daily", Schedule: "0 0 * * *", JobType: "daily.run"}}
	if err := RegisterCrons(context.Background(), nil, valid); err == nil {
		t.Fatal("RegisterCrons() error = nil, want nil-client error")
	}

	err := RegisterCrons(context.Background(), nil, []CronConfig{{
		Name: " ", Schedule: "0 0 * * *", JobType: "daily.run",
	}})
	if err == nil || !strings.Contains(err.Error(), "cron 0") {
		t.Fatalf("RegisterCrons() error = %v, want indexed validation error", err)
	}
}

func TestWorkerHealth_RegisteredButNotStarted(t *testing.T) {
	wm := NewWorkerManager(WorkerOptions{})
	wm.Register("test.job", func(context.Context, *ojs.JobContext) error { return nil })

	rec := httptest.NewRecorder()
	wm.HealthHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d before Start", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestWorkerStartAsync_ValidatesAndCanBeWaited(t *testing.T) {
	if err := NewWorkerManager(WorkerOptions{}).StartAsync(context.Background()); err == nil {
		t.Fatal("StartAsync() error = nil, want no-handlers error")
	}

	wm := NewWorkerManager(WorkerOptions{})
	wm.Register("test.job", func(context.Context, *ojs.JobContext) error { return nil })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := wm.StartAsync(ctx); err != nil {
		t.Fatalf("StartAsync() error = %v", err)
	}
	if err := wm.Wait(); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if err := wm.Stop(); err != nil {
		t.Fatalf("Stop() after completion error = %v", err)
	}
}
