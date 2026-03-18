package ojsecho

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	ojs "github.com/openjobspec/ojs-go-sdk"
)

func TestMiddleware_SetsClient(t *testing.T) {
	client, _ := ojs.NewClient("http://localhost:8080")

	e := echo.New()
	e.Use(Middleware(client))
	e.GET("/", func(c echo.Context) error {
		got, ok := ClientFromContext(c)
		if !ok {
			t.Fatal("expected client in context")
		}
		if got != client {
			t.Fatal("expected same client instance")
		}
		return c.NoContent(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestClientFromContext_Missing(t *testing.T) {
	e := echo.New()
	e.GET("/", func(c echo.Context) error {
		_, ok := ClientFromContext(c)
		if ok {
			t.Fatal("expected no client in context")
		}
		return c.NoContent(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
}

func TestEnqueue_NoClient(t *testing.T) {
	e := echo.New()
	e.POST("/", func(c echo.Context) error {
		err := Enqueue(c, "test.job", ojs.Args{"key": "arg1"})
		if err == nil {
			t.Fatal("expected error when no client in context")
		}
		return c.NoContent(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
}

func TestEnqueue_NoClient_ErrorMessage(t *testing.T) {
	e := echo.New()
	e.POST("/", func(c echo.Context) error {
		err := Enqueue(c, "test.job", ojs.Args{"key": "arg1"})
		if err == nil {
			t.Fatal("expected error")
		}
		want := "ojsecho: no OJS client in context; use ojsecho.Middleware"
		if err.Error() != want {
			t.Errorf("expected %q, got %q", want, err.Error())
		}
		return c.NoContent(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
}

func TestMiddleware_MultipleRequests_SameClient(t *testing.T) {
	client, _ := ojs.NewClient("http://localhost:8080")

	e := echo.New()
	e.Use(Middleware(client))

	var clients []*ojs.Client
	e.GET("/", func(c echo.Context) error {
		got, ok := ClientFromContext(c)
		if !ok {
			t.Fatal("expected client in context")
		}
		clients = append(clients, got)
		return c.NoContent(http.StatusOK)
	})

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
	}

	if len(clients) != 3 {
		t.Fatalf("expected 3 clients, got %d", len(clients))
	}
	for i, c := range clients {
		if c != client {
			t.Errorf("request %d: expected same client instance", i)
		}
	}
}

func TestMiddleware_ContextPropagation(t *testing.T) {
	client, _ := ojs.NewClient("http://localhost:8080")

	e := echo.New()
	e.Use(Middleware(client))

	e.GET("/", func(c echo.Context) error {
		// Verify the context key is correctly scoped — fetching with
		// a different key should not return the client.
		v := c.Get("wrong.key")
		if v != nil {
			t.Fatal("expected nil for wrong context key")
		}
		return c.NoContent(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestMiddleware_ChainWithOtherMiddleware(t *testing.T) {
	client, _ := ojs.NewClient("http://localhost:8080")
	order := make([]string, 0, 3)

	e := echo.New()

	// Add a middleware before OJS middleware
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			order = append(order, "before")
			return next(c)
		}
	})

	e.Use(Middleware(client))

	// Add a middleware after OJS middleware
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			order = append(order, "after")
			return next(c)
		}
	})

	e.GET("/", func(c echo.Context) error {
		order = append(order, "handler")
		_, ok := ClientFromContext(c)
		if !ok {
			t.Fatal("expected client in context after middleware chain")
		}
		return c.NoContent(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if len(order) != 3 {
		t.Fatalf("expected 3 middleware calls, got %d", len(order))
	}
	if order[0] != "before" || order[1] != "after" || order[2] != "handler" {
		t.Errorf("unexpected middleware order: %v", order)
	}
}

func TestMiddleware_ContextTimeout(t *testing.T) {
	client, _ := ojs.NewClient("http://localhost:8080")

	e := echo.New()
	e.Use(Middleware(client))

	e.GET("/", func(c echo.Context) error {
		got, ok := ClientFromContext(c)
		if !ok {
			t.Fatal("expected client in context")
		}
		if got != client {
			t.Fatal("expected same client instance")
		}
		// Verify the request context is accessible and valid
		ctx := c.Request().Context()
		if ctx == nil {
			t.Fatal("expected non-nil request context")
		}
		return c.NoContent(http.StatusOK)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestMiddleware_CancelledContext(t *testing.T) {
	client, _ := ojs.NewClient("http://localhost:8080")

	e := echo.New()
	e.Use(Middleware(client))

	var ctxErr error
	e.GET("/", func(c echo.Context) error {
		ctxErr = c.Request().Context().Err()
		return c.NoContent(http.StatusOK)
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if ctxErr == nil {
		t.Error("expected context error for cancelled context")
	}
}

// --- MustClientFromContext tests ---

func TestMustClientFromContext_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when no client in context")
		}
	}()

	e := echo.New()
	e.GET("/", func(c echo.Context) error {
		MustClientFromContext(c)
		return nil
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
}

func TestMustClientFromContext_Success(t *testing.T) {
	client, _ := ojs.NewClient("http://localhost:8080")

	e := echo.New()
	e.Use(Middleware(client))
	e.GET("/", func(c echo.Context) error {
		got := MustClientFromContext(c)
		if got != client {
			t.Fatal("expected same client instance")
		}
		return c.NoContent(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

// --- WorkerManager tests ---

func TestNewWorkerManager_Defaults(t *testing.T) {
	wm := NewWorkerManager(WorkerOptions{URL: "http://localhost:8080"})
	if len(wm.options.Queues) != 1 || wm.options.Queues[0] != "default" {
		t.Errorf("expected default queue, got %v", wm.options.Queues)
	}
	if wm.options.Concurrency != 10 {
		t.Errorf("expected concurrency 10, got %d", wm.options.Concurrency)
	}
}

func TestNewWorkerManager_CustomOptions(t *testing.T) {
	wm := NewWorkerManager(WorkerOptions{
		URL:         "http://localhost:9090",
		Queues:      []string{"email", "reports"},
		Concurrency: 20,
	})
	if len(wm.options.Queues) != 2 {
		t.Errorf("expected 2 queues, got %d", len(wm.options.Queues))
	}
	if wm.options.Concurrency != 20 {
		t.Errorf("expected concurrency 20, got %d", wm.options.Concurrency)
	}
}

func TestNewWorkerManager_ZeroConcurrency(t *testing.T) {
	wm := NewWorkerManager(WorkerOptions{
		URL:         "http://localhost:8080",
		Concurrency: 0,
	})
	if wm.options.Concurrency != 10 {
		t.Errorf("expected default concurrency 10 for zero value, got %d", wm.options.Concurrency)
	}
}

func TestNewWorkerManager_NegativeConcurrency(t *testing.T) {
	wm := NewWorkerManager(WorkerOptions{
		URL:         "http://localhost:8080",
		Concurrency: -5,
	})
	if wm.options.Concurrency != 10 {
		t.Errorf("expected default concurrency 10 for negative value, got %d", wm.options.Concurrency)
	}
}

func TestStart_NoHandlers(t *testing.T) {
	wm := NewWorkerManager(WorkerOptions{URL: "http://localhost:8080"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := wm.Start(ctx)
	if err == nil {
		t.Fatal("expected error when starting without handlers")
	}
}

func TestStop_NoWorker(t *testing.T) {
	wm := NewWorkerManager(WorkerOptions{URL: "http://localhost:8080"})
	err := wm.Stop()
	if err != nil {
		t.Fatalf("expected no error stopping unstarted worker, got %v", err)
	}
}

func TestWorkerHealthHandler_NoWorker(t *testing.T) {
	wm := NewWorkerManager(WorkerOptions{URL: "http://localhost:8080"})

	e := echo.New()
	e.GET("/health", wm.HealthHandler())

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}

// --- Health Check Handler tests ---

func TestHealthCheckHandler_Healthy(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status":         "healthy",
			"version":        "1.0.0",
			"uptime_seconds": 42,
		})
	}))
	defer fake.Close()

	client, err := ojs.NewClient(fake.URL)
	if err != nil {
		t.Fatal(err)
	}

	e := echo.New()
	e.GET("/healthz", HealthCheckHandler(client))

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestHealthCheckHandler_Unreachable(t *testing.T) {
	client, err := ojs.NewClient("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}

	e := echo.New()
	e.GET("/healthz", HealthCheckHandler(client))

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}

func TestHealthCheckHandler_Unhealthy(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status": "degraded",
		})
	}))
	defer fake.Close()

	client, err := ojs.NewClient(fake.URL)
	if err != nil {
		t.Fatal(err)
	}

	e := echo.New()
	e.GET("/healthz", HealthCheckHandler(client))

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}

// --- Cron helper tests ---

func TestCronConfig_Validation(t *testing.T) {
	tests := []struct {
		name    string
		config  CronConfig
		wantErr bool
	}{
		{
			name:    "empty name",
			config:  CronConfig{Schedule: "* * * * *", JobType: "test"},
			wantErr: true,
		},
		{
			name:    "empty schedule",
			config:  CronConfig{Name: "test", JobType: "test"},
			wantErr: true,
		},
		{
			name:    "empty job type",
			config:  CronConfig{Name: "test", Schedule: "* * * * *"},
			wantErr: true,
		},
	}

	client, _ := ojs.NewClient("http://127.0.0.1:1")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RegisterCrons(context.Background(), client, []CronConfig{tt.config})
			if (err != nil) != tt.wantErr {
				t.Errorf("RegisterCrons() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCronConfig_ValidConfig(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"name":     "test-cron",
			"cron":     "*/5 * * * *",
			"type":     "email.digest",
			"timezone": "UTC",
		})
	}))
	defer fake.Close()

	client, err := ojs.NewClient(fake.URL)
	if err != nil {
		t.Fatal(err)
	}

	crons := []CronConfig{
		{
			Name:     "test-cron",
			Schedule: "*/5 * * * *",
			JobType:  "email.digest",
			Args:     ojs.Args{"key": "value"},
		},
	}

	err = RegisterCrons(context.Background(), client, crons)
	if err != nil {
		t.Errorf("RegisterCrons() unexpected error: %v", err)
	}
}

func TestCronConfig_EmptySlice(t *testing.T) {
	client, _ := ojs.NewClient("http://127.0.0.1:1")
	err := RegisterCrons(context.Background(), client, nil)
	if err != nil {
		t.Errorf("RegisterCrons(nil) unexpected error: %v", err)
	}
}
