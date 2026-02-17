# ojs-echo

Echo web framework middleware for [Open Job Spec](https://github.com/openjobspec/ojs-go-sdk).

## Installation

```bash
go get github.com/openjobspec/ojs-go-contrib/ojs-echo
```

## Usage

```go
package main

import (
    "github.com/labstack/echo/v4"
    ojs "github.com/openjobspec/ojs-go-sdk"
    ojsecho "github.com/openjobspec/ojs-go-contrib/ojs-echo"
)

func main() {
    client, _ := ojs.NewClient("http://localhost:8080")

    e := echo.New()
    e.Use(ojsecho.Middleware(client))

    e.POST("/send-email", func(c echo.Context) error {
        err := ojsecho.Enqueue(c, "email.send", ojs.Args{"to": "user@example.com"})
        if err != nil {
            return c.JSON(500, map[string]string{"error": err.Error()})
        }
        return c.JSON(200, map[string]string{"status": "enqueued"})
    })

    e.Start(":3000")
}
```

## API

### `Middleware(client *ojs.Client) echo.MiddlewareFunc`

Injects an OJS client into the Echo context. The client is retrievable via `ClientFromContext(c)`.

### `Enqueue(c echo.Context, jobType string, args ojs.Args, opts ...ojs.EnqueueOption) error`

Helper that retrieves the OJS client from the Echo context and enqueues a job.

### `ClientFromContext(c echo.Context) (*ojs.Client, bool)`

Retrieves the OJS client stored in the Echo context by the middleware.

## Example

See [examples/](./examples/) for a complete working demo with Docker Compose.
