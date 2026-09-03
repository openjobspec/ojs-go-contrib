# ojs-serverless

AWS Lambda handler adapter for SQS-based job processing with [Open Job Spec](https://github.com/openjobspec/ojs-go-sdk).

> **Note:** This package was migrated from `github.com/openjobspec/ojs-go-sdk/serverless`. The original location is deprecated.

## Installation

```bash
go get github.com/openjobspec/ojs-go-contrib/ojs-serverless@v0.5.0
```

## Usage

```go
package main

import (
    "context"
    "os"

    "github.com/aws/aws-lambda-go/lambda"
    serverless "github.com/openjobspec/ojs-go-contrib/ojs-serverless"
)

func main() {
    handler := serverless.NewLambdaHandler(
        serverless.WithOJSURL("https://ojs.example.com"),
        serverless.WithPushSigningSecrets(os.Getenv("OJS_PUSH_SIGNING_SECRET")),
        serverless.WithSQSConcurrency(4),
    )

    handler.Register("email.send", func(ctx context.Context, job serverless.JobEvent) error {
        // Process the job
        return nil
    })

    lambda.Start(handler.HandleSQS)
}
```

## API

### `NewLambdaHandler(opts ...Option) *LambdaHandler`

Creates a reusable, concurrency-safe handler. Options include execution timeout, SQS concurrency, body limits, logging, cold-start warmup, default handlers, and push authentication.

### `(*LambdaHandler) Register(jobType string, handler HandlerFunc)`

Associates a handler function with a job type.

### `(*LambdaHandler) HandleSQS(ctx context.Context, event SQSEvent) (SQSBatchResponse, error)`

Processes an SQS event containing OJS jobs. Returns partial batch failures for SQS retry.

### `(*LambdaHandler) HandleHTTP() http.HandlerFunc`

Returns an HTTP handler for OJS push delivery.

HTTP push and API Gateway delivery fail closed unless at least one secret is configured with `WithPushSigningSecrets`. Signatures use `HMAC-SHA256(secret, timestamp + "." + rawBody)` in `X-OJS-Signature`, with `X-OJS-Timestamp`, `X-OJS-Delivery-ID`, and `X-OJS-Job-ID`. `WithInsecureAllowUnsignedPushForLocalDevelopment` is available only for local development and tests.

### `(*LambdaHandler) HandleAPIGateway(ctx, event)`

Maps API Gateway proxy methods, case-insensitive headers, base64 bodies, request IDs, body limits, push authentication, and OJS JSON responses.

### `(*LambdaHandler) HandleEventBridge(ctx, event)` and `HandleRaw(ctx, payload)`

Process EventBridge or auto-detected SQS/API Gateway/EventBridge/direct events while preserving trigger metadata and counting one invocation per raw event.

## Example

See [examples/](./examples/) for a complete SAM template and Lambda handler example.
