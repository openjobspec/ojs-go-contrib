package ojsecho

import (
	"context"
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
