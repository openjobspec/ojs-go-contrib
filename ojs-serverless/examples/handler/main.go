package main

import (
	"context"
	"log"
	"os"

	serverless "github.com/openjobspec/ojs-go-contrib/ojs-serverless"
)

func main() {
	ojsURL := os.Getenv("OJS_URL")
	if ojsURL == "" {
		ojsURL = "http://localhost:8080"
	}

	handler := serverless.NewLambdaHandler(
		serverless.WithOJSURL(ojsURL),
	)

	handler.Register("email.send", func(ctx context.Context, job serverless.JobEvent) error {
		log.Printf("Processing email.send job: id=%s", job.ID)
		// Process the job here
		return nil
	})

	handler.Register("report.generate", func(ctx context.Context, job serverless.JobEvent) error {
		log.Printf("Processing report.generate job: id=%s", job.ID)
		// Process the job here
		return nil
	})

	// Start the Lambda handler
	// Uncomment the following line when deploying to AWS Lambda:
	// lambda.Start(handler.HandleSQS)

	log.Println("Lambda handler configured with job types: email.send, report.generate")
	log.Println("Deploy with: sam build && sam deploy --guided")
}
