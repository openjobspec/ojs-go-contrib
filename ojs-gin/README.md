# ojs-gin

Gin web framework middleware for [Open Job Spec](https://github.com/openjobspec/ojs-go-sdk).

## Installation

```bash
go get github.com/openjobspec/ojs-go-contrib/ojs-gin
```

## Usage

```go
package main

import (
    "github.com/gin-gonic/gin"
    ojs "github.com/openjobspec/ojs-go-sdk"
    ojsgin "github.com/openjobspec/ojs-go-contrib/ojs-gin"
)

func main() {
    client, _ := ojs.NewClient("http://localhost:8080")

    r := gin.Default()
    r.Use(ojsgin.Middleware(client))

    r.POST("/send-email", func(c *gin.Context) {
        err := ojsgin.Enqueue(c, "email.send", ojs.Args{"to": "user@example.com"})
        if err != nil {
            c.JSON(500, gin.H{"error": err.Error()})
            return
        }
        c.JSON(200, gin.H{"status": "enqueued"})
    })

    r.Run(":3000")
}
```

## API

### `Middleware(client *ojs.Client) gin.HandlerFunc`

Injects an OJS client into the Gin context. The client is retrievable via `ClientFromContext(c)`.

### `Enqueue(c *gin.Context, jobType string, args ojs.Args, opts ...ojs.EnqueueOption) error`

Helper that retrieves the OJS client from the Gin context and enqueues a job.

### `ClientFromContext(c *gin.Context) (*ojs.Client, bool)`

Retrieves the OJS client stored in the Gin context by the middleware.

## Example

See [examples/](./examples/) for a complete working demo with Docker Compose.
