package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	ojs "github.com/openjobspec/ojs-go-sdk"
)

func main() {
	ojsURL := envOrDefault("OJS_URL", "http://localhost:8080")
	options := []ojs.WorkerOption{ojs.WithQueues("default")}
	if token := os.Getenv("OJS_AUTH_TOKEN"); token != "" {
		options = append(options, ojs.WithWorkerAuth(token))
	}
	worker := ojs.NewWorker(ojsURL, options...)

	worker.Register("email.send", func(ctx ojs.JobContext) error {
		log.Printf("Sending email: job_id=%s args=%v", ctx.Job.ID, ctx.Job.Args)
		return nil
	})

	sigCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.Println("Worker started, waiting for jobs...")
	if err := worker.Start(sigCtx); err != nil {
		log.Printf("worker stopped: %v", err)
	}
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
