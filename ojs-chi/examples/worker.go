// Example standalone worker using the Chi contrib package.
package main

import (
	"context"
	"fmt"
	"log"

	ojs "github.com/openjobspec/ojs-go-sdk"
	ojschi "github.com/openjobspec/ojs-go-contrib/ojs-chi"
)

func runWorker() {
	worker := ojschi.NewWorkerManager(ojschi.WorkerOptions{
		URL:         "http://localhost:8080",
		Queues:      []string{"default", "emails"},
		Concurrency: 5,
	})

	worker.Register("email.send", func(ctx context.Context, job *ojs.JobContext) error {
		to, _ := job.Args["to"].(string)
		fmt.Printf("[worker] Sending email to %s\n", to)
		return nil
	})

	worker.Register("report.generate", func(ctx context.Context, job *ojs.JobContext) error {
		reportType, _ := job.Args["type"].(string)
		fmt.Printf("[worker] Generating %s report\n", reportType)
		return nil
	})

	ctx, cancel := ojschi.GracefulShutdown()
	defer cancel()

	log.Println("Worker starting...")
	if err := worker.Start(ctx); err != nil {
		log.Fatal(err)
	}
}
