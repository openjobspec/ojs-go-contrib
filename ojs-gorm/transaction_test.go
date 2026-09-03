package ojsgorm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	ojs "github.com/openjobspec/ojs-go-sdk"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type transactionTestRecord struct {
	ID   uint `gorm:"primaryKey"`
	Name string
}

func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared&_busy_timeout=5000"), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	if err := db.AutoMigrate(&transactionTestRecord{}, &outboxRecord{}); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}

	// SQLite's shared-cache in-memory mode enforces same-process
	// table-level locking between distinct connections that is stricter
	// than ordinary busy-wait contention: two genuinely concurrent
	// connections (e.g. two Outbox instances sharing this *gorm.DB, one
	// renewing a claim while another polls) can occasionally observe a
	// non-retryable "database table is locked" (SQLITE_LOCKED) error that
	// _busy_timeout does not cover, since it only governs SQLITE_BUSY.
	// Many tests in this package deliberately exercise real concurrent
	// goroutines against a single shared openTestDB instance, so pin the
	// pool to exactly one physical connection: all access then serializes
	// through it, which is safe (every write already goes through a
	// transaction) and eliminates this class of flake without weakening
	// any test's actual concurrency assertions, since those assert on
	// claim/publish outcomes, not on genuine parallel database I/O.
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error = %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	return db
}

// openConcurrentTestDB opens a temp-file-backed SQLite database in WAL mode
// with a busy timeout, suitable for tests that exercise genuine multi-
// connection concurrency. SQLite's shared in-memory cache mode (used by
// openTestDB) enforces stricter same-process table-level locking that
// surfaces as a non-retryable "table is locked" error under real concurrent
// load; WAL mode against a real file allows concurrent readers and a
// blocking (busy_timeout-honoring) writer, matching production behavior.
func openConcurrentTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	// _txlock=immediate makes every transaction acquire the write lock with
	// BEGIN IMMEDIATE instead of a deferred BEGIN, so concurrent writers
	// serialize on busy_timeout instead of deadlocking when multiple
	// connections try to upgrade a shared read lock to a write lock at the
	// same time (a classic SQLite reader-to-writer upgrade conflict that
	// returns SQLITE_BUSY immediately, ignoring busy_timeout).
	dsn := "file:" + filepath.Join(t.TempDir(), "outbox.db") + "?_journal_mode=WAL&_busy_timeout=10000&_txlock=immediate"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	if err := db.AutoMigrate(&transactionTestRecord{}, &outboxRecord{}); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	return db
}

func newEnqueueServer(t *testing.T) (*httptest.Server, *atomic.Int64, *atomic.Bool) {
	t.Helper()

	var calls atomic.Int64
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if fail.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":"backend_error","message":"unavailable","retryable":true}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"job":{"id":"job-1","type":"test.job","queue":"default","args":[]}}`))
	}))
	t.Cleanup(server.Close)
	return server, &calls, &fail
}

// newEnqueueServerCounting behaves like newEnqueueServer but tracks how many
// times each job type was enqueued, keyed by the request body's "type"
// field, guarded by callerMu since concurrent publishers hit it in parallel.
func newEnqueueServerCounting(t *testing.T, callerMu *sync.Mutex, callsByType map[string]int) (*httptest.Server, *atomic.Int64, *atomic.Bool) {
	t.Helper()

	var calls atomic.Int64
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Type string `json:"type"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode enqueue request error = %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		calls.Add(1)
		callerMu.Lock()
		callsByType[request.Type]++
		callerMu.Unlock()
		if fail.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":"backend_error","message":"unavailable","retryable":true}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"job":{"id":"job-1","type":"` + request.Type + `","queue":"default","args":[]}}`))
	}))
	t.Cleanup(server.Close)
	return server, &calls, &fail
}

func newTestClient(t *testing.T, url string) *ojs.Client {
	t.Helper()
	client, err := ojs.NewClient(url)
	if err != nil {
		t.Fatalf("ojs.NewClient() error = %v", err)
	}
	return client
}

func TestEnqueueAfterCommit_RunsOnlyAfterSuccessfulCommit(t *testing.T) {
	db := openTestDB(t)
	server, calls, _ := newEnqueueServer(t)
	if err := Register(db, newTestClient(t, server.URL)); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&transactionTestRecord{Name: "committed"}).Error; err != nil {
			return err
		}
		EnqueueAfterCommit(tx, "test.job", ojs.Args{"name": "committed"})
		if got := calls.Load(); got != 0 {
			t.Fatalf("enqueue calls before commit = %d, want 0", got)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("committing transaction error = %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("enqueue calls after commit = %d, want 1", got)
	}

	errRollback := errors.New("rollback")
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&transactionTestRecord{Name: "rolled-back"}).Error; err != nil {
			return err
		}
		EnqueueAfterCommit(tx, "test.job", ojs.Args{"name": "rolled-back"})
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("rollback error = %v, want %v", err, errRollback)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("enqueue calls after rollback = %d, want 1", got)
	}
}

func TestEnqueueAfterCommit_PropagatesPostCommitFailure(t *testing.T) {
	db := openTestDB(t)
	server, calls, fail := newEnqueueServer(t)
	fail.Store(true)
	if err := Register(db, newTestClient(t, server.URL)); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&transactionTestRecord{Name: "committed"}).Error; err != nil {
			return err
		}
		EnqueueAfterCommit(tx, "test.job", ojs.Args{"name": "committed"})
		return nil
	})
	if err == nil {
		t.Fatal("transaction error = nil, want post-commit enqueue error")
	}
	if got := calls.Load(); got == 0 {
		t.Fatal("enqueue was not attempted after commit")
	}

	var count int64
	if err := db.Model(&transactionTestRecord{}).Where("name = ?", "committed").Count(&count).Error; err != nil {
		t.Fatalf("count committed record error = %v", err)
	}
	if count != 1 {
		t.Fatalf("committed records = %d, want 1 despite post-commit error", count)
	}
}

func TestEnqueueAfterCommitJSON_InvalidJSONPreventsCommit(t *testing.T) {
	db := openTestDB(t)
	server, calls, _ := newEnqueueServer(t)
	if err := Register(db, newTestClient(t, server.URL)); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&transactionTestRecord{Name: "invalid-json"}).Error; err != nil {
			return err
		}
		EnqueueAfterCommitJSON(tx, "test.job", json.RawMessage(`{`))
		return nil
	})
	if err == nil {
		t.Fatal("transaction error = nil, want invalid JSON error")
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("enqueue calls = %d, want 0", got)
	}

	var count int64
	if err := db.Model(&transactionTestRecord{}).Where("name = ?", "invalid-json").Count(&count).Error; err != nil {
		t.Fatalf("count invalid record error = %v", err)
	}
	if count != 0 {
		t.Fatalf("committed records = %d, want 0", count)
	}
}

func TestCloneArgsPreservesNumericJSONAndDeepCopies(t *testing.T) {
	args := ojs.Args{
		"int64":  int64(9007199254740993),
		"uint64": uint64(math.MaxUint64),
		"number": json.Number("12345678901234567890.123456789"),
		"nested": map[string]any{
			"slice": []any{int64(9007199254740995), "original"},
		},
	}

	cloned, err := cloneArgs(args)
	if err != nil {
		t.Fatalf("cloneArgs() error = %v", err)
	}

	args["int64"] = int64(1)
	args["nested"].(map[string]any)["slice"].([]any)[1] = "mutated"

	raw, err := json.Marshal(cloned)
	if err != nil {
		t.Fatalf("json.Marshal(cloned) error = %v", err)
	}
	const want = `{"int64":9007199254740993,"nested":{"slice":[9007199254740995,"original"]},"number":12345678901234567890.123456789,"uint64":18446744073709551615}`
	if string(raw) != want {
		t.Fatalf("cloned JSON = %s, want %s", raw, want)
	}
}

func TestEnqueueAfterCommitPreservesExactNumericJSON(t *testing.T) {
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
		_, _ = w.Write([]byte(`{"job":{"id":"job-1","type":"precision.job","queue":"default","args":[]}}`))
	}))
	t.Cleanup(server.Close)

	if err := Register(db, newTestClient(t, server.URL)); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	args := ojs.Args{
		"int64":  int64(9007199254740993),
		"uint64": uint64(math.MaxUint64),
		"number": json.Number("12345678901234567890.123456789"),
		"nested": []any{int64(9007199254740995)},
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := EnqueueAfterCommitErr(tx, "precision.job", args); err != nil {
			return err
		}
		args["int64"] = int64(1)
		args["nested"].([]any)[0] = int64(2)
		return nil
	}); err != nil {
		t.Fatalf("transaction error = %v", err)
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
	const want = `{"int64":9007199254740993,"nested":[9007199254740995],"number":12345678901234567890.123456789,"uint64":18446744073709551615}`
	if string(request.Args[0]) != want {
		t.Fatalf("enqueued args JSON = %s, want %s", request.Args[0], want)
	}
}

// TestEnqueueAfterCommit_MisuseLeavesDBErrorNil proves that misuse of
// EnqueueAfterCommit / EnqueueAfterCommitJSON (nil handle, unregistered
// plugin, or a non-transaction handle) never mutates db.Error on the
// caller's shared/base *gorm.DB handle. GORM clones propagate db.Error into
// every subsequent chained call (see (*gorm.DB).getInstance), so writing to
// it here would silently poison all future queries issued through that
// handle. The strict Err-returning variants still report the problem.
func TestEnqueueAfterCommit_MisuseLeavesDBErrorNil(t *testing.T) {
	db := openTestDB(t)

	// Nil handle: nothing to mutate, but must not panic.
	EnqueueAfterCommit(nil, "test.job", ojs.Args{"name": "nil-handle"})

	// Unregistered plugin, called directly on the shared/base handle.
	EnqueueAfterCommit(db, "test.job", ojs.Args{"name": "unregistered"})
	if db.Error != nil {
		t.Fatalf("db.Error = %v, want nil after unregistered-plugin misuse", db.Error)
	}
	if err := EnqueueAfterCommitErr(db, "test.job", ojs.Args{"name": "unregistered"}); err == nil {
		t.Fatal("EnqueueAfterCommitErr() error = nil, want unregistered plugin error")
	}
	if db.Error != nil {
		t.Fatalf("db.Error = %v, want nil after EnqueueAfterCommitErr misuse", db.Error)
	}

	// Registered plugin, but called outside of any active transaction
	// (directly on the shared/base handle returned by Register).
	registered := openTestDB(t)
	server, _, _ := newEnqueueServer(t)
	if err := Register(registered, newTestClient(t, server.URL)); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	EnqueueAfterCommit(registered, "test.job", ojs.Args{"name": "outside"})
	if registered.Error != nil {
		t.Fatalf("registered.Error = %v, want nil after non-transaction misuse", registered.Error)
	}
	if err := EnqueueAfterCommitErr(registered, "test.job", ojs.Args{"name": "outside"}); err == nil {
		t.Fatal("EnqueueAfterCommitErr() error = nil, want non-transaction error")
	}
	if registered.Error != nil {
		t.Fatalf("registered.Error = %v, want nil after EnqueueAfterCommitErr misuse", registered.Error)
	}

	// Same misuse via the JSON variants.
	EnqueueAfterCommitJSON(registered, "test.job", json.RawMessage(`{"name":"outside"}`))
	if registered.Error != nil {
		t.Fatalf("registered.Error = %v, want nil after JSON non-transaction misuse", registered.Error)
	}
	if err := EnqueueAfterCommitJSONErr(registered, "test.job", json.RawMessage(`{"name":"outside"}`)); err == nil {
		t.Fatal("EnqueueAfterCommitJSONErr() error = nil, want non-transaction error")
	}
	if registered.Error != nil {
		t.Fatalf("registered.Error = %v, want nil after EnqueueAfterCommitJSONErr misuse", registered.Error)
	}

	// The shared handle must remain fully usable for unrelated queries after
	// all of the misuse above (i.e., it was never poisoned).
	if err := registered.Create(&transactionTestRecord{Name: "after-misuse"}).Error; err != nil {
		t.Fatalf("Create() after misuse error = %v", err)
	}
	var count int64
	if err := registered.Model(&transactionTestRecord{}).Where("name = ?", "after-misuse").Count(&count).Error; err != nil {
		t.Fatalf("count after misuse error = %v", err)
	}
	if count != 1 {
		t.Fatalf("records after misuse = %d, want 1", count)
	}

	// A genuine subsequent transaction on the same handle must still work
	// end-to-end (commit, enqueue) after the earlier misuse.
	if err := registered.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&transactionTestRecord{Name: "post-misuse-tx"}).Error; err != nil {
			return err
		}
		EnqueueAfterCommit(tx, "test.job", ojs.Args{"name": "post-misuse-tx"})
		return nil
	}); err != nil {
		t.Fatalf("transaction after misuse error = %v", err)
	}
}

// TestEnqueueAfterCommit_ValidTransactionValidationErrorRollsBack proves
// that once EnqueueAfterCommit confirms a valid, plugin-registered active
// transaction, a delivery/outbox validation error (e.g., an empty job type)
// still marks that transaction for rollback per the existing commit-hook
// contract, even though EnqueueAfterCommit itself never touches tx.Error
// directly.
func TestEnqueueAfterCommit_ValidTransactionValidationErrorRollsBack(t *testing.T) {
	db := openTestDB(t)
	server, calls, _ := newEnqueueServer(t)
	if err := Register(db, newTestClient(t, server.URL)); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&transactionTestRecord{Name: "invalid-job-type"}).Error; err != nil {
			return err
		}
		EnqueueAfterCommit(tx, "", ojs.Args{"name": "invalid-job-type"})
		return nil
	})
	if err == nil {
		t.Fatal("transaction error = nil, want rollback error for empty job type")
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("enqueue calls = %d, want 0", got)
	}

	var count int64
	if err := db.Model(&transactionTestRecord{}).Where("name = ?", "invalid-job-type").Count(&count).Error; err != nil {
		t.Fatalf("count error = %v", err)
	}
	if count != 0 {
		t.Fatalf("records = %d, want 0 (rolled back)", count)
	}

	// The Err variant must also return the same validation error explicitly.
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&transactionTestRecord{Name: "invalid-job-type-err"}).Error; err != nil {
			return err
		}
		if err := EnqueueAfterCommitErr(tx, "", ojs.Args{}); err == nil {
			t.Fatal("EnqueueAfterCommitErr() error = nil, want empty job type error")
		}
		return nil
	})
	if err == nil {
		t.Fatal("transaction error = nil, want rollback error for empty job type (Err variant)")
	}
}

func TestEnqueueAfterCommit_InvalidTypeOrQueueRollsBackBeforeScheduling(t *testing.T) {
	db := openTestDB(t)
	server, calls, _ := newEnqueueServer(t)
	if err := Register(db, newTestClient(t, server.URL)); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	tests := []struct {
		name    string
		jobType string
		opts    []ojs.EnqueueOption
	}{
		{name: "invalid type pattern", jobType: "email-send"},
		{name: "type too long", jobType: strings.Repeat("a", 256)},
		{name: "invalid queue pattern", jobType: "email.send", opts: []ojs.EnqueueOption{ojs.WithQueue("Email")}},
		{name: "queue too long", jobType: "email.send", opts: []ojs.EnqueueOption{ojs.WithQueue(strings.Repeat("q", 129))}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := db.Transaction(func(tx *gorm.DB) error {
				if err := tx.Create(&transactionTestRecord{Name: tt.name}).Error; err != nil {
					return err
				}
				if err := EnqueueAfterCommitErr(tx, tt.jobType, ojs.Args{}, tt.opts...); err == nil {
					t.Fatal("EnqueueAfterCommitErr() error = nil, want validation error")
				}
				return nil
			})
			if err == nil {
				t.Fatal("transaction error = nil, want validation rollback")
			}

			var count int64
			if err := db.Model(&transactionTestRecord{}).Where("name = ?", tt.name).Count(&count).Error; err != nil {
				t.Fatalf("count records error = %v", err)
			}
			if count != 0 {
				t.Fatalf("committed records = %d, want 0", count)
			}
		})
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("enqueue calls = %d, want 0 for invalid scheduled jobs", got)
	}
}

func TestEnqueueAfterCommit_AcceptsMaximumLengthTypeAndQueue(t *testing.T) {
	db := openTestDB(t)
	server, calls, _ := newEnqueueServer(t)
	if err := Register(db, newTestClient(t, server.URL)); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	if err := db.Transaction(func(tx *gorm.DB) error {
		return EnqueueAfterCommitErr(
			tx,
			strings.Repeat("a", 255),
			ojs.Args{},
			ojs.WithQueue(strings.Repeat("q", 128)),
		)
	}); err != nil {
		t.Fatalf("transaction error = %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("enqueue calls = %d, want 1", got)
	}
}

func TestPublish_InvalidTypeOrQueueDoesNotInsertAndRollsBack(t *testing.T) {
	db := openTestDB(t)

	tests := []struct {
		name    string
		jobType string
		opts    []publishOption
	}{
		{name: "invalid type pattern", jobType: "email-send"},
		{name: "type too long", jobType: strings.Repeat("a", 256)},
		{name: "invalid queue pattern", jobType: "email.send", opts: []publishOption{WithPublishQueue("Email")}},
		{name: "queue too long", jobType: "email.send", opts: []publishOption{WithPublishQueue(strings.Repeat("q", 129))}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := db.Transaction(func(tx *gorm.DB) error {
				if err := tx.Create(&transactionTestRecord{Name: tt.name}).Error; err != nil {
					return err
				}
				err := Publish(tx, tt.jobType, ojs.Args{}, tt.opts...)
				if err == nil {
					t.Fatal("Publish() error = nil, want validation error")
				}

				var count int64
				if countErr := tx.Model(&outboxRecord{}).Count(&count).Error; countErr != nil {
					return countErr
				}
				if count != 0 {
					t.Fatalf("outbox rows before rollback = %d, want 0", count)
				}
				return err
			})
			if err == nil {
				t.Fatal("transaction error = nil, want validation rollback")
			}

			var records, entries int64
			if err := db.Model(&transactionTestRecord{}).Where("name = ?", tt.name).Count(&records).Error; err != nil {
				t.Fatalf("count records error = %v", err)
			}
			if err := db.Model(&outboxRecord{}).Count(&entries).Error; err != nil {
				t.Fatalf("count outbox entries error = %v", err)
			}
			if records != 0 || entries != 0 {
				t.Fatalf("committed records = %d, outbox entries = %d; want 0 each", records, entries)
			}
		})
	}
}

func TestPublish_AcceptsMaximumLengthTypeAndQueue(t *testing.T) {
	db := openTestDB(t)
	jobType := strings.Repeat("a", 255)
	queue := strings.Repeat("q", 128)

	if err := db.Transaction(func(tx *gorm.DB) error {
		return Publish(tx, jobType, ojs.Args{}, WithPublishQueue(queue))
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	var record outboxRecord
	if err := db.First(&record).Error; err != nil {
		t.Fatalf("load outbox record error = %v", err)
	}
	if record.JobType != jobType || record.Queue != queue || record.Status != outboxStatusPending {
		t.Fatalf("outbox record = %+v, want maximum valid type/queue pending", record)
	}
}

func TestTransactionHookUsesTransactionContextValues(t *testing.T) {
	db := openTestDB(t)
	server, _, _ := newEnqueueServer(t)
	if err := Register(db, newTestClient(t, server.URL)); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "value")
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		EnqueueAfterCommit(tx, "test.job", ojs.Args{"name": "context"})
		return nil
	}); err != nil {
		t.Fatalf("transaction error = %v", err)
	}
}
