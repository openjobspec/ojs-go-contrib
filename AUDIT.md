# Actor-Based SRP and Clean-Code Audit

## 1. Summary

- Audited all six production modules and five nested example modules as independent release units with `GOWORK=off`.
- Completed every repository-local, programmatically verifiable P0/P1 finding identified in middleware/context handling, lifecycle ownership, transaction/outbox behavior, serverless bindings, examples, and tooling.
- Preserved framework semantics: Chi uses request contexts, Gin uses `gin.Context`, Echo uses `echo.Context`, and Fiber uses Locals/UserContext. No generic cross-framework adapter was introduced.
- Replaced hidden worker goroutines and unsynchronized readiness checks with framework-local lifecycle actors that own start, stop, wait, terminal error, and running state.
- Replaced GORM's false CRUD-callback “after commit” behavior with top-level transaction commit interception and made the durable outbox the explicit solution for retryable delivery.
- Split serverless ownership into handler execution policy, SQS binding, push binding/authentication, and Lambda event mapping without removing an existing exported declaration or changing an existing exported signature.
- Upgraded every SDK consumer to the published `ojs-go-sdk v0.4.0`, completed all module/example sums, and made root tests, builds, vet, staticcheck, and golangci-lint workspace-independent.
- The over-extraction guard retained small framework middleware, health, and cron units. Similar code remains package-local where framework response/context semantics differ.
- Hardened the GORM outbox claim scan against optimistic-claim contention among concurrent publishers: distinct claimed/none/contended outcomes, in-place bounded retry with jittered backoff pinned to the contended row, a hard per-attempt database-call timeout, and batch progress that continues past a persistently-contended row instead of stalling.
- Added synchronous outbox publisher preflight validation and exact SDK-backed job type/queue validation so permanent configuration and poison-row failures cannot enter polling/retry loops.

## 2. Findings and dispositions

| ID | Location | Severity | Actors in conflict | Disposition |
|---|---|---:|---|---|
| OJS-GOC-001 | five module `go.sum` files and all nested examples | P1 | release consumers; workspace users | Implemented. Missing module hashes were reproduced with `-mod=readonly`; all modules/examples now tidy and build standalone against released dependencies. |
| OJS-GOC-002 | `examples/worker.go`, Chi SDK v0.4 migration | P1 | example API server; standalone worker package | Implemented. Worker programs are separately buildable `examples/worker` packages, READMEs use `go run ./worker`, and Chi uses `JobContext.Job.Args`. |
| OJS-GOC-003 | four framework `WorkerManager` implementations | P1 | worker configuration; goroutine lifecycle; readiness handlers | Implemented in framework-local actors. State is synchronized; `StartAsync` validates synchronously; `Stop`, `Wait`, and `Err` observe owned execution; signal resources use `signal.NotifyContext`. |
| OJS-GOC-004 | `PollInterval`, `ShutdownTimeout`, worker auth/custom transport | P1 | operators configure behavior; managers constructed only queue/concurrency | Implemented. Poll/grace values reach SDK options. `NewWorkerManagerWithSDKOptions` adds auth/custom transport without changing exported `WorkerOptions`. |
| OJS-GOC-005 | middleware/client lookup and health handlers | P1 | request handlers; dependency configuration | Implemented. Typed nil clients are treated as missing and health handlers return stable 503 JSON instead of panicking. |
| OJS-GOC-006 | cron registration in four modules | P1 | declarative config authors; remote registration | Implemented. Nil contexts/clients and whitespace-only required fields fail before network I/O with indexed errors. |
| OJS-GOC-007 | worker readiness endpoints | P1 | orchestrators; worker construction | Implemented. Registering a handler no longer reports a worker as running; readiness reflects synchronized execution state. |
| OJS-GOC-008 | `ojs-gorm` CRUD callbacks | P0 | database transaction owner; external OJS enqueue | Implemented for top-level GORM transactions. Jobs run only after successful commit, rollback discards them, invalid queued input prevents commit, and post-commit failures return `*PostCommitError`. |
| OJS-GOC-009 | `EnqueueAfterCommitJSON`, caller-owned args, background context | P1 | transaction caller; serialization; request lifecycle | Implemented. Errors enter the transaction path, args are snapshotted, option slices are copied, and post-commit work uses a bounded cancellation-detached context. Argument snapshots and pre-serialized JSON use `json.Decoder.UseNumber`, preserving exact `int64`, `uint64`, and `json.Number` values while still deep-copying nested maps/slices; exact clone and enqueue-wire JSON are regression-tested. |
| OJS-GOC-010 | GORM outbox polling/state updates | P0 | concurrent publishers; database recovery; OJS delivery | Implemented. Entries are claimed individually immediately before processing instead of sharing one batch lease. Active enqueue claims renew at one-third of the claim TTL through bounded, token-conditional updates; renewal loss cancels the active enqueue and is surfaced. PostgreSQL `SKIP LOCKED`, stale crash recovery, attempts/errors, terminal malformed rows, token-conditional finalization, immediate polling, single-run ownership, and `ProcessOnce` error propagation remain covered. Slow-client two-publisher tests prove later rows stay available, active rows cannot be reclaimed after the original TTL, successful enqueue occurs exactly once, renewal failure preserves token ownership checks, and abandoned no-renew claims recover after expiry. |
| OJS-GOC-011 | unsigned serverless push delivery | P0 | OJS producer; public function endpoint | Implemented. HTTP/API Gateway push fails closed, verifies bounded HMAC-SHA256 signatures over exact raw bytes, enforces timestamp freshness and delivery/job header consistency, and supports secret rotation. |
| OJS-GOC-012 | API Gateway mapping | P1 | AWS proxy binding; OJS push binding | Implemented. Method/status/JSON fidelity, case-insensitive headers, base64 decode errors, decoded body limits, request IDs, authentication, and handler failures map deterministically. |
| OJS-GOC-013 | SQS processing and handler panics | P1 | AWS partial-batch retry; user handler code; concurrency | Implemented. Configurable bounded concurrency isolates panics, propagates trigger/message context, and emits failures in original record order. |
| OJS-GOC-014 | global cold-start state and raw routing count | P1 | reusable handler instances; process observability | Implemented. Warmup is once per handler and concurrency-safe; warmup panics surface as errors; `HandleRaw` records exactly one invocation. |
| OJS-GOC-015 | framework examples | P1 | HTTP clients; backend errors; process shutdown | Implemented. Framework-specific route registration, request limits/validation, stable JSON/status handling, optional auth, bounded server shutdown, observed cleanup errors, and secure environment configuration replace swallowed failures. |
| OJS-GOC-016 | root Makefile and CI | P1 | contributors; CI; local tool versions | Implemented. Go 1.24 CI uses root standalone gates; staticcheck `v0.6.1` and golangci-lint `v2.7.0` are repository-pinned; examples are included in test/build/lint loops. |
| OJS-GOC-017 | `ojs-gorm` enqueue/outbox helper misuse reporting | P0 | shared/base `*gorm.DB` handle owner; concurrent unrelated queries on that handle | Implemented. `EnqueueAfterCommit`, `EnqueueAfterCommitJSON`, and `Publish` validate nil, invalid, and non-transaction handles without calling `tx.AddError`: GORM clones propagate `db.Error` into every subsequent chained call (`(*gorm.DB).getInstance`), so mutating a long-lived shared/base handle silently poisons later queries. The void enqueue helpers report misuse through GORM's existing logger (with `slog.Default()` only when no usable handle exists), while the additive `EnqueueAfterCommitErr`/`EnqueueAfterCommitJSONErr` variants and `Publish` return validation errors directly. Existing exported signatures remain unchanged. Once a valid active transaction is confirmed, enqueue validation errors still roll back through the existing `txState`/commit-hook contract, and callers preserve `Publish`'s existing rollback contract by returning its error from the GORM transaction callback. Regression tests prove helper misuse leaves `db.Error` nil, subsequent transactions succeed, and valid-transaction validation errors roll back. |
| OJS-GOC-018 | `ojs-gorm` outbox claim contention under concurrent publishers | P0 | concurrent publishers racing to claim the same row; `ProcessOnce` batch caller; database driver lock/busy-wait behavior | Implemented. The single-row claim scan (`claimOne`) now reports one of three distinct outcomes instead of collapsing "claimed" and "lost the race" into the same boolean: `claimOutcomeClaimed`, `claimOutcomeNone` (no eligible entry left to scan), and `claimOutcomeContended` (an optimistic claim update lost a race between the scan's `SELECT` and its own `UPDATE ... WHERE status = ...`). On contention, `claimNextWithRetry`/`retryContendedClaim` re-attempt the *same* candidate row (via a new pinned `claimByID`, not a fresh scan, so retries cannot drift to an unrelated row) up to a bounded attempt budget with jittered, capped exponential backoff, so a transient lost race resolves within the same `ProcessOnce` call instead of ending the batch. Once that per-row budget is exhausted the scan cursor advances past the contended row so remaining eligible candidates in the batch are still processed rather than the batch stalling on one contested row. A shared per-call contended-candidate budget, derived from the requested batch size, additionally prevents total retry work from scaling with an arbitrarily large table of contended rows. Each individual claim database call (initial scan and every pinned retry) is bounded by its own short timeout, distinct from and nested inside the caller's context, so a slow or lock-contended database round trip cannot let one attempt block indefinitely; an initial scan timeout is returned as a distinct wrapped claim-attempt error instead of being misreported as `claimOutcomeNone`, while the caller's own context cancellation/deadline is always distinguished and propagates immediately. `ProcessOnce` also rechecks `ctx.Err()` immediately before and after acquiring its per-instance concurrency semaphore, closing a narrow pre-existing race where Go's pseudo-random `select` between an already-cancelled context and a ready semaphore token could occasionally let an already-cancelled call proceed. New SQLite-backed regression tests cover all three `claimOne` outcomes directly, transient contention resolved via retry with all rows published in one call, persistent contention on one row not blocking other eligible rows in the same call, an all-rows-contended batch returning promptly with no error and no hang, contention work bounded relative to batch size, initial claim timeouts not masquerading as an empty queue, context-deadline honored promptly during persistent contention, and real concurrent goroutines/`Outbox` instances racing to publish many rows and completing them all within one polling round. |
| OJS-GOC-019 | `ojs-gorm` outbox publisher startup validation | P0 | publisher configuration owner; polling lifecycle; operator logs | Implemented. `Run` and `ProcessOnce` share one typed validator for the receiver, database, OJS client, interval, batch size, claim TTL, logger, and process semaphore. `Run` completes preflight before acquiring run ownership or creating its ticker, and returns any permanent configuration error immediately rather than logging it every interval. Regression tests cover nil/missing dependencies and every invalid internal configuration, assert no ticker, enqueue call, log entry, or running state is created, and prove `ProcessOnce` returns the same error class. |
| OJS-GOC-020 | `ojs-gorm` scheduled/outbox job identity validation | P0 | transaction owner; outbox writer; legacy row publisher | Implemented. Because SDK v0.4.0 keeps its validators private, contrib delegates to the exported `Client.Enqueue` validation path through a sentinel in-memory transport that can never perform network I/O; this applies the SDK's exact 255-character job type grammar and 128-character queue grammar, including composed `EnqueueOption` values. `EnqueueAfterCommitErr` validates before adding a pending job, and records invalid input on transaction state so the void helper still forces rollback. `Publish` validates after resolving publish options but before argument encoding or row insertion. Legacy rows are validated immediately after claim and invalid job types/queues become terminal `failed` rows with incremented attempts and `last_error`; valid transport failures remain `pending` and retryable. Tests cover transaction rollback, zero pre-rollback outbox mutation, poison rows with no enqueue attempt, transient retry preservation, and maximum valid type/queue lengths. |

## 3. Compatibility and verification evidence

Existing exported function and method signatures are preserved. Existing exported struct field sets remain unchanged, including `WorkerOptions`, `OutboxEntry`, `APIGatewayEvent`, and `APIGatewayResponse`. New APIs are additive: worker SDK-option constructors plus `Wait`/`Err`, outbox `ProcessOnce`/claim TTL, serverless execution/auth options, handler metadata access, `PostCommitError`, and the strict `EnqueueAfterCommitErr`/`EnqueueAfterCommitJSONErr` variants.

Behavior changes are intentional correctness/security changes:

- nil middleware clients are unavailable rather than present-but-nil;
- readiness means running rather than merely constructed;
- invalid cron/GORM/push input fails before side effects;
- push endpoints require authentication unless the explicitly named insecure local-development option is used;
- top-level post-commit enqueue failures are visible instead of logged and discarded;
- transaction and outbox argument decoding preserves exact JSON numeric values rather than coercing them through `float64`;
- outbox leases cover only the entry actively being published and are renewed while its enqueue call is in flight;
- `EnqueueAfterCommit`/`EnqueueAfterCommitJSON` misuse (nil, unregistered, or non-transaction handles) is logged rather than written to the caller's `*gorm.DB.Error`, so a shared/base handle is never poisoned for subsequent, unrelated queries; a valid active transaction is still marked for rollback via `txState`, unchanged from the existing contract.
- outbox publisher configuration errors fail synchronously before polling starts, while invalid persisted job identities fail terminally instead of being retried forever;
- scheduled and durable enqueue paths reject SDK-invalid job types and queues before scheduling or inserting an outbox row.

Final gates:

| Gate | Result |
|---|---|
| `make tidy` across six modules and five example modules | Passed |
| `make test-all` (`GOWORK=off`, readonly, race detector) | Passed |
| `make build-all` for all modules/examples | Passed |
| `make lint` (`gofmt`, vet, pinned staticcheck, pinned golangci-lint) | Passed, zero golangci issues |
| `git diff --check` | Passed |
| independent read-only diff review | Passed after one option-composition defect was fixed and regression-tested |

Re-verified after the OJS-GOC-017 fix (`ojs-gorm` only changed; all modules re-run since gates are repository-wide):

| Gate | Result |
|---|---|
| `gofmt -l` / `make fmt-check` | Passed |
| `GOWORK=off go mod tidy` (`ojs-gorm`, no diff) | Passed, no dependency changes |
| `make vet` (all 6 modules + 5 example modules) | Passed |
| `make build-all` (all 6 modules + 5 example modules) | Passed |
| `make test-all` (`GOWORK=off`, `-race`, all 6 modules + 5 example modules) | Passed |
| `make staticcheck` (all 6 modules + 5 example modules) | Passed |
| `make golangci-lint` (all 6 modules + 5 example modules) | Passed, `0 issues.` for every module |
| `git diff --check` (changed files) | Passed |

Re-verified after the OJS-GOC-009/OJS-GOC-010 numeric precision and claim-lifecycle fixes:

| Gate | Result |
|---|---|
| `make tidy` across six modules and five example modules | Passed |
| `make test-all` (`GOWORK=off`, `-race`, all modules/examples) | Passed |
| `make build-all` (all modules/examples) | Passed |
| `make lint` (`gofmt`, vet, pinned staticcheck, pinned golangci-lint) | Passed, `0 issues.` for every module |
| focused numeric precision, slow-client publisher race, renewal failure, crash recovery, and exactly-once success regressions | Passed |
| `git diff --check` | Passed |

Re-verified after the OJS-GOC-018 claim-contention fix (`ojs-gorm` only changed; all repository-wide gates re-run since they span every module):

| Gate | Result |
|---|---|
| `make fmt-check` | Passed |
| `GOWORK=off go mod tidy` (`ojs-gorm`, `ojs-gorm/examples`, no diff) | Passed, no dependency changes |
| `make vet` (all 6 modules + 5 example modules) | Passed |
| `make build-all` (all 6 modules + 5 example modules) | Passed |
| `make test` (all 6 modules, `GOWORK=off`, `-race -count=1`) | Passed |
| `make test-examples` (all 5 example modules, `GOWORK=off`, `-race -count=1`) | Passed |
| `make staticcheck` (all 6 modules + 5 example modules) | Passed |
| `make golangci-lint` (all 6 modules + 5 example modules) | Passed, `0 issues.` for every module |
| `ojs-gorm` full suite repeated under `-race -count=1`, isolated stress runs | 40/40 consecutive clean runs (plus 12 earlier), after also pinning `openTestDB`'s connection pool to one connection (see below) |
| `git status` / `git diff --cached` | Passed, no files staged |

Re-verified after the OJS-GOC-019/OJS-GOC-020 preflight and job identity validation fixes:

| Gate | Result |
|---|---|
| `make tidy` across six modules and five example modules | Passed |
| `make test-all` (`GOWORK=off`, `-race -count=1`, all modules/examples) | Passed |
| `make build-all` (all modules/examples) | Passed |
| `make lint` (`gofmt`, vet, pinned staticcheck, pinned golangci-lint) | Passed, `0 issues.` for every module |
| focused permanent-config/no-polling, transaction rollback/no-insert, poison-row, transient retry, and maximum-valid-length regressions | Passed |
| `git diff --check` / `git diff --cached` | Passed, no staged changes |

While repeatedly re-running `ojs-gorm`'s suite to validate OJS-GOC-018, a pre-existing, unrelated flake surfaced intermittently (roughly 1 in 15-20 full-suite runs): `TestOutbox_RenewsSlowEnqueueClaimAndPublishesExactlyOnce` could hit a `database table is locked` (SQLITE_LOCKED) error inside the pre-existing `renewClaim` renewal path when two `Outbox` instances sharing one `openTestDB`-backed `*gorm.DB` performed genuinely concurrent writes against SQLite's shared-cache in-memory mode, which enforces same-process table-level locking that `_busy_timeout` does not cover. This is independent of the OJS-GOC-018 claim/retry logic itself (the failure occurs in `renewClaim`, which OJS-GOC-018 does not modify), but running more tests in the same process made the pre-existing race easier to observe. Fixed by pinning `openTestDB`'s connection pool to exactly one physical connection (`SetMaxOpenConns(1)`/`SetMaxIdleConns(1)`), which serializes all access through the same single connection every test in the package already assumes and eliminates the multi-connection lock class entirely without weakening any test's concurrency assertions (those assert on claim/publish outcomes, not on genuine parallel database I/O). Verified with 40 consecutive full-suite `-race -count=1` runs and 20 additional isolated runs of the previously-flaky test with no further occurrences.

## 4. Deferred

- A database commit and a remote OJS enqueue cannot be one atomic transaction. A crash after OJS accepts an outbox job but before the row is marked published can redeliver it; handlers must remain idempotent.
- `EnqueueAfterCommit` supports the exact top-level GORM transaction handle. Nested savepoint callbacks should use `Publish`, whose row naturally participates in savepoint rollback.
- API Gateway HTTP API v2 has a different event contract and no existing repository API. Adding a second public event model requires an explicit compatibility decision rather than silently changing the existing REST proxy structs.
- Real AWS Lambda/SQS/API Gateway and PostgreSQL multi-process integration matrices require cloud/database infrastructure outside this repository. Local tests cover binding bytes, headers, ordering, races, transactions, claims, and failure states.

## 5. Out of scope

- Sibling repositories, root-organization modules, backend behavior, protocol/schema changes, releases, staging, commits, pushes, merges, and stash operations.
- A shared framework adapter that would erase Chi/Gin/Echo/Fiber context, response, middleware-chain, or testing semantics.
- Breaking replacements for existing void registration/enqueue APIs or exported struct reshaping.
- Provider-specific API Gateway v2, ALB, SNS, Kinesis, or cloud deployment configuration not represented by the existing public package contracts.
