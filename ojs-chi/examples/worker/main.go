// Example standalone worker using the Chi contrib package.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	ojschi "github.com/openjobspec/ojs-go-contrib/ojs-chi"
	ojs "github.com/openjobspec/ojs-go-sdk"
)

func main() {
	ojsURL := envOrDefault("OJS_URL", "http://localhost:8080")
	var workerOptions []ojs.WorkerOption
	if token := os.Getenv("OJS_AUTH_TOKEN"); token != "" {
		workerOptions = append(workerOptions, ojs.WithWorkerAuth(token))
	}
	worker := ojschi.NewWorkerManagerWithSDKOptions(ojschi.WorkerOptions{
		URL:         ojsURL,
		Queues:      []string{"default", "emails"},
		Concurrency: 5,
	}, workerOptions...)

	worker.Register("email.send", func(ctx context.Context, job *ojs.JobContext) error {
		to, _ := job.Job.Args["to"].(string)
		fmt.Printf("[worker] Sending email to %s\n", to)
		return nil
	})

	worker.Register("report.generate", func(ctx context.Context, job *ojs.JobContext) error {
		reportType, _ := job.Job.Args["type"].(string)
		fmt.Printf("[worker] Generating %s report\n", reportType)
		return nil
	})

	ctx, cancel := ojschi.GracefulShutdown()
	defer cancel()

	log.Println("Worker starting...")
	if err := worker.Start(ctx); err != nil {
		log.Printf("worker stopped: %v", err)
	}
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
