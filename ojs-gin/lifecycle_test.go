package ojsgin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	ojs "github.com/openjobspec/ojs-go-sdk"
)

func TestMiddleware_NilClientIsMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Middleware(nil))
	router.GET("/", func(c *gin.Context) {
		if client, ok := ClientFromContext(c); ok || client != nil {
			t.Fatalf("ClientFromContext() = %v, %v; want nil, false", client, ok)
		}
		c.Status(http.StatusNoContent)
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestHealthCheckHandler_NilClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/", HealthCheckHandler(nil))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
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
	gin.SetMode(gin.TestMode)
	wm := NewWorkerManager(WorkerOptions{})
	wm.Register("test.job", func(context.Context, *ojs.JobContext) error { return nil })

	router := gin.New()
	router.GET("/", wm.HealthHandler())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
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
