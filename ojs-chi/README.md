# ojs-chi

Chi router middleware for [Open Job Spec](https://github.com/openjobspec/ojs-go-sdk).

Chi is the HTTP router used by the official OJS backends (`ojs-backend-redis`, `ojs-backend-postgres`), making this integration a natural fit for projects that already use Chi or want consistency with the OJS server stack.

## Installation

```bash
go get github.com/openjobspec/ojs-go-contrib/ojs-chi
```

## Usage

### Middleware

```go
package main

import (
    "encoding/json"
    "net/http"

    "github.com/go-chi/chi/v5"
    "github.com/go-chi/chi/v5/middleware"
    ojs "github.com/openjobspec/ojs-go-sdk"
    ojschi "github.com/openjobspec/ojs-go-contrib/ojs-chi"
)

func main() {
    client, _ := ojs.NewClient("http://localhost:8080")

    r := chi.NewRouter()
    r.Use(middleware.Logger)
    r.Use(ojschi.Middleware(client))

    r.Post("/send-email", func(w http.ResponseWriter, r *http.Request) {
        err := ojschi.Enqueue(r, "email.send", ojs.Args{"to": "user@example.com"})
        if err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(map[string]string{"status": "enqueued"})
    })

    http.ListenAndServe(":3000", r)
}
```

### Direct Client Access

```go
r.Get("/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
    client, ok := ojschi.ClientFromContext(r.Context())
    if !ok {
        http.Error(w, "OJS client not available", http.StatusInternalServerError)
        return
    }

    jobID := chi.URLParam(r, "id")
    job, err := client.GetJob(r.Context(), jobID)
    if err != nil {
        http.Error(w, err.Error(), http.StatusNotFound)
        return
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(job)
})
```

### Worker Integration

Run an OJS worker alongside your Chi server:

```go
func main() {
    client, _ := ojs.NewClient("http://localhost:8080")

    // Set up Chi router
    r := chi.NewRouter()
    r.Use(ojschi.Middleware(client))

    // Set up worker
    worker := ojschi.NewWorkerManager(ojschi.WorkerOptions{
        URL:         "http://localhost:8080",
        Queues:      []string{"default", "emails"},
        Concurrency: 10,
    })

    worker.Register("email.send", func(ctx context.Context, job *ojs.JobContext) error {
        to := job.Args["to"].(string)
        fmt.Printf("Sending email to %s\n", to)
        return nil
    })

    // Health check endpoint
    r.Get("/health", worker.HealthHandler())

    // Start worker and server with graceful shutdown
    ctx, cancel := ojschi.GracefulShutdown()
    defer cancel()

    worker.StartAsync(ctx)

    srv := &http.Server{Addr: ":3000", Handler: r}
    go srv.ListenAndServe()

    <-ctx.Done()
    srv.Shutdown(context.Background())
    worker.Stop()
}
```

## API

### `Middleware(client *ojs.Client) func(http.Handler) http.Handler`

Returns a Chi-compatible middleware that injects the OJS client into the request context. Uses `context.WithValue` under the hood, making it compatible with any `net/http` middleware chain.

### `ClientFromContext(ctx context.Context) (*ojs.Client, bool)`

Retrieves the OJS client from a context. Works with any context that was enriched by the middleware, not just Chi-specific contexts.

### `ClientFromRequest(r *http.Request) (*ojs.Client, bool)`

Convenience wrapper around `ClientFromContext` that takes an `*http.Request` directly.

### `MustClientFromContext(ctx context.Context) *ojs.Client`

Same as `ClientFromContext` but panics if the client is not found. Use only when the middleware is guaranteed to be registered upstream.

### `Enqueue(r *http.Request, jobType string, args ojs.Args, opts ...ojs.EnqueueOption) error`

Helper that retrieves the OJS client from the request context and enqueues a job.

### `NewWorkerManager(opts WorkerOptions) *WorkerManager`

Creates a new worker manager for running an OJS worker alongside your Chi server.

### `GracefulShutdown() (context.Context, context.CancelFunc)`

Sets up signal handling for graceful shutdown of both the Chi server and OJS worker. Returns a context that is cancelled on SIGTERM or SIGINT.

## Example

See [examples/](./examples/) for a complete working demo with Docker Compose.
