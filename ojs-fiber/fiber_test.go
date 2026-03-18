package ojsfiber

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v2"
	ojs "github.com/openjobspec/ojs-go-sdk"
)

func TestMiddleware_SetsClient(t *testing.T) {
	client, _ := ojs.NewClient("http://localhost:8080")

	app := fiber.New()
	app.Use(Middleware(client))
	app.Get("/", func(c *fiber.Ctx) error {
		got, ok := ClientFromContext(c)
		if !ok {
			t.Fatal("expected client in context")
		}
		if got != client {
			t.Fatal("expected same client instance")
		}
		return c.SendStatus(http.StatusOK)
	})

	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test failed: %v", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestClientFromContext_Missing(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c *fiber.Ctx) error {
		_, ok := ClientFromContext(c)
		if ok {
			t.Fatal("expected no client in context")
		}
		return c.SendStatus(http.StatusOK)
	})

	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test failed: %v", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
}

func TestEnqueue_NoClient(t *testing.T) {
	app := fiber.New()
	app.Post("/", func(c *fiber.Ctx) error {
		err := Enqueue(c, "test.job", ojs.Args{"key": "arg1"})
		if err == nil {
			t.Fatal("expected error when no client in context")
		}
		return c.SendStatus(http.StatusOK)
	})

	req, _ := http.NewRequest(http.MethodPost, "/", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test failed: %v", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
}

func TestEnqueue_NoClient_ErrorMessage(t *testing.T) {
	app := fiber.New()
	app.Post("/", func(c *fiber.Ctx) error {
		err := Enqueue(c, "test.job", ojs.Args{"key": "arg1"})
		if err == nil {
			t.Fatal("expected error")
		}
		want := "ojsfiber: no OJS client in context; use ojsfiber.Middleware"
		if err.Error() != want {
			t.Errorf("expected %q, got %q", want, err.Error())
		}
		return c.SendStatus(http.StatusOK)
	})

	req, _ := http.NewRequest(http.MethodPost, "/", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test failed: %v", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
}

func TestMiddleware_MultipleRequests_SameClient(t *testing.T) {
	client, _ := ojs.NewClient("http://localhost:8080")

	app := fiber.New()
	app.Use(Middleware(client))

	var mu sync.Mutex
	var clients []*ojs.Client
	app.Get("/", func(c *fiber.Ctx) error {
		got, ok := ClientFromContext(c)
		if !ok {
			t.Error("expected client in context")
			return c.SendStatus(http.StatusInternalServerError)
		}
		mu.Lock()
		clients = append(clients, got)
		mu.Unlock()
		return c.SendStatus(http.StatusOK)
	})

	for i := 0; i < 3; i++ {
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test failed on request %d: %v", i, err)
		}
		resp.Body.Close()
	}

	mu.Lock()
	defer mu.Unlock()
	if len(clients) != 3 {
		t.Fatalf("expected 3 clients, got %d", len(clients))
	}
	for i, c := range clients {
		if c != client {
			t.Errorf("request %d: expected same client instance", i)
		}
	}
}

func TestMiddleware_ConcurrentRequests(t *testing.T) {
	client, _ := ojs.NewClient("http://localhost:8080")

	app := fiber.New()
	app.Use(Middleware(client))

	var mu sync.Mutex
	var count int
	app.Get("/", func(c *fiber.Ctx) error {
		got, ok := ClientFromContext(c)
		if !ok {
			return c.SendStatus(http.StatusInternalServerError)
		}
		if got != client {
			return c.SendStatus(http.StatusInternalServerError)
		}
		mu.Lock()
		count++
		mu.Unlock()
		return c.SendStatus(http.StatusOK)
	})

	const n = 10
	var wg sync.WaitGroup
	wg.Add(n)
	errs := make([]error, n)

	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			req, _ := http.NewRequest(http.MethodGet, "/", nil)
			resp, err := app.Test(req)
			if err != nil {
				errs[idx] = err
				return
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				errs[idx] = fmt.Errorf("request %d: expected 200, got %d", idx, resp.StatusCode)
			}
		}(i)
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("request %d: %v", i, err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if count != n {
		t.Errorf("expected %d successful requests, got %d", n, count)
	}
}

func TestMiddleware_ErrorInHandler(t *testing.T) {
	client, _ := ojs.NewClient("http://localhost:8080")

	app := fiber.New(fiber.Config{
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			return c.Status(http.StatusInternalServerError).SendString(err.Error())
		},
	})
	app.Use(Middleware(client))
	app.Get("/", func(c *fiber.Ctx) error {
		return fmt.Errorf("handler error")
	})

	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test failed: %v", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", resp.StatusCode)
	}
}

func TestMiddleware_WrongLocalsKey(t *testing.T) {
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("wrong.key", "not-a-client")
		return c.Next()
	})
	app.Get("/", func(c *fiber.Ctx) error {
		_, ok := ClientFromContext(c)
		if ok {
			t.Fatal("expected no client with wrong key")
		}
		return c.SendStatus(http.StatusOK)
	})

	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test failed: %v", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
}

// --- MustClientFromContext tests ---

func TestMustClientFromContext_Panics(t *testing.T) {
	// Fiber runs handlers in a separate goroutine (fasthttp), so
	// a panic inside the handler will not be caught by a test-level
	// recover(). Instead we use Fiber's built-in recovery middleware
	// and verify it returns 500.
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		defer func() {
			if r := recover(); r != nil {
				_ = c.Status(fiber.StatusInternalServerError).SendString("panic recovered")
			}
		}()
		return c.Next()
	})
	app.Get("/", func(c *fiber.Ctx) error {
		MustClientFromContext(c)
		return nil
	})

	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test failed: %v", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 from recovered panic, got %d", resp.StatusCode)
	}
}

func TestMustClientFromContext_Success(t *testing.T) {
	client, _ := ojs.NewClient("http://localhost:8080")

	app := fiber.New()
	app.Use(Middleware(client))
	app.Get("/", func(c *fiber.Ctx) error {
		got := MustClientFromContext(c)
		if got != client {
			t.Fatal("expected same client instance")
		}
		return c.SendStatus(http.StatusOK)
	})

	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test failed: %v", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
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

	app := fiber.New()
	app.Get("/health", wm.HealthHandler())

	req, _ := http.NewRequest(http.MethodGet, "/health", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test failed: %v", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", resp.StatusCode)
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

	app := fiber.New()
	app.Get("/healthz", HealthCheckHandler(client))

	req, _ := http.NewRequest(http.MethodGet, "/healthz", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestHealthCheckHandler_Unreachable(t *testing.T) {
	client, err := ojs.NewClient("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}

	app := fiber.New()
	app.Get("/healthz", HealthCheckHandler(client))

	req, _ := http.NewRequest(http.MethodGet, "/healthz", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test failed: %v", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", resp.StatusCode)
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

	app := fiber.New()
	app.Get("/healthz", HealthCheckHandler(client))

	req, _ := http.NewRequest(http.MethodGet, "/healthz", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test failed: %v", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", resp.StatusCode)
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
