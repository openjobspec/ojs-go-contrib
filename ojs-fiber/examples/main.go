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
	"encoding/json"
	"log"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	ojs "github.com/openjobspec/ojs-go-sdk"
	ojsfiber "github.com/openjobspec/ojs-go-contrib/ojs-fiber"
)

func main() {
	client, err := ojs.NewClient("http://localhost:8080")
	if err != nil {
		log.Fatal(err)
	}

	// Set up Fiber
	app := fiber.New()
	app.Use(logger.New())
	app.Use(recover.New())
	app.Use(ojsfiber.Middleware(client))

	// Set up worker
	worker := ojsfiber.NewWorkerManager(ojsfiber.WorkerOptions{
		URL:         "http://localhost:8080",
		Queues:      []string{"default", "emails"},
		Concurrency: 10,
	})

	worker.Register("email.send", handleEmailSend)

	// Routes
	app.Post("/send-email", sendEmailHandler)
	app.Get("/jobs/:id", getJobHandler)
	app.Get("/healthz", ojsfiber.HealthCheckHandler(client)) // OJS server health
	app.Get("/readyz", worker.HealthHandler())                // local worker health

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
		log.Fatal(err)
	}

	go func() {
		log.Println("Server listening on :3000")
		if err := app.Listen(":3000"); err != nil {
			log.Fatal(err)
		}
	}()

	<-ctx.Done()
	log.Println("Shutting down...")
	app.Shutdown()
	worker.Stop()
}

func sendEmailHandler(c *fiber.Ctx) error {
	var body struct {
		To      string `json:"to"`
		Subject string `json:"subject"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	err := ojsfiber.Enqueue(c, "email.send", ojs.Args{
		"to":      body.To,
		"subject": body.Subject,
	})
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(fiber.Map{"status": "enqueued"})
}

func getJobHandler(c *fiber.Ctx) error {
	client := ojsfiber.MustClientFromContext(c)

	jobID := c.Params("id")
	job, err := client.GetJob(c.UserContext(), jobID)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(job)
}

func handleEmailSend(ctx context.Context, job *ojs.JobContext) error {
	to, _ := job.Job.Args["to"].(string)
	subject, _ := job.Job.Args["subject"].(string)
	data, _ := json.Marshal(map[string]string{"to": to, "subject": subject})
	log.Printf("Sending email: %s", data)
	return nil
}
