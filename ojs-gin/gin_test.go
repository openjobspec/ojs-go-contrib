package ojsgin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	ojs "github.com/openjobspec/ojs-go-sdk"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestMiddleware_SetsClient(t *testing.T) {
	client, _ := ojs.NewClient("http://localhost:8080")

	r := gin.New()
	r.Use(Middleware(client))
	r.GET("/", func(c *gin.Context) {
		got, ok := ClientFromContext(c)
		if !ok {
			t.Fatal("expected client in context")
		}
		if got != client {
			t.Fatal("expected same client instance")
		}
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestClientFromContext_Missing(t *testing.T) {
	r := gin.New()
	r.GET("/", func(c *gin.Context) {
		_, ok := ClientFromContext(c)
		if ok {
			t.Fatal("expected no client in context")
		}
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)
}

func TestEnqueue_NoClient(t *testing.T) {
	r := gin.New()
	r.POST("/", func(c *gin.Context) {
		err := Enqueue(c, "test.job", ojs.Args{"key": "arg1"})
		if err == nil {
			t.Fatal("expected error when no client in context")
		}
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	r.ServeHTTP(w, req)
}

func TestEnqueue_NoClient_ErrorMessage(t *testing.T) {
	r := gin.New()
	r.POST("/", func(c *gin.Context) {
		err := Enqueue(c, "test.job", ojs.Args{"key": "arg1"})
		if err == nil {
			t.Fatal("expected error")
		}
		want := "ojsgin: no OJS client in context; use ojsgin.Middleware"
		if err.Error() != want {
			t.Errorf("expected %q, got %q", want, err.Error())
		}
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	r.ServeHTTP(w, req)
}

func TestMiddleware_MultipleRequests_SameClient(t *testing.T) {
	client, _ := ojs.NewClient("http://localhost:8080")

	r := gin.New()
	r.Use(Middleware(client))

	var clients []*ojs.Client
	r.GET("/", func(c *gin.Context) {
		got, ok := ClientFromContext(c)
		if !ok {
			t.Fatal("expected client in context")
		}
		clients = append(clients, got)
		c.Status(http.StatusOK)
	})

	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		r.ServeHTTP(w, req)
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

func TestMiddleware_ChainedMiddleware(t *testing.T) {
	client, _ := ojs.NewClient("http://localhost:8080")
	order := make([]string, 0, 3)

	r := gin.New()

	r.Use(func(c *gin.Context) {
		order = append(order, "before")
		c.Next()
	})

	r.Use(Middleware(client))

	r.Use(func(c *gin.Context) {
		order = append(order, "after")
		c.Next()
	})

	r.GET("/", func(c *gin.Context) {
		order = append(order, "handler")
		_, ok := ClientFromContext(c)
		if !ok {
			t.Fatal("expected client in context after middleware chain")
		}
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)

	if len(order) != 3 {
		t.Fatalf("expected 3 middleware calls, got %d: %v", len(order), order)
	}
	if order[0] != "before" || order[1] != "after" || order[2] != "handler" {
		t.Errorf("unexpected middleware order: %v", order)
	}
}

func TestMiddleware_CustomErrorHandler(t *testing.T) {
	client, _ := ojs.NewClient("http://localhost:8080")

	r := gin.New()
	r.Use(Middleware(client))
	r.Use(func(c *gin.Context) {
		c.Next()
		if len(c.Errors) > 0 {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": c.Errors.Last().Error(),
			})
		}
	})

	r.GET("/", func(c *gin.Context) {
		_ = c.Error(fmt.Errorf("custom error"))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

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

func TestHealthHandler_NoWorker(t *testing.T) {
	wm := NewWorkerManager(WorkerOptions{URL: "http://localhost:8080"})

	r := gin.New()
	r.GET("/health", wm.HealthHandler())

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
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

// --- MustClientFromContext tests ---

func TestMustClientFromContext_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when no client in context")
		}
	}()

	r := gin.New()
	r.GET("/", func(c *gin.Context) {
		MustClientFromContext(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)
}

func TestMustClientFromContext_Success(t *testing.T) {
	client, _ := ojs.NewClient("http://localhost:8080")

	r := gin.New()
	r.Use(Middleware(client))
	r.GET("/", func(c *gin.Context) {
		got := MustClientFromContext(c)
		if got != client {
			t.Fatal("expected same client instance")
		}
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
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

	r := gin.New()
	r.GET("/healthz", HealthCheckHandler(client))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHealthCheckHandler_Unreachable(t *testing.T) {
	client, err := ojs.NewClient("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}

	r := gin.New()
	r.GET("/healthz", HealthCheckHandler(client))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
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

	r := gin.New()
	r.GET("/healthz", HealthCheckHandler(client))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
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
