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
	worker := ojs.NewWorker("http://localhost:8080", ojs.WithQueues("default"))

	worker.Register("email.send", func(ctx ojs.JobContext) error {
		log.Printf("Sending email: job_id=%s args=%v", ctx.Job.ID, ctx.Job.Args)
		return nil
	})

	sigCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.Println("Worker started, waiting for jobs...")
	if err := worker.Start(sigCtx); err != nil {
		log.Fatal(err)
	}
}

