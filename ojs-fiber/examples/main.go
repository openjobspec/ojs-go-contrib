// Example Fiber server with full OJS integration.
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
	"os"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	ojsfiber "github.com/openjobspec/ojs-go-contrib/ojs-fiber"
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

	// Set up Fiber
	app := fiber.New(fiber.Config{
		BodyLimit:    1 << 20,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  time.Minute,
	})
	app.Use(logger.New())
	app.Use(recover.New())
	app.Use(ojsfiber.Middleware(client))

	// Set up worker
	worker := ojsfiber.NewWorkerManagerWithSDKOptions(ojsfiber.WorkerOptions{
		URL:         ojsURL,
		Queues:      []string{"default", "emails"},
		Concurrency: 10,
	}, workerOptions...)

	worker.Register("email.send", handleEmailSend)

	// Routes
	registerRoutes(app, client, worker)

	// Register cron jobs
	crons := []ojsfiber.CronConfig{
		{
			Name:     "daily-digest",
			Schedule: "0 9 * * *",
			JobType:  "email.digest",
			Args:     ojs.Args{"type": "daily"},
		},
	}

	// Start worker and server with graceful shutdown
	ctx, cancel := ojsfiber.GracefulShutdown()
	defer cancel()

	if err := ojsfiber.RegisterCrons(ctx, client, crons); err != nil {
		log.Printf("Warning: failed to register crons: %v", err)
	}

	if err := worker.StartAsync(ctx); err != nil {
		log.Printf("start worker: %v", err)
		return
	}

	serverErr := make(chan error, 1)
	go func() {
		log.Println("Server listening on :3000")
		serverErr <- app.Listen(":3000")
	}()

	select {
	case <-ctx.Done():
	case err := <-serverErr:
		if err != nil {
			log.Printf("server stopped: %v", err)
		}
		cancel()
	}
	log.Println("Shutting down...")
	if err := app.ShutdownWithTimeout(10 * time.Second); err != nil {
		log.Printf("server shutdown: %v", err)
	}
	if err := worker.Stop(); err != nil {
		log.Printf("worker shutdown: %v", err)
	}
}

func registerRoutes(app *fiber.App, client *ojs.Client, worker *ojsfiber.WorkerManager) {
	app.Post("/send-email", sendEmailHandler)
	app.Get("/jobs/:id", getJobHandler)
	app.Get("/healthz", ojsfiber.HealthCheckHandler(client))
	app.Get("/readyz", worker.HealthHandler())
}

func sendEmailHandler(c *fiber.Ctx) error {
	var body struct {
		To      string `json:"to"`
		Subject string `json:"subject"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if strings.TrimSpace(body.To) == "" || strings.TrimSpace(body.Subject) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "to and subject are required",
		})
	}

	err := ojsfiber.Enqueue(c, "email.send", ojs.Args{
		"to":      body.To,
		"subject": body.Subject,
	})
	if err != nil {
		log.Printf("enqueue email.send: %v", err)
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "failed to enqueue job"})
	}

	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"status": "enqueued"})
}

func getJobHandler(c *fiber.Ctx) error {
	client := ojsfiber.MustClientFromContext(c)

	jobID := strings.TrimSpace(c.Params("id"))
	if jobID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "job id is required"})
	}
	job, err := client.GetJob(c.UserContext(), jobID)
	if err != nil {
		status := fiber.StatusBadGateway
		if errors.Is(err, ojs.ErrNotFound) {
			status = fiber.StatusNotFound
		}
		return c.Status(status).JSON(fiber.Map{"error": "failed to retrieve job"})
	}

	return c.JSON(job)
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
