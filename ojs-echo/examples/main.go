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
	"encoding/json"
	"log"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	ojs "github.com/openjobspec/ojs-go-sdk"
	ojsecho "github.com/openjobspec/ojs-go-contrib/ojs-echo"
)

func main() {
	client, err := ojs.NewClient("http://localhost:8080")
	if err != nil {
		log.Fatal(err)
	}

	// Set up Echo
	e := echo.New()
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Use(ojsecho.Middleware(client))

	// Set up worker
	worker := ojsecho.NewWorkerManager(ojsecho.WorkerOptions{
		URL:         "http://localhost:8080",
		Queues:      []string{"default", "emails"},
		Concurrency: 10,
	})

	worker.Register("email.send", handleEmailSend)

	// Routes
	e.POST("/send-email", sendEmailHandler)
	e.GET("/jobs/:id", getJobHandler)
	e.GET("/healthz", ojsecho.HealthCheckHandler(client)) // OJS server health
	e.GET("/readyz", worker.HealthHandler())               // local worker health

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
		log.Fatal(err)
	}

	go func() {
		log.Println("Server listening on :3000")
		if err := e.Start(":3000"); err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	<-ctx.Done()
	log.Println("Shutting down...")
	e.Shutdown(context.Background())
	worker.Stop()
}

func sendEmailHandler(c echo.Context) error {
	var body struct {
		To      string `json:"to"`
		Subject string `json:"subject"`
	}
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	err := ojsecho.Enqueue(c, "email.send", ojs.Args{
		"to":      body.To,
		"subject": body.Subject,
	})
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]string{"status": "enqueued"})
}

func getJobHandler(c echo.Context) error {
	client := ojsecho.MustClientFromContext(c)

	jobID := c.Param("id")
	job, err := client.GetJob(c.Request().Context(), jobID)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, job)
}

func handleEmailSend(ctx context.Context, job *ojs.JobContext) error {
	to, _ := job.Job.Args["to"].(string)
	subject, _ := job.Job.Args["subject"].(string)
	data, _ := json.Marshal(map[string]string{"to": to, "subject": subject})
	log.Printf("Sending email: %s", data)
	return nil
}
