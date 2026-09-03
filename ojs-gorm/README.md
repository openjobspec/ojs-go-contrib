# ojs-gorm

Transactional job enqueue and durable outbox publishing for [Open Job Spec](https://github.com/openjobspec/ojs-go-sdk).

## Installation

```bash
go get github.com/openjobspec/ojs-go-contrib/ojs-gorm@v0.5.0
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
    err := db.Transaction(func(tx *gorm.DB) error {
        tx.Create(&User{Name: "Alice"})
        ojsgorm.EnqueueAfterCommit(tx, "welcome.email", ojs.Args{"name": "Alice"})
        return nil
    })
    if err != nil {
        // A *ojsgorm.PostCommitError means the row committed but enqueue failed.
        log.Fatal(err)
    }
}
```

## API

### `Register(db *gorm.DB, client *ojs.Client) error`

Registers the OJS plugin with GORM. This enables `EnqueueAfterCommit` for all transactions on this DB instance.

### `EnqueueAfterCommit(tx *gorm.DB, jobType string, args ojs.Args, opts ...ojs.EnqueueOption)`

Queues a job to be enqueued with the OJS server after the current GORM transaction commits successfully. If the transaction rolls back, the job is discarded.

The exact top-level transaction handle supplied to `gorm.DB.Transaction` must be passed; nested savepoint callbacks should use the outbox instead. A returned `*PostCommitError` means the database commit succeeded but enqueue failed, so callers must not blindly retry the database transaction.

`EnqueueAfterCommit` has no error return, so misuse (a nil handle, an unregistered plugin, or a `*gorm.DB` that is not an active transaction — for example calling it directly on the base handle returned by `gorm.Open`) is reported through GORM's existing logger instead of being returned. It never writes to `tx.Error` in that case, because `tx` may be a long-lived shared/base handle rather than a disposable transaction-scoped clone, and GORM propagates `db.Error` into every subsequent chained call — mutating it would silently break all later queries on that handle. Use `EnqueueAfterCommitErr` (and `EnqueueAfterCommitJSONErr` for the JSON variant) if you want the validation error returned directly instead of logged. Once a valid, plugin-registered active transaction is confirmed, a delivery/outbox validation error (such as an empty job type) still marks that transaction for rollback, unchanged from the existing contract.

`Publish` likewise requires an active transaction and returns validation errors directly without mutating the supplied handle. Return that error from the GORM transaction callback to roll back both the business change and outbox insert.

### `NewOutbox(db *gorm.DB, client *ojs.Client, opts ...OutboxOption) *Outbox`

Creates an outbox publisher that atomically claims ordered batches, recovers stale claims, records attempts/errors, and publishes them. Use this instead of post-commit enqueue when delivery must survive process crashes.

### `(*Outbox) Run(ctx context.Context) error`

Starts the outbox publisher. It polls the outbox table at regular intervals and publishes pending jobs.

### `(*Outbox) ProcessOnce(ctx context.Context) error`

Claims and processes one batch while returning all decode, enqueue, and status-update failures to the caller. Concurrent publishers use conditional claims; PostgreSQL additionally uses `FOR UPDATE SKIP LOCKED`.

The database and OJS backend cannot participate in one atomic commit. A process crash after OJS accepts a job but before the outbox row is marked published can still redeliver it; handlers should remain idempotent.

## Example

See [examples/](./examples/) for a complete working demo with Docker Compose.
