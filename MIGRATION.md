# Migration Guide: 0.x → 1.0

This guide covers upgrading from ojs-go-contrib 0.x to the stable 1.0 release.

## Breaking Changes

### API Surface Cleanup

All packages have had their public APIs reviewed and stabilized. The following changes apply:

1. **Consistent error messages** — All error strings now follow the pattern `"ojs<pkg>: <message>"`. If you match on error strings, update your patterns.
2. **Option function signatures** — Option functions (`OutboxOption`, `publishOption`, `Option`) are finalized. Custom option implementations should be verified against the new signatures.
3. **Context key types** — Context keys are unexported typed constants (not string keys). This was already the case in ojs-chi; ojs-echo, ojs-fiber, and ojs-gin use framework-native context storage and are unaffected.

### Module Paths

Module paths are **unchanged** for 1.0:

| Package | Module Path |
|---------|------------|
| ojs-chi | `github.com/openjobspec/ojs-go-contrib/ojs-chi` |
| ojs-gin | `github.com/openjobspec/ojs-go-contrib/ojs-gin` |
| ojs-echo | `github.com/openjobspec/ojs-go-contrib/ojs-echo` |
| ojs-fiber | `github.com/openjobspec/ojs-go-contrib/ojs-fiber` |
| ojs-gorm | `github.com/openjobspec/ojs-go-contrib/ojs-gorm` |
| ojs-serverless | `github.com/openjobspec/ojs-go-contrib/ojs-serverless` |

No `/v2` suffix is introduced. The 1.0 release is backward-compatible at the module path level.

### Configuration Changes

#### ojs-chi / ojs-gin — WorkerOptions

The `WorkerOptions` struct fields are finalized:

```go
type WorkerOptions struct {
    URL             string   // OJS server URL (required)
    Queues          []string // Defaults to ["default"]
    Concurrency     int      // Defaults to 10
    PollInterval    int      // Milliseconds between polls
    ShutdownTimeout int      // Seconds for graceful shutdown
}
```

- `Concurrency` now defaults to `10` if set to `0` or negative.
- `Queues` now defaults to `["default"]` if empty.

#### ojs-gorm — Outbox Defaults

- Default `batchSize` is `100` (was previously undocumented).
- Default `interval` is `5s`.
- The outbox table name is `ojs_outbox` (via `TableName()` on `OutboxEntry`).

#### ojs-serverless — Handler Options

- `WithColdStartWarmup` is now the canonical way to run initialization logic on first invocation.
- `WithDefaultHandler` provides a fallback for unregistered job types.

## Step-by-Step Migration

### ojs-chi

1. Update your dependency:
   ```bash
   go get github.com/openjobspec/ojs-go-contrib/ojs-chi@v1.0.0
   ```

2. If you use `WorkerManager`, verify your `WorkerOptions` struct. Fields are unchanged but defaults are now enforced:
   ```go
   // Before (0.x): zero-value concurrency was undefined behavior
   wm := ojschi.NewWorkerManager(ojschi.WorkerOptions{URL: url})

   // After (1.0): zero-value concurrency defaults to 10
   wm := ojschi.NewWorkerManager(ojschi.WorkerOptions{URL: url})
   ```

3. No changes needed for `Middleware`, `ClientFromContext`, `ClientFromRequest`, `MustClientFromContext`, or `Enqueue`.

### ojs-gin

1. Update your dependency:
   ```bash
   go get github.com/openjobspec/ojs-go-contrib/ojs-gin@v1.0.0
   ```

2. If you use `WorkerManager`, the same defaults as ojs-chi apply (Concurrency=10, Queues=["default"]).

3. No changes needed for `Middleware`, `ClientFromContext`, or `Enqueue`.

### ojs-echo

1. Update your dependency:
   ```bash
   go get github.com/openjobspec/ojs-go-contrib/ojs-echo@v1.0.0
   ```

2. No API changes. `Middleware`, `ClientFromContext`, and `Enqueue` signatures are unchanged.

### ojs-fiber

1. Update your dependency:
   ```bash
   go get github.com/openjobspec/ojs-go-contrib/ojs-fiber@v1.0.0
   ```

2. No API changes. `Middleware`, `ClientFromContext`, and `Enqueue` signatures are unchanged.

### ojs-gorm

1. Update your dependency:
   ```bash
   go get github.com/openjobspec/ojs-go-contrib/ojs-gorm@v1.0.0
   ```

2. If you use the `Outbox`, verify your configuration against the new defaults:
   ```go
   // Explicit configuration (recommended for 1.0)
   outbox := ojsgorm.NewOutbox(db, client,
       ojsgorm.WithOutboxBatchSize(100),
       ojsgorm.WithOutboxInterval(5 * time.Second),
   )
   ```

3. Ensure your outbox table matches the `OutboxEntry` schema. Run `outbox.AutoMigrate()` to update.

4. `EnqueueAfterCommit` and `Register` are unchanged.

### ojs-serverless

1. Update your dependency:
   ```bash
   go get github.com/openjobspec/ojs-go-contrib/ojs-serverless@v1.0.0
   ```

2. If you relied on unregistered job types silently failing, note that they now return an error. Use `WithDefaultHandler` for a fallback:
   ```go
   handler := serverless.NewLambdaHandler(
       serverless.WithDefaultHandler(func(ctx context.Context, job serverless.JobEvent) error {
           log.Printf("unhandled job type: %s", job.Type)
           return nil
       }),
   )
   ```

3. `HandleSQS`, `HandleHTTP`, `HandleAPIGateway`, `HandleEventBridge`, and `HandleRaw` signatures are unchanged.

## Minimum Requirements

| Dependency | Minimum Version |
|-----------|----------------|
| Go | 1.22+ |
| ojs-go-sdk | 0.1.0+ |
| Chi | v5.1.0+ |
| Gin | v1.10.0+ |
| Echo | v4.12.0+ |
| Fiber | v2.52.0+ |
| GORM | v1.25.0+ |

