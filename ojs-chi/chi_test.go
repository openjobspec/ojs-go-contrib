package ojschi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	ojs "github.com/openjobspec/ojs-go-sdk"
)

func TestMiddleware_SetsClient(t *testing.T) {
	client, _ := ojs.NewClient("http://localhost:8080")

	r := chi.NewRouter()
	r.Use(Middleware(client))
	r.Get("/", func(w http.ResponseWriter, req *http.Request) {
		got, ok := ClientFromContext(req.Context())
		if !ok {
			t.Fatal("expected client in context")
		}
		if got != client {
			t.Fatal("expected same client instance")
		}
		w.WriteHeader(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestClientFromContext_Missing(t *testing.T) {
	r := chi.NewRouter()
	r.Get("/", func(w http.ResponseWriter, req *http.Request) {
		_, ok := ClientFromContext(req.Context())
		if ok {
			t.Fatal("expected no client in context")
		}
		w.WriteHeader(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)
}

func TestClientFromRequest(t *testing.T) {
	client, _ := ojs.NewClient("http://localhost:8080")

	r := chi.NewRouter()
	r.Use(Middleware(client))
	r.Get("/", func(w http.ResponseWriter, req *http.Request) {
		got, ok := ClientFromRequest(req)
		if !ok {
			t.Fatal("expected client from request")
		}
		if got != client {
			t.Fatal("expected same client instance")
		}
		w.WriteHeader(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestEnqueue_NoClient(t *testing.T) {
	r := chi.NewRouter()
	r.Post("/", func(w http.ResponseWriter, req *http.Request) {
		err := Enqueue(req, "test.job", ojs.Args{"key": "arg1"})
		if err == nil {
			t.Fatal("expected error when no client in context")
		}
		w.WriteHeader(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	r.ServeHTTP(w, req)
}

func TestMustClientFromContext_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when no client in context")
		}
	}()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	MustClientFromContext(req.Context())
}

func TestMustClientFromContext_Success(t *testing.T) {
	client, _ := ojs.NewClient("http://localhost:8080")

	r := chi.NewRouter()
	r.Use(Middleware(client))
	r.Get("/", func(w http.ResponseWriter, req *http.Request) {
		got := MustClientFromContext(req.Context())
		if got != client {
			t.Fatal("expected same client instance")
		}
		w.WriteHeader(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestMiddleware_MultipleRequests_SameClient(t *testing.T) {
	client, _ := ojs.NewClient("http://localhost:8080")

	r := chi.NewRouter()
	r.Use(Middleware(client))

	var clients []*ojs.Client
	r.Get("/", func(w http.ResponseWriter, req *http.Request) {
		got, ok := ClientFromContext(req.Context())
		if !ok {
			t.Fatal("expected client in context")
		}
		clients = append(clients, got)
		w.WriteHeader(http.StatusOK)
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

func TestHealthHandler_NoWorker(t *testing.T) {
	wm := NewWorkerManager(WorkerOptions{URL: "http://localhost:8080"})

	handler := wm.HealthHandler()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	handler(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
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

// --- Health Check Handler tests ---

func TestHealthCheckHandler_Healthy(t *testing.T) {
	// Spin up a fake OJS server that returns healthy.
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

	handler := HealthCheckHandler(client)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "healthy" {
		t.Errorf("expected healthy status, got %v", body["status"])
	}
}

func TestHealthCheckHandler_Unreachable(t *testing.T) {
	client, err := ojs.NewClient("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}

	handler := HealthCheckHandler(client)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	handler(w, req)

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

	handler := HealthCheckHandler(client)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	handler(w, req)

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
	// Spin up a fake OJS server that accepts cron registrations.
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


