# ojs-chi Example

A complete example using Chi router with OJS middleware and worker.

## Prerequisites

- Go 1.24+
- Docker and Docker Compose

## Running

1. Start the OJS backend:

```bash
docker-compose up -d
```

2. Run the server (includes an embedded worker):

```bash
go run .
```

To run the standalone worker example instead:

```bash
go run ./worker
```

3. Enqueue a job:

```bash
curl -X POST http://localhost:3000/send-email \
  -H "Content-Type: application/json" \
  -d '{"to":"user@example.com","subject":"Hello from OJS"}'
```

4. Check health:

```bash
curl http://localhost:3000/readyz
```

## Architecture

- `main.go` — Chi router with OJS middleware, job enqueue routes, and embedded worker
- `worker/main.go` — Standalone worker example
- `docker-compose.yml` — Redis + OJS backend server
