# ojs-fiber

Fiber web framework middleware for [Open Job Spec](https://github.com/openjobspec/ojs-go-sdk).

## Installation

```bash
go get github.com/openjobspec/ojs-go-contrib/ojs-fiber
```

## Usage

```go
package main

import (
    "github.com/gofiber/fiber/v2"
    ojs "github.com/openjobspec/ojs-go-sdk"
    ojsfiber "github.com/openjobspec/ojs-go-contrib/ojs-fiber"
)

func main() {
    client, _ := ojs.NewClient("http://localhost:8080")

    app := fiber.New()
    app.Use(ojsfiber.Middleware(client))

    app.Post("/send-email", func(c *fiber.Ctx) error {
        err := ojsfiber.Enqueue(c, "email.send", ojs.Args{"to": "user@example.com"})
        if err != nil {
            return c.Status(500).JSON(fiber.Map{"error": err.Error()})
        }
        return c.JSON(fiber.Map{"status": "enqueued"})
    })

    app.Listen(":3000")
}
```

## API

### `Middleware(client *ojs.Client) fiber.Handler`

Injects an OJS client into Fiber's Locals. The client is retrievable via `ClientFromContext(c)`.

### `Enqueue(c *fiber.Ctx, jobType string, args ojs.Args, opts ...ojs.EnqueueOption) error`

Helper that retrieves the OJS client from Fiber's Locals and enqueues a job.

### `ClientFromContext(c *fiber.Ctx) (*ojs.Client, bool)`

Retrieves the OJS client stored in Fiber's Locals by the middleware.

## Example

See [examples/](./examples/) for a complete working demo with Docker Compose.
