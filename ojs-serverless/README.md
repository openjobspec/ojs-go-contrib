# ojs-serverless

AWS Lambda handler adapter for SQS-based job processing with [Open Job Spec](https://github.com/openjobspec/ojs-go-sdk).

> **Note:** This package was migrated from `github.com/openjobspec/ojs-go-sdk/serverless`. The original location is deprecated.

## Installation

```bash
go get github.com/openjobspec/ojs-go-contrib/ojs-serverless
```

## Usage

```go
package main

import (
    "context"
    "github.com/aws/aws-lambda-go/lambda"
    serverless "github.com/openjobspec/ojs-go-contrib/ojs-serverless"
)

func main() {
    handler := serverless.NewLambdaHandler(
        serverless.WithOJSURL("https://ojs.example.com"),
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

Creates a new serverless handler. Options include `WithOJSURL` and `WithLogger`.

### `(*LambdaHandler) Register(jobType string, handler HandlerFunc)`

Associates a handler function with a job type.

### `(*LambdaHandler) HandleSQS(ctx context.Context, event SQSEvent) (SQSBatchResponse, error)`

Processes an SQS event containing OJS jobs. Returns partial batch failures for SQS retry.

### `(*LambdaHandler) HandleHTTP() http.HandlerFunc`

Returns an HTTP handler for OJS push delivery.

## Example

See [examples/](./examples/) for a complete SAM template and Lambda handler example.

