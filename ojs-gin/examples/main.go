package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	ojsgin "github.com/openjobspec/ojs-go-contrib/ojs-gin"
	ojs "github.com/openjobspec/ojs-go-sdk"
)

const maxRequestBodySize = 1 << 20

func main() {
	ojsURL := envOrDefault("OJS_URL", "http://localhost:8080")
	var clientOptions []ojs.ClientOption
	if token := os.Getenv("OJS_AUTH_TOKEN"); token != "" {
		clientOptions = append(clientOptions, ojs.WithAuthToken(token))
	}
	client, err := ojs.NewClient(ojsURL, clientOptions...)
	if err != nil {
		log.Fatal(err)
	}

	r := gin.Default()
	r.Use(ojsgin.Middleware(client))
	registerRoutes(r, client)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	server := &http.Server{
		Addr:              ":3000",
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       time.Minute,
	}
	serverErr := make(chan error, 1)
	go func() {
		log.Println("API server listening on :3000")
		serverErr <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
	case err := <-serverErr:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Printf("server stopped: %v", err)
		}
		cancel()
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("server shutdown: %v", err)
	}
}

func registerRoutes(router gin.IRoutes, client *ojs.Client) {
	router.POST("/send-email", sendEmailHandler)
	router.GET("/healthz", ojsgin.HealthCheckHandler(client))
}

func sendEmailHandler(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxRequestBodySize)
	var request struct {
		To      string `json:"to"`
		Subject string `json:"subject"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		status := http.StatusBadRequest
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			status = http.StatusRequestEntityTooLarge
		}
		c.JSON(status, gin.H{"error": "invalid request body"})
		return
	}
	if strings.TrimSpace(request.To) == "" || strings.TrimSpace(request.Subject) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "to and subject are required"})
		return
	}

	if err := ojsgin.Enqueue(c, "email.send", ojs.Args{
		"to":      request.To,
		"subject": request.Subject,
	}); err != nil {
		log.Printf("enqueue email.send: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to enqueue job"})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"status": "enqueued"})
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
