package ojsgorm

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ojs "github.com/openjobspec/ojs-go-sdk"
	"gorm.io/gorm"
)

// resetClaimHook clears the package-level test seam once the calling test
// (and everything it started) is done observing it.
func resetClaimHook(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { claimAfterSelectHook = nil })
}

// TestOutbox_ClaimOne_DistinctStates proves claimOne reports three distinct,
// separately observable outcomes: an entry was claimed, no eligible entry
// exists, and an eligible candidate was found but lost the optimistic claim
// race to a competitor (contention). Losing the race must not be reported
// the same way as "nothing left to claim".
func TestOutbox_ClaimOne_DistinctStates(t *testing.T) {
	t.Run("Claimed", func(t *testing.T) {
		db := openTestDB(t)
		outbox := NewOutbox(db, newTestClient(t, "http://127.0.0.1:0"))
		publishTestJob(t, db, "claim.me", ojs.Args{"id": 1})

		entry, outcome, candidateID, err := outbox.claimOne(context.Background(), 0)
		if err != nil {
			t.Fatalf("claimOne() error = %v", err)
		}
		if outcome != claimOutcomeClaimed {
			t.Fatalf("outcome = %v, want claimOutcomeClaimed", outcome)
		}
		if entry == nil {
			t.Fatal("entry = nil, want claimed entry")
		}
		if candidateID != entry.ID {
			t.Fatalf("candidateID = %d, want %d (entry.ID)", candidateID, entry.ID)
		}
		if entry.Status != outboxStatusProcessing || entry.ClaimToken == "" {
			t.Fatalf("claimed entry = %+v, want processing status with a claim token", entry)
		}
	})

	t.Run("NoEligibleEntry", func(t *testing.T) {
		db := openTestDB(t)
		outbox := NewOutbox(db, newTestClient(t, "http://127.0.0.1:0"))
		// No rows exist at all.

		entry, outcome, candidateID, err := outbox.claimOne(context.Background(), 0)
		if err != nil {
			t.Fatalf("claimOne() error = %v", err)
		}
		if outcome != claimOutcomeNone {
			t.Fatalf("outcome = %v, want claimOutcomeNone", outcome)
		}
		if entry != nil {
			t.Fatalf("entry = %+v, want nil", entry)
		}
		if candidateID != 0 {
			t.Fatalf("candidateID = %d, want 0", candidateID)
		}
	})

	t.Run("Contended", func(t *testing.T) {
		db := openTestDB(t)
		outbox := NewOutbox(db, newTestClient(t, "http://127.0.0.1:0"))
		publishTestJob(t, db, "contended.me", ojs.Args{"id": 1})
		resetClaimHook(t)

		// Deterministically simulate a competing publisher winning the
		// optimistic claim race between our SELECT and our UPDATE by
		// mutating the row (via the same in-flight transaction, which is
		// indistinguishable at the SQL level from a truly concurrent writer
		// having already committed) immediately after the candidate is
		// selected.
		claimAfterSelectHook = func(tx *gorm.DB, candidateID uint) {
			if err := tx.Model(&outboxRecord{}).
				Where("id = ?", candidateID).
				Updates(map[string]any{
					"status":      outboxStatusProcessing,
					"claim_token": "competitor-token",
					"claimed_at":  time.Now().UTC(),
				}).Error; err != nil {
				t.Fatalf("simulate competing claim error = %v", err)
			}
		}

		entry, outcome, candidateID, err := outbox.claimOne(context.Background(), 0)
		if err != nil {
			t.Fatalf("claimOne() error = %v", err)
		}
		if outcome != claimOutcomeContended {
			t.Fatalf("outcome = %v, want claimOutcomeContended", outcome)
		}
		if entry != nil {
			t.Fatalf("entry = %+v, want nil on contention", entry)
		}
		if candidateID == 0 {
			t.Fatal("candidateID = 0, want the contended row's id")
		}

		var record outboxRecord
		if err := db.First(&record, candidateID).Error; err != nil {
			t.Fatalf("load contended record error = %v", err)
		}
		if record.ClaimToken != "competitor-token" {
			t.Fatalf("record.ClaimToken = %q, want competitor-token (our claim must not have won)", record.ClaimToken)
		}
	})
}

// TestOutbox_ProcessOnce_RetriesContentionWithinSameCallAndPublishesAllRows
// proves that a lost optimistic-claim race does not end the batch. A
// competitor "steals" one row's claim, then abandons it; ProcessOnce must
// retry that contention in place (bounded attempts + backoff) within the
// same call, recover the abandoned claim once it goes stale, and publish
// every row in the same ProcessOnce call.
func TestOutbox_ProcessOnce_RetriesContentionWithinSameCallAndPublishesAllRows(t *testing.T) {
	db := openTestDB(t)
	server, calls, _ := newEnqueueServer(t)
	// A generous claim TTL keeps this test independent of wall-clock timing
	// entirely: the abandoned claim below is released deterministically via
	// claimByIDHook on the first retry attempt, not by waiting for it to
	// become stale, so no real-time race with backoff scheduling is
	// possible.
	outbox := NewOutbox(db, newTestClient(t, server.URL), WithOutboxBatchSize(10), WithOutboxClaimTTL(time.Hour))

	publishTestJob(t, db, "row.one", ojs.Args{"n": 1})
	publishTestJob(t, db, "row.two.contended", ojs.Args{"n": 2})
	publishTestJob(t, db, "row.three", ojs.Args{"n": 3})

	var stolenID uint
	var stealOnce, releaseOnce sync.Once
	resetClaimHook(t)
	t.Cleanup(func() { claimByIDHook = nil })

	claimAfterSelectHook = func(tx *gorm.DB, candidateID uint) {
		var record outboxRecord
		if err := tx.First(&record, candidateID).Error; err != nil {
			t.Fatalf("load candidate for theft check error = %v", err)
		}
		if record.JobType != "row.two.contended" {
			return
		}
		stealOnce.Do(func() {
			stolenID = candidateID
			// Steal the claim once, simulating a competitor winning the
			// optimistic race first.
			if err := tx.Model(&outboxRecord{}).
				Where("id = ?", candidateID).
				Updates(map[string]any{
					"status":      outboxStatusProcessing,
					"claim_token": "abandoned-competitor-token",
					"claimed_at":  time.Now().UTC(),
				}).Error; err != nil {
				t.Fatalf("simulate abandoned competing claim error = %v", err)
			}
		})
	}
	claimByIDHook = func(tx *gorm.DB, id uint) {
		if id != stolenID || stolenID == 0 {
			return
		}
		// Deterministically simulate the competitor's claim being abandoned
		// (e.g. it crashed or its own claim silently expired) exactly once,
		// on the first pinned retry attempt, instead of depending on real
		// time elapsing past claimTTL during backoff. The retry loop must
		// still recover it and publish it in this same ProcessOnce call.
		releaseOnce.Do(func() {
			if err := tx.Model(&outboxRecord{}).
				Where("id = ?", id).
				Updates(map[string]any{
					"status":      outboxStatusPending,
					"claim_token": "",
					"claimed_at":  nil,
				}).Error; err != nil {
				t.Fatalf("simulate abandoned claim release error = %v", err)
			}
		})
	}

	if err := outbox.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce() error = %v", err)
	}
	if stolenID == 0 {
		t.Fatal("contention was never exercised; test setup did not target the expected row")
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("enqueue calls = %d, want 3 (all rows published in the same call)", got)
	}

	var records []outboxRecord
	if err := db.Order("id ASC").Find(&records).Error; err != nil {
		t.Fatalf("load records error = %v", err)
	}
	for _, record := range records {
		if record.Status != outboxStatusPublished || record.PublishedAt == nil {
			t.Fatalf("record %d (%s) status = %q, published_at = %v, want published",
				record.ID, record.JobType, record.Status, record.PublishedAt)
		}
	}
}

// TestOutbox_ProcessOnce_SkipsPersistentlyContendedRowButProcessesOthers
// proves that when contention on a specific row cannot be resolved within
// the bounded attempt budget, ProcessOnce advances past it and still
// processes every other eligible candidate in the same call instead of
// ending the batch.
func TestOutbox_ProcessOnce_SkipsPersistentlyContendedRowButProcessesOthers(t *testing.T) {
	db := openTestDB(t)
	server, calls, _ := newEnqueueServer(t)
	outbox := NewOutbox(db, newTestClient(t, server.URL), WithOutboxBatchSize(10), WithOutboxClaimTTL(time.Hour))

	publishTestJob(t, db, "row.before", ojs.Args{"n": 1})
	publishTestJob(t, db, "row.stuck", ojs.Args{"n": 2})
	publishTestJob(t, db, "row.after", ojs.Args{"n": 3})

	resetClaimHook(t)
	var stuckID uint
	claimAfterSelectHook = func(tx *gorm.DB, candidateID uint) {
		var record outboxRecord
		if err := tx.First(&record, candidateID).Error; err != nil {
			t.Fatalf("load candidate for stuck check error = %v", err)
		}
		if record.JobType != "row.stuck" {
			return
		}
		stuckID = candidateID
		// Re-claim with a fresh claimed_at on every single attempt so the
		// row never becomes stale/reclaimable and contention persists past
		// the retry budget within one ProcessOnce call.
		if err := tx.Model(&outboxRecord{}).
			Where("id = ?", candidateID).
			Updates(map[string]any{
				"status":      outboxStatusProcessing,
				"claim_token": "permanent-competitor-token",
				"claimed_at":  time.Now().UTC(),
			}).Error; err != nil {
			t.Fatalf("simulate persistent competing claim error = %v", err)
		}
	}

	if err := outbox.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce() error = %v", err)
	}
	if stuckID == 0 {
		t.Fatal("persistent contention was never exercised; test setup did not target the expected row")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("enqueue calls = %d, want 2 (all rows except the stuck one)", got)
	}

	var stuck outboxRecord
	if err := db.First(&stuck, stuckID).Error; err != nil {
		t.Fatalf("load stuck record error = %v", err)
	}
	if stuck.Status != outboxStatusProcessing || stuck.ClaimToken != "permanent-competitor-token" {
		t.Fatalf("stuck record = %+v, want left untouched under the competitor's claim", stuck)
	}

	var others []outboxRecord
	if err := db.Where("job_type IN ?", []string{"row.before", "row.after"}).Order("id ASC").Find(&others).Error; err != nil {
		t.Fatalf("load other records error = %v", err)
	}
	if len(others) != 2 {
		t.Fatalf("other records count = %d, want 2", len(others))
	}
	for _, record := range others {
		if record.Status != outboxStatusPublished || record.PublishedAt == nil {
			t.Fatalf("record %d (%s) status = %q, want published", record.ID, record.JobType, record.Status)
		}
	}
}

// TestOutbox_ProcessOnce_AllRowsContendedReturnsNoErrorWithoutHanging is the
// all-contended case: every eligible row is permanently held by a
// competitor. ProcessOnce must not error, must not publish anything, and
// must return promptly (proving no spin/livelock) instead of hanging or
// busy-looping until some external timeout.
func TestOutbox_ProcessOnce_AllRowsContendedReturnsNoErrorWithoutHanging(t *testing.T) {
	db := openTestDB(t)
	server, calls, _ := newEnqueueServer(t)
	outbox := NewOutbox(db, newTestClient(t, server.URL), WithOutboxBatchSize(10), WithOutboxClaimTTL(time.Hour))

	for i := range 4 {
		publishTestJob(t, db, fmt.Sprintf("row_%d", i), ojs.Args{"n": i})
	}

	resetClaimHook(t)
	var contentionEvents atomic.Int64
	claimAfterSelectHook = func(tx *gorm.DB, candidateID uint) {
		contentionEvents.Add(1)
		if err := tx.Model(&outboxRecord{}).
			Where("id = ?", candidateID).
			Updates(map[string]any{
				"status":      outboxStatusProcessing,
				"claim_token": "everyone-else's-token",
				"claimed_at":  time.Now().UTC(),
			}).Error; err != nil {
			t.Fatalf("simulate all-contended claim error = %v", err)
		}
	}

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		done <- outbox.ProcessOnce(context.Background())
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ProcessOnce() error = %v, want nil (contention alone is not a failure)", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ProcessOnce() did not return; suspected spin/livelock under all-contended rows")
	}
	elapsed := time.Since(start)
	// A generous upper bound: 4 rows * bounded retry budget with capped
	// backoff should resolve in well under a second, not hang indefinitely.
	if elapsed > 3*time.Second {
		t.Fatalf("ProcessOnce() took %s to give up on all-contended rows, want a bounded, fast return", elapsed)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("enqueue calls = %d, want 0 (nothing should have been claimable)", got)
	}
	if contentionEvents.Load() == 0 {
		t.Fatal("contention hook was never invoked; test did not exercise all-contended rows")
	}

	var pendingLike int64
	if err := db.Model(&outboxRecord{}).
		Where("status = ? AND claim_token = ?", outboxStatusProcessing, "everyone-else's-token").
		Count(&pendingLike).Error; err != nil {
		t.Fatalf("count contended rows error = %v", err)
	}
	if pendingLike != 4 {
		t.Fatalf("rows still held by the competitor = %d, want 4", pendingLike)
	}
}

// TestOutbox_ProcessOnce_RespectsContextDeadlineDuringPersistentContention
// proves that PublishPending-style batch processing honors context
// cancellation/deadlines while retrying contention, instead of waiting out
// the full retry/backoff budget.
func TestOutbox_ProcessOnce_RespectsContextDeadlineDuringPersistentContention(t *testing.T) {
	db := openTestDB(t)
	server, calls, _ := newEnqueueServer(t)
	outbox := NewOutbox(db, newTestClient(t, server.URL), WithOutboxBatchSize(10), WithOutboxClaimTTL(time.Hour))
	publishTestJob(t, db, "contended.forever", ojs.Args{"n": 1})

	resetClaimHook(t)
	claimAfterSelectHook = func(tx *gorm.DB, candidateID uint) {
		if err := tx.Model(&outboxRecord{}).
			Where("id = ?", candidateID).
			Updates(map[string]any{
				"status":      outboxStatusProcessing,
				"claim_token": "forever-competitor-token",
				"claimed_at":  time.Now().UTC(),
			}).Error; err != nil {
			t.Fatalf("simulate perpetual contention error = %v", err)
		}
	}

	const deadline = 20 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()

	start := time.Now()
	err := outbox.ProcessOnce(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("ProcessOnce() error = nil, want context deadline error under perpetual contention")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ProcessOnce() error = %v, want context.DeadlineExceeded", err)
	}
	// The full contention/backoff budget for even one row is much larger
	// than the deadline; a bounded, responsive return proves cancellation
	// is checked between (and within) retries rather than only at the end.
	if elapsed > 2*time.Second {
		t.Fatalf("ProcessOnce() took %s to honor a %s deadline under contention", elapsed, deadline)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("enqueue calls = %d, want 0", got)
	}
}

func TestOutbox_ProcessOnce_ClaimAttemptTimeoutIsNotReportedAsEmpty(t *testing.T) {
	db := openConcurrentTestDB(t)
	server, calls, _ := newEnqueueServer(t)
	outbox := NewOutbox(db, newTestClient(t, server.URL), WithOutboxBatchSize(3))
	for i := range 3 {
		publishTestJob(t, db, fmt.Sprintf("timeout.row_%d", i), ojs.Args{"n": i})
	}

	resetClaimHook(t)
	var once sync.Once
	claimAfterSelectHook = func(_ *gorm.DB, _ uint) {
		once.Do(func() {
			time.Sleep(claimAttemptTimeout + 50*time.Millisecond)
		})
	}

	err := outbox.ProcessOnce(context.Background())
	if !errors.Is(err, errClaimAttemptTimedOut) {
		t.Fatalf("ProcessOnce() error = %v, want errClaimAttemptTimedOut", err)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("enqueue calls = %d, want 0", got)
	}

	var pending int64
	if err := db.Model(&outboxRecord{}).Where("status = ?", outboxStatusPending).Count(&pending).Error; err != nil {
		t.Fatalf("count pending rows error = %v", err)
	}
	if pending != 3 {
		t.Fatalf("pending rows = %d, want 3; a timed-out scan must not masquerade as an empty queue", pending)
	}
}

func TestOutbox_ProcessOnce_ContentionWorkIsBoundedByBatchSize(t *testing.T) {
	db := openTestDB(t)
	server, calls, _ := newEnqueueServer(t)
	const (
		rowCount  = 50
		batchSize = 2
	)
	outbox := NewOutbox(
		db,
		newTestClient(t, server.URL),
		WithOutboxBatchSize(batchSize),
		WithOutboxClaimTTL(time.Hour),
	)
	for i := range rowCount {
		publishTestJob(t, db, fmt.Sprintf("bounded.row_%02d", i), ojs.Args{"n": i})
	}

	resetClaimHook(t)
	var contentionEvents atomic.Int64
	claimAfterSelectHook = func(tx *gorm.DB, candidateID uint) {
		contentionEvents.Add(1)
		if err := tx.Model(&outboxRecord{}).
			Where("id = ?", candidateID).
			Updates(map[string]any{
				"status":      outboxStatusProcessing,
				"claim_token": "bounded-competitor-token",
				"claimed_at":  time.Now().UTC(),
			}).Error; err != nil {
			t.Fatalf("simulate bounded contention error = %v", err)
		}
	}

	start := time.Now()
	if err := outbox.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce() error = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("ProcessOnce() took %s with %d contended rows and batch size %d; contention work is not bounded",
			elapsed, rowCount, batchSize)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("enqueue calls = %d, want 0", got)
	}
	// Each budgeted candidate is retried, then one additional scan may
	// observe contention and stop without retrying it.
	if got := contentionEvents.Load(); got > batchSize+1 {
		t.Fatalf("contended candidates observed = %d, want at most %d for batch size %d",
			got, batchSize+1, batchSize)
	}
}

// TestOutbox_ProcessOnce_ConcurrentPublishersProcessAllRowsInOneRound uses
// real, independent Outbox instances racing over genuine goroutines and
// SQLite connections (no test hook) to prove that many concurrently polling
// publishers claim and publish every available row without double-publishing
// and without any of their single ProcessOnce calls giving up early due to
// contention.
func TestOutbox_ProcessOnce_ConcurrentPublishersProcessAllRowsInOneRound(t *testing.T) {
	// A real (temp-file, WAL-mode) SQLite database is used instead of the
	// shared-cache in-memory database used elsewhere in this file: SQLite's
	// shared-cache mode enforces its own table-level locking between
	// connections in the same process and returns a non-retryable "table is
	// locked" error under genuine multi-connection concurrency, which
	// busy_timeout does not cover. WAL mode against a real file supports
	// true concurrent connections the way a production SQLite deployment
	// would.
	db := openConcurrentTestDB(t)

	const rowCount = 24
	const publisherCount = 6

	var mu sync.Mutex
	callsByType := map[string]int{}
	server, _, _ := newEnqueueServerCounting(t, &mu, callsByType)

	for i := range rowCount {
		publishTestJob(t, db, fmt.Sprintf("concurrent.row%02d", i), ojs.Args{"n": i})
	}

	publishers := make([]*Outbox, publisherCount)
	for i := range publishers {
		publishers[i] = NewOutbox(
			db,
			newTestClient(t, server.URL),
			WithOutboxBatchSize(rowCount),
			WithOutboxClaimTTL(2*time.Second),
		)
	}

	var wg sync.WaitGroup
	errs := make(chan error, publisherCount)
	for _, publisher := range publishers {
		wg.Add(1)
		go func(o *Outbox) {
			defer wg.Done()
			errs <- o.ProcessOnce(context.Background())
		}(publisher)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent ProcessOnce calls did not complete; suspected spin/livelock")
	}
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("ProcessOnce() error = %v", err)
		}
	}

	mu.Lock()
	totalCalls := 0
	for jobType, n := range callsByType {
		if n != 1 {
			t.Errorf("job %q enqueued %d times, want exactly 1", jobType, n)
		}
		totalCalls += n
	}
	mu.Unlock()
	if totalCalls != rowCount {
		t.Fatalf("total enqueue calls = %d, want %d", totalCalls, rowCount)
	}

	var publishedCount int64
	if err := db.Model(&outboxRecord{}).Where("status = ?", outboxStatusPublished).Count(&publishedCount).Error; err != nil {
		t.Fatalf("count published rows error = %v", err)
	}
	if publishedCount != rowCount {
		t.Fatalf("published rows = %d, want %d (all rows processed across this single round of concurrent calls)", publishedCount, rowCount)
	}
}
