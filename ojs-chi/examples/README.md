# ojs-chi Example

A complete example using Chi router with OJS middleware and worker.

## Prerequisites

- Go 1.22+
- Docker and Docker Compose

## Running

1. Start the OJS backend:

```bash
docker-compose up -d
```

2. Run the server (includes embedded worker):

```bash
go run main.go worker.go
```

3. Enqueue a job:

```bash
curl -X POST http://localhost:3000/send-email \
  -H "Content-Type: application/json" \
  -d '{"to":"user@example.com","subject":"Hello from OJS"}'
```

4. Check health:

```bash
curl http://localhost:3000/health
```

## Architecture

- `main.go` — Chi router with OJS middleware, job enqueue routes, and embedded worker
- `worker.go` — Standalone worker example (shared process with server)
- `docker-compose.yml` — Redis + OJS backend server
