package ojsfiber

import (
	"fmt"
	"io"
	"net/http"
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
