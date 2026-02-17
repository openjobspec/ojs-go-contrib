# ojs-gorm

Transactional job enqueue via GORM after-commit hooks for [Open Job Spec](https://github.com/openjobspec/ojs-go-sdk).

## Installation

```bash
go get github.com/openjobspec/ojs-go-contrib/ojs-gorm
```

## Usage

```go
package main

import (
    "gorm.io/gorm"
    ojs "github.com/openjobspec/ojs-go-sdk"
    ojsgorm "github.com/openjobspec/ojs-go-contrib/ojs-gorm"
)

func main() {
    client, _ := ojs.NewClient("http://localhost:8080")
    db, _ := gorm.Open(/* your driver */)

    // Register the OJS plugin with GORM
    ojsgorm.Register(db, client)

    // Enqueue a job that only sends after the transaction commits
    db.Transaction(func(tx *gorm.DB) error {
        tx.Create(&User{Name: "Alice"})
        ojsgorm.EnqueueAfterCommit(tx, "welcome.email", ojs.Args{"name": "Alice"})
        return nil
    })
}
```

## API

### `Register(db *gorm.DB, client *ojs.Client) error`

Registers the OJS plugin with GORM. This enables `EnqueueAfterCommit` for all transactions on this DB instance.

### `EnqueueAfterCommit(tx *gorm.DB, jobType string, args ojs.Args, opts ...ojs.EnqueueOption)`

Queues a job to be enqueued with the OJS server after the current GORM transaction commits successfully. If the transaction rolls back, the job is discarded.

### `NewOutbox(db *gorm.DB, client *ojs.Client, opts ...OutboxOption) *Outbox`

Creates an outbox publisher that polls an outbox table for pending jobs and publishes them. Use this for guaranteed delivery even if the OJS server is temporarily unavailable.

### `(*Outbox) Run(ctx context.Context) error`

Starts the outbox publisher. It polls the outbox table at regular intervals and publishes pending jobs.

## Example

See [examples/](./examples/) for a complete working demo with Docker Compose.
