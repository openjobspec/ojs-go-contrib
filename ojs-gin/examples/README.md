# ojs-gin Example

A complete Gin application demonstrating OJS job enqueue from HTTP endpoints.

## Prerequisites

- [Docker](https://docs.docker.com/get-docker/) and Docker Compose
- Go 1.24+

## Setup

Start the OJS backend:

```bash
docker compose up -d
```

Run the API server:

```bash
go run main.go
```

In another terminal, run the worker:

```bash
go run worker.go
```

## Usage

Enqueue a job:

```bash
curl -X POST http://localhost:3000/send-email \
  -H "Content-Type: application/json" \
  -d '{"to": "user@example.com", "subject": "Hello"}'
```

## Cleanup

```bash
docker compose down
```
