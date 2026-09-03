package ojsgorm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ojs "github.com/openjobspec/ojs-go-sdk"
	"gorm.io/gorm"
)

func publishTestJob(t *testing.T, db *gorm.DB, jobType string, args ojs.Args) {
	t.Helper()
	if err := db.Transaction(func(tx *gorm.DB) error {
		return Publish(tx, jobType, args)
	}); err != nil {
		t.Fatalf("Publish(%q) error = %v", jobType, err)
	}
}

func TestPublish_IsTransactional(t *testing.T) {
	db := openTestDB(t)

	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := Publish(tx, "rolled.back", ojs.Args{"id": 1}); err != nil {
			return err
		}
		return errors.New("rollback")
	}); err == nil {
		t.Fatal("transaction error = nil, want rollback error")
	}

	var count int64
	if err := db.Model(&outboxRecord{}).Count(&count).Error; err != nil {
		t.Fatalf("count after rollback error = %v", err)
	}
	if count != 0 {
		t.Fatalf("outbox rows after rollback = %d, want 0", count)
	}

	if err := db.Transaction(func(tx *gorm.DB) error {
		return Publish(tx, "committed", ojs.Args{"id": 2})
	}); err != nil {
		t.Fatalf("committing publish error = %v", err)
	}
	if err := db.Model(&outboxRecord{}).Count(&count).Error; err != nil {
		t.Fatalf("count after commit error = %v", err)
	}
	if count != 1 {
		t.Fatalf("outbox rows after commit = %d, want 1", count)
	}
}

func TestPublish_MisuseLeavesDBErrorNilAndSubsequentTransactionWorks(t *testing.T) {
	db := openTestDB(t)

	if err := Publish(nil, "nil.handle", ojs.Args{}); err == nil {
		t.Fatal("Publish(nil) error = nil, want validation error")
	}
	if err := Publish(&gorm.DB{}, "invalid.handle", ojs.Args{}); err == nil {
		t.Fatal("Publish(invalid handle) error = nil, want validation error")
	}
	if err := Publish(db, "outside.transaction", ojs.Args{}); err == nil {
		t.Fatal("Publish(base handle) error = nil, want active transaction error")
	}
	if db.Error != nil {
		t.Fatalf("db.Error = %v, want nil after Publish misuse", db.Error)
	}

	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&transactionTestRecord{Name: "after-publish-misuse"}).Error; err != nil {
			return err
		}
		return Publish(tx, "after.misuse", ojs.Args{"ok": true})
	}); err != nil {
		t.Fatalf("transaction after Publish misuse error = %v", err)
	}

	var records, entries int64
	if err := db.Model(&transactionTestRecord{}).Where("name = ?", "after-publish-misuse").Count(&records).Error; err != nil {
		t.Fatalf("count records error = %v", err)
	}
	if err := db.Model(&outboxRecord{}).Where("job_type = ?", "after.misuse").Count(&entries).Error; err != nil {
		t.Fatalf("count outbox entries error = %v", err)
	}
	if records != 1 || entries != 1 {
		t.Fatalf("committed records = %d, outbox entries = %d; want 1 each", records, entries)
	}
}

func TestPublish_ValidTransactionValidationErrorRollsBack(t *testing.T) {
	db := openTestDB(t)

	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&transactionTestRecord{Name: "invalid-publish"}).Error; err != nil {
			return err
		}
		return Publish(tx, "   ", ojs.Args{})
	})
	if err == nil {
		t.Fatal("transaction error = nil, want Publish validation error")
	}

	var count int64
	if err := db.Model(&transactionTestRecord{}).Where("name = ?", "invalid-publish").Count(&count).Error; err != nil {
		t.Fatalf("count records error = %v", err)
	}
	if count != 0 {
		t.Fatalf("committed records = %d, want 0", count)
	}
}

func TestOutbox_ProcessOncePublishesInIDOrder(t *testing.T) {
	db := openTestDB(t)
	server, calls, _ := newEnqueueServer(t)
	outbox := NewOutbox(db, newTestClient(t, server.URL), WithOutboxBatchSize(10))

	publishTestJob(t, db, "first.job", ojs.Args{"order": 1})
	publishTestJob(t, db, "second.job", ojs.Args{"order": 2})

	if err := outbox.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce() error = %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("enqueue calls = %d, want 2", got)
	}

	var records []outboxRecord
	if err := db.Order("id ASC").Find(&records).Error; err != nil {
		t.Fatalf("load records error = %v", err)
	}
	for _, record := range records {
		if record.Status != outboxStatusPublished || record.PublishedAt == nil {
			t.Fatalf("record %d status = %q, published_at = %v", record.ID, record.Status, record.PublishedAt)
		}
	}
}

func TestOutbox_ProcessOnceDoesNotDoublePublishConcurrently(t *testing.T) {
	db := openTestDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error = %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	server, calls, _ := newEnqueueServer(t)
	outbox := NewOutbox(db, newTestClient(t, server.URL), WithOutboxBatchSize(10))
	publishTestJob(t, db, "once.job", ojs.Args{"id": 1})

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- outbox.ProcessOnce(context.Background())
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("ProcessOnce() error = %v", err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("enqueue calls = %d, want 1", got)
	}
}

func TestOutbox_ClaimsEachEntryImmediatelyBeforeProcessing(t *testing.T) {
	db := openTestDB(t)
	var mu sync.Mutex
	calls := map[string]int{}
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var startOnce sync.Once
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { close(releaseFirst) })
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Type string `json:"type"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode enqueue request error = %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		mu.Lock()
		calls[request.Type]++
		mu.Unlock()

		if request.Type == "first.slow" {
			startOnce.Do(func() { close(firstStarted) })
			select {
			case <-releaseFirst:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"job":{"id":"job-1","type":"test.job","queue":"default","args":[]}}`))
	}))
	t.Cleanup(server.Close)
	t.Cleanup(release)

	publishTestJob(t, db, "first.slow", ojs.Args{"order": 1})
	publishTestJob(t, db, "second.fast", ojs.Args{"order": 2})

	first := NewOutbox(db, newTestClient(t, server.URL), WithOutboxBatchSize(2), WithOutboxClaimTTL(time.Second))
	second := NewOutbox(db, newTestClient(t, server.URL), WithOutboxBatchSize(2), WithOutboxClaimTTL(time.Second))
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- first.ProcessOnce(context.Background())
	}()

	select {
	case <-firstStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("first publisher did not start the slow enqueue")
	}

	if err := second.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("second ProcessOnce() error = %v", err)
	}

	mu.Lock()
	secondCalls := calls["second.fast"]
	mu.Unlock()
	if secondCalls != 1 {
		t.Fatalf("second.fast enqueue calls while first.slow is active = %d, want 1", secondCalls)
	}

	release()
	if err := <-firstDone; err != nil {
		t.Fatalf("first ProcessOnce() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if calls["first.slow"] != 1 || calls["second.fast"] != 1 {
		t.Fatalf("enqueue calls = %#v, want one call per entry", calls)
	}
}

func TestOutbox_RenewsSlowEnqueueClaimAndPublishesExactlyOnce(t *testing.T) {
	db := openTestDB(t)
	var calls atomic.Int64
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	var releaseOnce sync.Once
	releaseEnqueue := func() {
		releaseOnce.Do(func() { close(release) })
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		startOnce.Do(func() { close(started) })
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"job":{"id":"job-1","type":"slow.job","queue":"default","args":[]}}`))
	}))
	t.Cleanup(server.Close)
	t.Cleanup(releaseEnqueue)

	publishTestJob(t, db, "slow.job", ojs.Args{"id": 1})
	const claimTTL = 180 * time.Millisecond
	first := NewOutbox(db, newTestClient(t, server.URL), WithOutboxBatchSize(1), WithOutboxClaimTTL(claimTTL))
	second := NewOutbox(db, newTestClient(t, server.URL), WithOutboxBatchSize(1), WithOutboxClaimTTL(claimTTL))

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- first.ProcessOnce(context.Background())
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("slow enqueue did not start")
	}

	for range 4 {
		time.Sleep(claimTTL / 2)
		if err := second.ProcessOnce(context.Background()); err != nil {
			t.Fatalf("competing ProcessOnce() error = %v", err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("enqueue calls while first publisher is active = %d, want 1", got)
	}

	releaseEnqueue()
	if err := <-firstDone; err != nil {
		t.Fatalf("first ProcessOnce() error = %v", err)
	}
	if err := second.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("post-success ProcessOnce() error = %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("successful enqueue calls = %d, want exactly 1", got)
	}

	var record outboxRecord
	if err := db.First(&record).Error; err != nil {
		t.Fatalf("load published record error = %v", err)
	}
	if record.Status != outboxStatusPublished || record.ClaimToken != "" || record.ClaimedAt != nil {
		t.Fatalf("published record = %+v", record)
	}
}

func TestOutbox_RenewalFailureCancelsEnqueueAndPreservesTokenCheck(t *testing.T) {
	db := openTestDB(t)
	started := make(chan struct{})
	releaseHandler := make(chan struct{})
	var startOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		startOnce.Do(func() { close(started) })
		select {
		case <-r.Context().Done():
		case <-releaseHandler:
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(releaseHandler) })

	publishTestJob(t, db, "renewal.failure", ojs.Args{"id": 1})
	outbox := NewOutbox(
		db,
		newTestClient(t, server.URL),
		WithOutboxBatchSize(1),
		WithOutboxClaimTTL(180*time.Millisecond),
	)

	done := make(chan error, 1)
	go func() {
		done <- outbox.ProcessOnce(context.Background())
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("enqueue did not start")
	}

	if err := db.Model(&outboxRecord{}).
		Where("job_type = ?", "renewal.failure").
		Update("claim_token", "replacement-token").Error; err != nil {
		t.Fatalf("replace claim token error = %v", err)
	}

	var processErr error
	select {
	case processErr = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("ProcessOnce() did not stop after renewal failure")
	}
	if processErr == nil {
		t.Fatal("ProcessOnce() error = nil, want renewal failure")
	}
	if !strings.Contains(processErr.Error(), "renewing claim") ||
		!strings.Contains(processErr.Error(), "claim was lost before renewal") {
		t.Fatalf("ProcessOnce() error = %v, want renewal claim-loss error", processErr)
	}

	var record outboxRecord
	if err := db.First(&record).Error; err != nil {
		t.Fatalf("load claim-lost record error = %v", err)
	}
	if record.Status != outboxStatusProcessing || record.ClaimToken != "replacement-token" {
		t.Fatalf("claim-lost record = %+v, want replacement token left untouched", record)
	}
}

func TestOutbox_RecoversExpiredClaimWithoutRenewal(t *testing.T) {
	db := openTestDB(t)
	server, calls, _ := newEnqueueServer(t)
	const claimTTL = 120 * time.Millisecond
	outbox := NewOutbox(db, newTestClient(t, server.URL), WithOutboxBatchSize(1), WithOutboxClaimTTL(claimTTL))
	publishTestJob(t, db, "crashed.publisher", ojs.Args{"id": 1})

	claimedAt := time.Now().UTC()
	if err := db.Model(&outboxRecord{}).
		Where("job_type = ?", "crashed.publisher").
		Updates(map[string]any{
			"status":      outboxStatusProcessing,
			"claim_token": "crashed-token",
			"claimed_at":  claimedAt,
		}).Error; err != nil {
		t.Fatalf("simulate crashed claim error = %v", err)
	}

	if err := outbox.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce() before TTL error = %v", err)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("enqueue calls before claim expiry = %d, want 0", got)
	}

	time.Sleep(2 * claimTTL)
	if err := outbox.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce() after TTL error = %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("enqueue calls after claim expiry = %d, want 1", got)
	}
}

func TestOutbox_PreservesExactNumericJSON(t *testing.T) {
	db := openTestDB(t)
	requestBody := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("io.ReadAll(request body) error = %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		requestBody <- raw
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"job":{"id":"job-1","type":"precision.outbox","queue":"default","args":[]}}`))
	}))
	t.Cleanup(server.Close)

	publishTestJob(t, db, "precision.outbox", ojs.Args{
		"int64":  int64(9007199254740993),
		"uint64": uint64(math.MaxUint64),
		"number": json.Number("12345678901234567890.123456789"),
	})
	outbox := NewOutbox(db, newTestClient(t, server.URL))
	if err := outbox.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce() error = %v", err)
	}

	var request struct {
		Args []json.RawMessage `json:"args"`
	}
	raw := <-requestBody
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatalf("json.Unmarshal(request body) error = %v; body = %s", err, raw)
	}
	if len(request.Args) != 1 {
		t.Fatalf("request args count = %d, want 1; body = %s", len(request.Args), raw)
	}
	const want = `{"int64":9007199254740993,"number":12345678901234567890.123456789,"uint64":18446744073709551615}`
	if string(request.Args[0]) != want {
		t.Fatalf("enqueued args JSON = %s, want %s", request.Args[0], want)
	}
}

func TestOutbox_ProcessOnceRetriesEnqueueFailure(t *testing.T) {
	db := openTestDB(t)
	server, calls, fail := newEnqueueServer(t)
	fail.Store(true)
	outbox := NewOutbox(db, newTestClient(t, server.URL), WithOutboxBatchSize(10))
	publishTestJob(t, db, "retry.job", ojs.Args{"id": 1})

	if err := outbox.ProcessOnce(context.Background()); err == nil {
		t.Fatal("first ProcessOnce() error = nil, want enqueue error")
	}
	var record outboxRecord
	if err := db.First(&record).Error; err != nil {
		t.Fatalf("load failed record error = %v", err)
	}
	if record.Status != outboxStatusPending || record.Attempts != 1 || record.LastError == "" {
		t.Fatalf("failed record = %+v", record)
	}

	fail.Store(false)
	if err := outbox.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("retry ProcessOnce() error = %v", err)
	}
	if got := calls.Load(); got < 2 {
		t.Fatalf("enqueue calls = %d, want at least 2", got)
	}
}

func TestOutbox_ProcessOnceMarksMalformedArgsFailed(t *testing.T) {
	db := openTestDB(t)
	server, calls, _ := newEnqueueServer(t)
	outbox := NewOutbox(db, newTestClient(t, server.URL))

	record := outboxRecord{
		JobType: "bad.args",
		Args:    json.RawMessage(`{`),
		Status:  outboxStatusPending,
	}
	if err := db.Create(&record).Error; err != nil {
		t.Fatalf("create malformed record error = %v", err)
	}

	if err := outbox.ProcessOnce(context.Background()); err == nil {
		t.Fatal("ProcessOnce() error = nil, want malformed args error")
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("enqueue calls = %d, want 0", got)
	}
	if err := db.First(&record, record.ID).Error; err != nil {
		t.Fatalf("reload malformed record error = %v", err)
	}
	if record.Status != outboxStatusFailed || record.LastError == "" {
		t.Fatalf("malformed record = %+v", record)
	}
}

func TestOutbox_RunProcessesImmediatelyAndStops(t *testing.T) {
	db := openTestDB(t)
	server, calls, _ := newEnqueueServer(t)
	outbox := NewOutbox(db, newTestClient(t, server.URL), WithOutboxInterval(time.Hour))
	publishTestJob(t, db, "immediate.job", ojs.Args{"id": 1})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- outbox.Run(ctx)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("enqueue calls = %d, want 1 immediate call", got)
	}
}

func TestNewOutbox_NormalizesInvalidOptions(t *testing.T) {
	db := openTestDB(t)
	server, _, _ := newEnqueueServer(t)
	outbox := NewOutbox(
		db,
		newTestClient(t, server.URL),
		WithOutboxInterval(0),
		WithOutboxBatchSize(0),
		WithOutboxLogger(nil),
	)
	if outbox.interval <= 0 || outbox.batchSize <= 0 || outbox.logger == nil {
		t.Fatalf("invalid normalized outbox = %+v", outbox)
	}
}

func TestOutbox_ProcessOnceRejectsNilDependencies(t *testing.T) {
	if err := NewOutbox(nil, nil).ProcessOnce(context.Background()); err == nil {
		t.Fatal("ProcessOnce() error = nil, want dependency error")
	}
	if err := NewOutbox(&gorm.DB{}, nil).ProcessOnce(context.Background()); err == nil {
		t.Fatal("ProcessOnce() error = nil, want client error")
	}
}

func TestOutbox_RunRejectsPermanentConfigurationBeforePolling(t *testing.T) {
	db := openTestDB(t)
	server, calls, _ := newEnqueueServer(t)
	client := newTestClient(t, server.URL)

	originalTicker := newOutboxTicker
	var tickerCalls atomic.Int64
	newOutboxTicker = func(d time.Duration) *time.Ticker {
		tickerCalls.Add(1)
		return originalTicker(d)
	}
	t.Cleanup(func() { newOutboxTicker = originalTicker })

	tests := []struct {
		name   string
		outbox func(*slog.Logger) *Outbox
	}{
		{
			name: "nil receiver",
			outbox: func(*slog.Logger) *Outbox {
				return nil
			},
		},
		{
			name: "nil database",
			outbox: func(logger *slog.Logger) *Outbox {
				return NewOutbox(nil, client, WithOutboxLogger(logger))
			},
		},
		{
			name: "nil client",
			outbox: func(logger *slog.Logger) *Outbox {
				return NewOutbox(db, nil, WithOutboxLogger(logger))
			},
		},
		{
			name: "invalid interval",
			outbox: func(logger *slog.Logger) *Outbox {
				outbox := NewOutbox(db, client, WithOutboxLogger(logger))
				outbox.interval = 0
				return outbox
			},
		},
		{
			name: "invalid batch size",
			outbox: func(logger *slog.Logger) *Outbox {
				outbox := NewOutbox(db, client, WithOutboxLogger(logger))
				outbox.batchSize = 0
				return outbox
			},
		},
		{
			name: "invalid claim TTL",
			outbox: func(logger *slog.Logger) *Outbox {
				outbox := NewOutbox(db, client, WithOutboxLogger(logger))
				outbox.claimTTL = 0
				return outbox
			},
		},
		{
			name: "nil logger",
			outbox: func(*slog.Logger) *Outbox {
				outbox := NewOutbox(db, client)
				outbox.logger = nil
				return outbox
			},
		},
		{
			name: "nil process semaphore",
			outbox: func(logger *slog.Logger) *Outbox {
				outbox := NewOutbox(db, client, WithOutboxLogger(logger))
				outbox.process = nil
				return outbox
			},
		},
		{
			name: "unbuffered process semaphore",
			outbox: func(logger *slog.Logger) *Outbox {
				outbox := NewOutbox(db, client, WithOutboxLogger(logger))
				outbox.process = make(chan struct{})
				return outbox
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			outbox := tt.outbox(slog.New(slog.NewTextHandler(&logs, nil)))
			err := outbox.Run(context.Background())
			if err == nil || !isOutboxConfigError(err) {
				t.Fatalf("Run() error = %v, want permanent configuration error", err)
			}
			if logs.Len() != 0 {
				t.Fatalf("Run() logged permanent configuration error: %s", logs.String())
			}
			if outbox != nil && outbox.running {
				t.Fatal("Run() acquired run ownership for invalid configuration")
			}
		})
	}

	if got := tickerCalls.Load(); got != 0 {
		t.Fatalf("ticker starts = %d, want 0 for invalid configuration", got)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("enqueue calls = %d, want 0 for invalid configuration", got)
	}

	stacks := make([]byte, 1<<20)
	n := runtime.Stack(stacks, true)
	for _, function := range []string{"ojsgorm.(*Outbox).Run", "ojsgorm.(*Outbox).renewClaimUntilCanceled"} {
		if bytes.Contains(stacks[:n], []byte(function)) {
			t.Fatalf("invalid configuration left an outbox goroutine running:\n%s", stacks[:n])
		}
	}
}

func TestOutbox_ProcessOnceSharesConfigurationValidator(t *testing.T) {
	db := openTestDB(t)
	server, _, _ := newEnqueueServer(t)
	client := newTestClient(t, server.URL)

	tests := []struct {
		name   string
		outbox *Outbox
	}{
		{name: "nil receiver"},
		{name: "nil database", outbox: NewOutbox(nil, client)},
		{name: "nil client", outbox: NewOutbox(db, nil)},
		{
			name: "invalid configuration",
			outbox: func() *Outbox {
				outbox := NewOutbox(db, client)
				outbox.batchSize = 0
				return outbox
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.outbox.ProcessOnce(context.Background())
			if err == nil || !isOutboxConfigError(err) {
				t.Fatalf("ProcessOnce() error = %v, want shared configuration error", err)
			}
		})
	}
}

func TestOutbox_ProcessOnceMarksInvalidStoredJobsFailed(t *testing.T) {
	db := openTestDB(t)
	server, calls, _ := newEnqueueServer(t)
	outbox := NewOutbox(db, newTestClient(t, server.URL), WithOutboxBatchSize(10))

	records := []outboxRecord{
		{JobType: "Invalid.Type", Args: json.RawMessage(`{}`), Status: outboxStatusPending},
		{JobType: "valid.type", Queue: "Invalid-Queue", Args: json.RawMessage(`{}`), Status: outboxStatusPending},
	}
	if err := db.Create(&records).Error; err != nil {
		t.Fatalf("create poison records error = %v", err)
	}

	if err := outbox.ProcessOnce(context.Background()); err == nil {
		t.Fatal("ProcessOnce() error = nil, want terminal validation errors")
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("enqueue calls = %d, want 0 for poison rows", got)
	}

	var stored []outboxRecord
	if err := db.Order("id ASC").Find(&stored).Error; err != nil {
		t.Fatalf("load poison records error = %v", err)
	}
	for _, record := range stored {
		if record.Status != outboxStatusFailed || record.Attempts != 1 || record.LastError == "" {
			t.Errorf("poison record = %+v, want terminal failed status with diagnostic", record)
		}
	}
}

func TestOutbox_ProcessOnceAcceptsMaximumLengthJobAndQueue(t *testing.T) {
	db := openTestDB(t)
	server, calls, _ := newEnqueueServer(t)
	outbox := NewOutbox(db, newTestClient(t, server.URL))

	record := outboxRecord{
		JobType: strings.Repeat("a", 255),
		Queue:   strings.Repeat("q", 128),
		Args:    json.RawMessage(`{}`),
		Status:  outboxStatusPending,
	}
	if err := db.Create(&record).Error; err != nil {
		t.Fatalf("create edge record error = %v", err)
	}
	if err := outbox.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce() error = %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("enqueue calls = %d, want 1", got)
	}
	if err := db.First(&record, record.ID).Error; err != nil {
		t.Fatalf("reload edge record error = %v", err)
	}
	if record.Status != outboxStatusPublished {
		t.Fatalf("edge record status = %q, want %q", record.Status, outboxStatusPublished)
	}
}
