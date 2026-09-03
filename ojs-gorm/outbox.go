package ojsgorm

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	mathrand "math/rand/v2"
	"sync"
	"time"

	ojs "github.com/openjobspec/ojs-go-sdk"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	outboxStatusPending    = "pending"
	outboxStatusProcessing = "processing"
	outboxStatusPublished  = "published"
	outboxStatusFailed     = "failed"

	defaultOutboxInterval  = 5 * time.Second
	defaultOutboxBatchSize = 100
	defaultOutboxClaimTTL  = time.Minute
)

// newOutboxTicker is a test seam for proving invalid publishers fail before
// allocating polling resources.
var newOutboxTicker = time.NewTicker

// OutboxEntry represents a pending job in the outbox table.
type OutboxEntry struct {
	ID        uint            `gorm:"primaryKey;autoIncrement"`
	JobType   string          `gorm:"column:job_type;not null"`
	Args      json.RawMessage `gorm:"column:args;type:jsonb"`
	Queue     string          `gorm:"column:queue"`
	Priority  int             `gorm:"column:priority"`
	Status    string          `gorm:"column:status;default:pending;not null;index"`
	CreatedAt time.Time       `gorm:"column:created_at;autoCreateTime"`
}

// TableName returns the outbox table name.
func (OutboxEntry) TableName() string {
	return "ojs_outbox"
}

// outboxRecord owns publisher-only claim and diagnostic columns while keeping
// the exported OutboxEntry shape source-compatible.
type outboxRecord struct {
	ID          uint            `gorm:"primaryKey;autoIncrement"`
	JobType     string          `gorm:"column:job_type;not null"`
	Args        json.RawMessage `gorm:"column:args;type:jsonb"`
	Queue       string          `gorm:"column:queue"`
	Priority    int             `gorm:"column:priority"`
	Status      string          `gorm:"column:status;default:pending;not null;index"`
	Attempts    int             `gorm:"column:attempts;default:0;not null"`
	LastError   string          `gorm:"column:last_error"`
	ClaimToken  string          `gorm:"column:claim_token;index"`
	ClaimedAt   *time.Time      `gorm:"column:claimed_at;index"`
	PublishedAt *time.Time      `gorm:"column:published_at"`
	CreatedAt   time.Time       `gorm:"column:created_at;autoCreateTime"`
}

func (outboxRecord) TableName() string {
	return OutboxEntry{}.TableName()
}

// OutboxOption configures the Outbox publisher.
type OutboxOption func(*Outbox)

// WithOutboxInterval sets the polling interval for the outbox publisher.
func WithOutboxInterval(d time.Duration) OutboxOption {
	return func(o *Outbox) {
		o.interval = d
	}
}

// WithOutboxBatchSize sets the number of entries to process per poll cycle.
func WithOutboxBatchSize(n int) OutboxOption {
	return func(o *Outbox) {
		o.batchSize = n
	}
}

// WithOutboxLogger sets a custom slog logger for the outbox publisher.
func WithOutboxLogger(logger *slog.Logger) OutboxOption {
	return func(o *Outbox) {
		o.logger = logger
	}
}

// WithOutboxClaimTTL sets how long an interrupted processing claim remains
// exclusive before another publisher may reclaim it.
func WithOutboxClaimTTL(d time.Duration) OutboxOption {
	return func(o *Outbox) {
		o.claimTTL = d
	}
}

// Outbox polls an outbox table for pending jobs and publishes them to OJS.
type Outbox struct {
	db        *gorm.DB
	client    *ojs.Client
	interval  time.Duration
	batchSize int
	claimTTL  time.Duration
	logger    *slog.Logger

	runMu   sync.Mutex
	running bool
	process chan struct{}
}

type outboxConfigError struct {
	err error
}

func (e *outboxConfigError) Error() string {
	return e.err.Error()
}

func (e *outboxConfigError) Unwrap() error {
	return e.err
}

func newOutboxConfigError(format string, args ...any) error {
	return &outboxConfigError{err: fmt.Errorf(format, args...)}
}

func isOutboxConfigError(err error) bool {
	var configErr *outboxConfigError
	return errors.As(err, &configErr)
}

// NewOutbox creates a new outbox publisher.
func NewOutbox(db *gorm.DB, client *ojs.Client, opts ...OutboxOption) *Outbox {
	o := &Outbox{
		db:        db,
		client:    client,
		interval:  defaultOutboxInterval,
		batchSize: defaultOutboxBatchSize,
		claimTTL:  defaultOutboxClaimTTL,
		logger:    slog.Default(),
		process:   make(chan struct{}, 1),
	}
	o.process <- struct{}{}
	for _, opt := range opts {
		if opt != nil {
			opt(o)
		}
	}
	if o.interval <= 0 {
		o.interval = defaultOutboxInterval
	}
	if o.batchSize <= 0 {
		o.batchSize = defaultOutboxBatchSize
	}
	if o.claimTTL <= 0 {
		o.claimTTL = defaultOutboxClaimTTL
	}
	if o.logger == nil {
		o.logger = slog.Default()
	}
	return o
}

// AutoMigrate creates or updates the outbox table.
func (o *Outbox) AutoMigrate() error {
	if o == nil || o.db == nil {
		return fmt.Errorf("ojsgorm: outbox database must not be nil")
	}
	return o.db.AutoMigrate(&outboxRecord{})
}

// Run starts the outbox publisher. It processes a batch immediately, then
// polls at the configured interval until the context is cancelled.
func (o *Outbox) Run(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("ojsgorm: context must not be nil")
	}
	if err := o.validate(); err != nil {
		return err
	}
	if err := o.beginRun(); err != nil {
		return err
	}
	defer o.endRun()

	if err := o.processAndLog(ctx); err != nil {
		return err
	}

	ticker := newOutboxTicker(o.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := o.processAndLog(ctx); err != nil {
				return err
			}
		}
	}
}

func (o *Outbox) processAndLog(ctx context.Context) error {
	err := o.ProcessOnce(ctx)
	if err == nil || errors.Is(err, context.Canceled) {
		return nil
	}
	if isOutboxConfigError(err) {
		return err
	}
	o.logger.Error("outbox batch processing failed", "error", err)
	return nil
}

// ProcessOnce claims and processes at most one configured batch. Entries are
// claimed individually immediately before processing so a slow enqueue does
// not hold leases for the rest of the batch. Concurrent calls on the same
// Outbox are serialized with context-aware admission.
func (o *Outbox) ProcessOnce(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("ojsgorm: context must not be nil")
	}
	if err := o.validate(); err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-o.process:
	}
	defer func() { o.process <- struct{}{} }()
	// The select above can race a ready o.process token against an
	// already-cancelled context (Go picks a ready case at random), so check
	// again now that admission is held to guarantee cancellation wins.
	if err := ctx.Err(); err != nil {
		return err
	}

	var errs []error
	var afterID uint
	remainingContendedCandidates := o.batchSize
	for range o.batchSize {
		entry, claimed, err := o.claimNextWithRetry(ctx, &afterID, &remainingContendedCandidates)
		if err != nil {
			errs = append(errs, err)
			break
		}
		if !claimed {
			break
		}
		if err := o.processEntry(ctx, entry); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (o *Outbox) validate() error {
	switch {
	case o == nil:
		return newOutboxConfigError("ojsgorm: outbox must not be nil")
	case o.db == nil:
		return newOutboxConfigError("ojsgorm: outbox database must not be nil")
	case o.client == nil:
		return newOutboxConfigError("ojsgorm: outbox OJS client must not be nil")
	case o.interval <= 0:
		return newOutboxConfigError("ojsgorm: outbox interval must be positive")
	case o.batchSize <= 0:
		return newOutboxConfigError("ojsgorm: outbox batch size must be positive")
	case o.claimTTL <= 0:
		return newOutboxConfigError("ojsgorm: outbox claim TTL must be positive")
	case o.logger == nil:
		return newOutboxConfigError("ojsgorm: outbox logger must not be nil")
	case o.process == nil:
		return newOutboxConfigError("ojsgorm: outbox process semaphore must not be nil")
	case cap(o.process) == 0:
		return newOutboxConfigError("ojsgorm: outbox process semaphore must be buffered")
	default:
		return nil
	}
}

// claimOutcome distinguishes why claimOne did or did not return a claimed
// entry so callers can react differently to "nothing left to do" versus
// "lost a race for an eligible row and should try again".
type claimOutcome int

const (
	// claimOutcomeClaimed means an eligible entry was exclusively claimed.
	claimOutcomeClaimed claimOutcome = iota
	// claimOutcomeNone means no eligible entry exists after afterID.
	claimOutcomeNone
	// claimOutcomeContended means an eligible candidate existed but another
	// publisher won the optimistic claim update first.
	claimOutcomeContended
)

const (
	// maxClaimContentionAttempts bounds retries against a single contended
	// candidate before the publisher gives up on that row and advances past
	// it, guaranteeing a same-call batch always makes forward progress.
	maxClaimContentionAttempts = 8
	claimBackoffBase           = 2 * time.Millisecond
	claimBackoffMax            = 50 * time.Millisecond

	// claimAttemptTimeout bounds how long any single claim database call
	// (the initial scan or a pinned retry) may take. Without this, a slow
	// or lock-contended database round trip (e.g. SQLite's internal
	// busy-timeout wait racing another connection) could make a single
	// attempt block far longer than our own backoff, and stacking that
	// across maxClaimContentionAttempts retries could turn a bounded retry
	// budget into a multi-second stall. Capping each attempt keeps total
	// worst-case latency for one contended candidate bounded and
	// predictable regardless of underlying driver/lock behavior.
	claimAttemptTimeout = 500 * time.Millisecond
)

var errClaimAttemptTimedOut = errors.New("outbox claim attempt timed out")

// claimAfterSelectHook is a package-level test seam (nil in production). See
// its call site in claimOne for details. It receives the active claim
// transaction so tests can deterministically simulate a competing claim
// (e.g. another publisher's optimistic update) landing between our SELECT
// and our UPDATE, without relying on real goroutine timing races that would
// otherwise deadlock against SQLite's single-writer lock. Tests must set it
// before starting any claim activity that could observe it and reset it to
// nil once done, since it is read without additional synchronization.
var claimAfterSelectHook func(tx *gorm.DB, candidateID uint)

// claimByIDHook is a package-level test seam (nil in production), symmetric
// to claimAfterSelectHook but for the pinned retry path in claimByID. It
// lets tests deterministically release a simulated competing claim exactly
// once a retry attempt begins, instead of depending on real wall-clock claim
// TTL expiry racing against jittered backoff timing. Tests must set it
// before starting any claim activity that could observe it and reset it to
// nil once done, since it is read without additional synchronization.
var claimByIDHook func(tx *gorm.DB, id uint)

// claimOne attempts to claim exactly one eligible outbox entry with id >
// afterID. It reports which of the three claimOutcome states occurred so
// callers can distinguish a claimed entry, the end of eligible work, and a
// lost optimistic-claim race (contention) that should be retried against
// other candidates rather than treated as the end of the batch.
func (o *Outbox) claimOne(ctx context.Context, afterID uint) (*outboxRecord, claimOutcome, uint, error) {
	token, err := newClaimToken()
	if err != nil {
		return nil, claimOutcomeNone, 0, fmt.Errorf("ojsgorm: generating outbox claim token: %w", err)
	}
	now := time.Now().UTC()
	staleBefore := now.Add(-o.claimTTL)
	var claimed outboxRecord
	var candidateID uint
	outcome := claimOutcomeNone

	attemptCtx, cancel := context.WithTimeout(ctx, claimAttemptTimeout)
	defer cancel()

	err = o.db.WithContext(attemptCtx).Transaction(func(tx *gorm.DB) error {
		query := tx.
			Where("id > ?", afterID).
			Where(
				"status = ? OR (status = ? AND (claimed_at IS NULL OR claimed_at < ?))",
				outboxStatusPending,
				outboxStatusProcessing,
				staleBefore,
			).
			Order("id ASC").
			Limit(1)
		if tx.Name() == "postgres" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"})
		}

		var candidate outboxRecord
		result := query.Find(&candidate)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			outcome = claimOutcomeNone
			return nil
		}
		candidateID = candidate.ID

		// Test-only synchronization point: allows tests to deterministically
		// inject a competing claim between our SELECT and our UPDATE so the
		// claimOutcomeContended path can be exercised without relying on
		// real timing races. Always nil outside tests.
		if claimAfterSelectHook != nil {
			claimAfterSelectHook(tx, candidate.ID)
		}

		result = tx.Model(&outboxRecord{}).
			Where("id = ?", candidate.ID).
			Where(
				"status = ? OR (status = ? AND (claimed_at IS NULL OR claimed_at < ?))",
				outboxStatusPending,
				outboxStatusProcessing,
				staleBefore,
			).
			Updates(map[string]any{
				"status":      outboxStatusProcessing,
				"claim_token": token,
				"claimed_at":  now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			// Another publisher claimed this exact row between our SELECT
			// and our UPDATE. This is contention, not "no eligible work".
			outcome = claimOutcomeContended
			return nil
		}

		result = tx.
			Where("id = ? AND claim_token = ? AND status = ?", candidate.ID, token, outboxStatusProcessing).
			Find(&claimed)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			// Extremely unlikely, but if the row changed again before our
			// re-read, treat it the same as lost contention rather than a
			// hard error.
			outcome = claimOutcomeContended
			return nil
		}
		outcome = claimOutcomeClaimed
		return nil
	})
	if err != nil {
		if attemptCtx.Err() != nil && ctx.Err() == nil {
			// Our own bounded per-attempt timeout fired, not the caller's
			// context. This means the underlying database call (e.g. a
			// SQLite busy-wait racing another connection) took too long,
			// not that the row is genuinely unclaimable. Treat it the same
			// as "not claimed this attempt" rather than a hard failure so
			// Do not collapse a timed-out database attempt into
			// claimOutcomeNone: that would make ProcessOnce silently report
			// an empty queue and abandon other eligible work. Surface a
			// distinct retryable error instead.
			return nil, claimOutcomeNone, 0, fmt.Errorf("%w: claiming next outbox entry", errClaimAttemptTimedOut)
		}
		return nil, claimOutcomeNone, 0, fmt.Errorf("ojsgorm: claiming next outbox entry: %w", err)
	}
	switch outcome {
	case claimOutcomeClaimed:
		return &claimed, claimOutcomeClaimed, claimed.ID, nil
	case claimOutcomeContended:
		return nil, claimOutcomeContended, candidateID, nil
	default:
		return nil, claimOutcomeNone, 0, nil
	}
}

// claimByID attempts to claim exactly one specific outbox entry by id,
// without scanning. It is used to retry a candidate that was just lost to
// contention: both "another publisher still holds it" and "it was not
// eligible at all" collapse into claimOutcomeContended, since from a
// retrying caller's perspective both simply mean "not claimable yet, keep
// waiting within the attempt budget". This keeps retries pinned to the
// specific row that lost the race instead of drifting to a different,
// unrelated candidate that a plain rescan might pick up.
func (o *Outbox) claimByID(ctx context.Context, id uint) (*outboxRecord, claimOutcome, error) {
	token, err := newClaimToken()
	if err != nil {
		return nil, claimOutcomeContended, fmt.Errorf("ojsgorm: generating outbox claim token: %w", err)
	}
	now := time.Now().UTC()
	staleBefore := now.Add(-o.claimTTL)
	var claimed outboxRecord
	outcome := claimOutcomeContended

	attemptCtx, cancel := context.WithTimeout(ctx, claimAttemptTimeout)
	defer cancel()

	err = o.db.WithContext(attemptCtx).Transaction(func(tx *gorm.DB) error {
		// Test-only synchronization point: allows tests to deterministically
		// simulate a competitor's claim being released/expiring exactly once
		// a retry begins, instead of depending on real wall-clock TTL
		// expiry racing against backoff timing. Always nil outside tests.
		if claimByIDHook != nil {
			claimByIDHook(tx, id)
		}

		result := tx.Model(&outboxRecord{}).
			Where("id = ?", id).
			Where(
				"status = ? OR (status = ? AND (claimed_at IS NULL OR claimed_at < ?))",
				outboxStatusPending,
				outboxStatusProcessing,
				staleBefore,
			).
			Updates(map[string]any{
				"status":      outboxStatusProcessing,
				"claim_token": token,
				"claimed_at":  now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			outcome = claimOutcomeContended
			return nil
		}

		result = tx.
			Where("id = ? AND claim_token = ? AND status = ?", id, token, outboxStatusProcessing).
			Find(&claimed)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			outcome = claimOutcomeContended
			return nil
		}
		outcome = claimOutcomeClaimed
		return nil
	})
	if err != nil {
		if attemptCtx.Err() != nil && ctx.Err() == nil {
			// Same reasoning as claimOne: our own bounded per-attempt
			// timeout fired, not the caller's context, so treat this
			// attempt as still-contended (retryable within budget) rather
			// than a hard error.
			return nil, claimOutcomeContended, nil
		}
		return nil, claimOutcomeContended, fmt.Errorf("ojsgorm: claiming outbox entry %d: %w", id, err)
	}
	if outcome == claimOutcomeClaimed {
		return &claimed, claimOutcomeClaimed, nil
	}
	return nil, claimOutcomeContended, nil
}

// claimNextWithRetry claims the next eligible entry after *afterID. When the
// scan's chosen candidate is lost to contention, it is retried in place (the
// same specific row, via claimByID) with a bounded attempt budget and
// jittered backoff so a transient competing claim resolves within this same
// ProcessOnce call instead of ending the batch on the first lost race. Only
// once that budget is exhausted does afterID advance past the contended row
// so the scan continues toward other eligible candidates rather than
// spinning forever on a single contested one. Context cancellation and
// deadlines are always respected, both between retries and inside every
// database call this makes.
func (o *Outbox) claimNextWithRetry(
	ctx context.Context,
	afterID *uint,
	remainingContendedCandidates *int,
) (*outboxRecord, bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}

		entry, outcome, candidateID, err := o.claimOne(ctx, *afterID)
		if err != nil {
			return nil, false, err
		}

		switch outcome {
		case claimOutcomeClaimed:
			*afterID = entry.ID
			return entry, true, nil
		case claimOutcomeNone:
			return nil, false, nil
		}

		// claimOutcomeContended: retry this exact candidate with bounded
		// attempts and backoff before giving up on it and continuing the
		// scan past it. The per-call candidate budget prevents total work
		// from scaling with an arbitrarily large table of contended rows.
		if *remainingContendedCandidates <= 0 {
			return nil, false, nil
		}
		*remainingContendedCandidates--
		claimedByRetry, resolved, err := o.retryContendedClaim(ctx, candidateID)
		if err != nil {
			return nil, false, err
		}
		if resolved {
			*afterID = claimedByRetry.ID
			return claimedByRetry, true, nil
		}
		// Budget exhausted for this candidate; skip past it and keep
		// scanning for other eligible candidates in the same call.
		*afterID = candidateID
	}
}

// retryContendedClaim retries claiming a single previously-contended
// candidate up to maxClaimContentionAttempts times, waiting a bounded,
// jittered backoff between attempts. It returns resolved=true only if the
// retry succeeded; resolved=false means the attempt budget was exhausted
// without a hard error, and the caller should move on to other candidates.
func (o *Outbox) retryContendedClaim(ctx context.Context, candidateID uint) (*outboxRecord, bool, error) {
	for attempt := 1; attempt <= maxClaimContentionAttempts; attempt++ {
		if err := claimContentionBackoff(ctx, attempt); err != nil {
			return nil, false, err
		}
		entry, outcome, err := o.claimByID(ctx, candidateID)
		if err != nil {
			return nil, false, err
		}
		if outcome == claimOutcomeClaimed {
			return entry, true, nil
		}
	}
	return nil, false, nil
}

// claimContentionBackoff waits a bounded, jittered interval before retrying
// a contended claim. It always honors context cancellation/deadlines so a
// caller waiting on many concurrent publishers can never spin or livelock.
func claimContentionBackoff(ctx context.Context, attempt int) error {
	if attempt < 1 {
		attempt = 1
	}
	d := claimBackoffBase * time.Duration(uint(1)<<uint(min(attempt-1, 16)))
	if d <= 0 || d > claimBackoffMax {
		d = claimBackoffMax
	}
	// Jitter only spreads out retry timing to reduce contention between
	// competing publishers; it is not security-sensitive, so the weaker,
	// faster math/rand/v2 generator is intentional here.
	jitter := d/2 + time.Duration(mathrand.Int64N(int64(d/2)+1)) //nolint:gosec // jitter timing only, not security-sensitive

	timer := time.NewTimer(jitter)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (o *Outbox) processEntry(ctx context.Context, entry *outboxRecord) error {
	if err := validateStoredJob(entry.JobType, entry.Queue); err != nil {
		updateErr := o.finishClaim(ctx, entry, outboxStatusFailed, err, nil)
		return errors.Join(
			fmt.Errorf("ojsgorm: validating outbox entry %d: %w", entry.ID, err),
			updateErr,
		)
	}

	args, err := decodeArgs(entry.Args)
	if err != nil {
		updateErr := o.finishClaim(ctx, entry, outboxStatusFailed, err, nil)
		return errors.Join(
			fmt.Errorf("ojsgorm: decoding outbox entry %d args: %w", entry.ID, err),
			updateErr,
		)
	}

	var opts []ojs.EnqueueOption
	if entry.Queue != "" {
		opts = append(opts, ojs.WithQueue(entry.Queue))
	}
	if entry.Priority != 0 {
		opts = append(opts, ojs.WithPriority(entry.Priority))
	}

	enqueueCtx, cancelEnqueue := context.WithCancel(ctx)
	renewalDone := make(chan error, 1)
	go func() {
		renewalErr := o.renewClaimUntilCanceled(enqueueCtx, entry)
		if renewalErr != nil {
			cancelEnqueue()
		}
		renewalDone <- renewalErr
	}()

	_, enqueueErr := o.client.Enqueue(enqueueCtx, entry.JobType, args, opts...)
	cancelEnqueue()
	renewalErr := <-renewalDone

	if enqueueErr != nil {
		cause := errors.Join(enqueueErr, renewalErr)
		updateErr := o.finishClaim(ctx, entry, outboxStatusPending, cause, nil)
		return errors.Join(
			fmt.Errorf("ojsgorm: enqueueing outbox entry %d (%s): %w", entry.ID, entry.JobType, enqueueErr),
			wrapRenewalError(entry.ID, renewalErr),
			updateErr,
		)
	}

	publishedAt := time.Now().UTC()
	finishErr := o.finishClaim(ctx, entry, outboxStatusPublished, nil, &publishedAt)
	if finishErr != nil {
		finishErr = fmt.Errorf("ojsgorm: recording published outbox entry %d: %w", entry.ID, finishErr)
	}
	return errors.Join(wrapRenewalError(entry.ID, renewalErr), finishErr)
}

func (o *Outbox) renewClaimUntilCanceled(ctx context.Context, entry *outboxRecord) error {
	interval := o.claimTTL / 3
	if interval <= 0 {
		interval = time.Nanosecond
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := o.renewClaim(ctx, entry, interval); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
		}
	}
}

func (o *Outbox) renewClaim(ctx context.Context, entry *outboxRecord, timeout time.Duration) error {
	const maxRenewalTimeout = 5 * time.Second
	if timeout <= 0 {
		timeout = time.Nanosecond
	}
	if timeout > maxRenewalTimeout {
		timeout = maxRenewalTimeout
	}

	renewCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result := o.db.WithContext(renewCtx).Model(&outboxRecord{}).
		Where("id = ? AND claim_token = ? AND status = ?", entry.ID, entry.ClaimToken, outboxStatusProcessing).
		Update("claimed_at", time.Now().UTC())
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("claim was lost before renewal")
	}
	return nil
}

func wrapRenewalError(entryID uint, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("ojsgorm: renewing claim for outbox entry %d: %w", entryID, err)
}

func (o *Outbox) finishClaim(
	ctx context.Context,
	entry *outboxRecord,
	status string,
	cause error,
	publishedAt *time.Time,
) error {
	updates := map[string]any{
		"status":       status,
		"claim_token":  "",
		"claimed_at":   nil,
		"published_at": publishedAt,
	}
	if cause != nil {
		updates["attempts"] = gorm.Expr("attempts + 1")
		updates["last_error"] = cause.Error()
	} else {
		updates["last_error"] = ""
	}

	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	result := o.db.WithContext(finishCtx).Model(&outboxRecord{}).
		Where("id = ? AND claim_token = ? AND status = ?", entry.ID, entry.ClaimToken, outboxStatusProcessing).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("claim was lost before status update")
	}
	return nil
}

func (o *Outbox) beginRun() error {
	o.runMu.Lock()
	defer o.runMu.Unlock()
	if o.running {
		return fmt.Errorf("ojsgorm: outbox is already running")
	}
	o.running = true
	return nil
}

func (o *Outbox) endRun() {
	o.runMu.Lock()
	o.running = false
	o.runMu.Unlock()
}

func newClaimToken() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

// Publish adds an entry to the outbox table within the given transaction.
// The entry will be picked up and published by the outbox publisher.
func Publish(tx *gorm.DB, jobType string, args ojs.Args, opts ...publishOption) error {
	if err := validateActiveTransaction(tx, "Publish"); err != nil {
		return err
	}

	publicEntry := OutboxEntry{
		JobType: jobType,
		Status:  outboxStatusPending,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&publicEntry)
		}
	}
	if err := validateStoredJob(publicEntry.JobType, publicEntry.Queue); err != nil {
		return fmt.Errorf("ojsgorm: validating outbox job: %w", err)
	}

	raw, err := json.Marshal(args)
	if err != nil {
		return fmt.Errorf("ojsgorm: encoding outbox args: %w", err)
	}
	entry := outboxRecord{
		JobType:  publicEntry.JobType,
		Args:     raw,
		Queue:    publicEntry.Queue,
		Priority: publicEntry.Priority,
		Status:   publicEntry.Status,
	}

	if err := tx.Create(&entry).Error; err != nil {
		return fmt.Errorf("ojsgorm: inserting outbox entry: %w", err)
	}
	return nil
}

type publishOption func(*OutboxEntry)

// WithPublishQueue sets the queue for the outbox entry.
func WithPublishQueue(queue string) publishOption {
	return func(e *OutboxEntry) {
		e.Queue = queue
	}
}

// WithPublishPriority sets the priority for the outbox entry.
func WithPublishPriority(priority int) publishOption {
	return func(e *OutboxEntry) {
		e.Priority = priority
	}
}
