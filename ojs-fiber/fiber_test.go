package ojsfiber

import (
	"io"
	"net/http"
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
