// Example Echo server with full OJS integration.
//
// Demonstrates middleware, worker lifecycle, health checks, and cron registration.
//
// Run with Docker Compose:
//
//	docker-compose up -d
//	go run main.go
//
// Then enqueue a job:
//
//	curl -X POST http://localhost:3000/send-email \
//	  -H "Content-Type: application/json" \
//	  -d '{"to":"user@example.com","subject":"Hello"}'
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	ojsecho "github.com/openjobspec/ojs-go-contrib/ojs-echo"
	ojs "github.com/openjobspec/ojs-go-sdk"
)

func main() {
	ojsURL := envOrDefault("OJS_URL", "http://localhost:8080")
	var clientOptions []ojs.ClientOption
	var workerOptions []ojs.WorkerOption
	if token := os.Getenv("OJS_AUTH_TOKEN"); token != "" {
		clientOptions = append(clientOptions, ojs.WithAuthToken(token))
		workerOptions = append(workerOptions, ojs.WithWorkerAuth(token))
	}
	client, err := ojs.NewClient(ojsURL, clientOptions...)
	if err != nil {
		log.Fatal(err)
	}

	// Set up Echo
	e := echo.New()
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Use(middleware.BodyLimit("1M"))
	e.Use(ojsecho.Middleware(client))

	// Set up worker
	worker := ojsecho.NewWorkerManagerWithSDKOptions(ojsecho.WorkerOptions{
		URL:         ojsURL,
		Queues:      []string{"default", "emails"},
		Concurrency: 10,
	}, workerOptions...)

	worker.Register("email.send", handleEmailSend)

	// Routes
	registerRoutes(e, client, worker)

	// Register cron jobs
	crons := []ojsecho.CronConfig{
		{
			Name:     "daily-digest",
			Schedule: "0 9 * * *",
			JobType:  "email.digest",
			Args:     ojs.Args{"type": "daily"},
		},
	}

	// Start worker and server with graceful shutdown
	ctx, cancel := ojsecho.GracefulShutdown()
	defer cancel()

	if err := ojsecho.RegisterCrons(ctx, client, crons); err != nil {
		log.Printf("Warning: failed to register crons: %v", err)
	}

	if err := worker.StartAsync(ctx); err != nil {
		log.Printf("start worker: %v", err)
		return
	}

	serverErr := make(chan error, 1)
	go func() {
		log.Println("Server listening on :3000")
		serverErr <- e.Start(":3000")
	}()

	select {
	case <-ctx.Done():
	case err := <-serverErr:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Printf("server stopped: %v", err)
		}
		cancel()
	}
	log.Println("Shutting down...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := e.Shutdown(shutdownCtx); err != nil {
		log.Printf("server shutdown: %v", err)
	}
	if err := worker.Stop(); err != nil {
		log.Printf("worker shutdown: %v", err)
	}
}

func registerRoutes(e *echo.Echo, client *ojs.Client, worker *ojsecho.WorkerManager) {
	e.POST("/send-email", sendEmailHandler)
	e.GET("/jobs/:id", getJobHandler)
	e.GET("/healthz", ojsecho.HealthCheckHandler(client))
	e.GET("/readyz", worker.HealthHandler())
}

func sendEmailHandler(c echo.Context) error {
	var body struct {
		To      string `json:"to"`
		Subject string `json:"subject"`
	}
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	if strings.TrimSpace(body.To) == "" || strings.TrimSpace(body.Subject) == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "to and subject are required",
		})
	}

	err := ojsecho.Enqueue(c, "email.send", ojs.Args{
		"to":      body.To,
		"subject": body.Subject,
	})
	if err != nil {
		log.Printf("enqueue email.send: %v", err)
		return c.JSON(http.StatusBadGateway, map[string]string{"error": "failed to enqueue job"})
	}

	return c.JSON(http.StatusAccepted, map[string]string{"status": "enqueued"})
}

func getJobHandler(c echo.Context) error {
	client := ojsecho.MustClientFromContext(c)

	jobID := strings.TrimSpace(c.Param("id"))
	if jobID == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "job id is required"})
	}
	job, err := client.GetJob(c.Request().Context(), jobID)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, ojs.ErrNotFound) {
			status = http.StatusNotFound
		}
		return c.JSON(status, map[string]string{"error": "failed to retrieve job"})
	}

	return c.JSON(http.StatusOK, job)
}

func handleEmailSend(ctx context.Context, job *ojs.JobContext) error {
	to, _ := job.Job.Args["to"].(string)
	subject, _ := job.Job.Args["subject"].(string)
	log.Printf("Sending email: to=%q subject=%q", to, subject)
	return nil
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
