package ojsgin

import (
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
