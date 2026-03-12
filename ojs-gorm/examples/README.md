# ojs-gorm Example

A complete application demonstrating transactional job enqueue with GORM and OJS.

## Prerequisites

- [Docker](https://docs.docker.com/get-docker/) and Docker Compose
- Go 1.24+

## Setup

Start the OJS backend and PostgreSQL:

```bash
docker compose up -d
```

Run the application:

```bash
go run main.go
```

In another terminal, run the worker:

```bash
go run worker.go
```

## How It Works

The example creates a database record and enqueues a job atomically. If the database transaction fails, the job is never enqueued. The outbox pattern provides guaranteed delivery even if the OJS server is temporarily unavailable.

## Cleanup

```bash
docker compose down -v
```
