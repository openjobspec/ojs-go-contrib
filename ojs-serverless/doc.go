// Package serverless provides OJS job processing adapters for serverless
// environments like AWS Lambda. It bridges SQS events into OJS JobContext
// objects and handles ACK/NACK callbacks automatically.
//
// # AWS Lambda with SQS
//
// The most common pattern is using SQS event source mapping to trigger
// Lambda functions. The LambdaHandler wraps your job handlers and
// translates SQS events into OJS job processing:
//
//	handler := serverless.NewLambdaHandler(
//	    serverless.WithOJSURL("https://ojs.example.com"),
//	)
//
//	handler.Register("email.send", func(ctx context.Context, job serverless.JobEvent) error {
//	    // Process the job
//	    return nil
//	})
//
//	lambda.Start(handler.HandleSQS)
//
// # Push Delivery
//
// For HTTP push delivery (OJS server pushes jobs to a function URL),
// use HandleHTTP which accepts standard http.Handler compatible signatures.
package serverless
