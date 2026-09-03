// Package ojsgorm provides transactional job enqueue via GORM for Open Job Spec.
//
// It ensures jobs are only enqueued after the database transaction commits
// successfully, preventing ghost jobs from rolled-back transactions.
package ojsgorm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	ojs "github.com/openjobspec/ojs-go-sdk"
	"gorm.io/gorm"
)

const (
	pluginName        = "ojs:enqueue"
	postCommitTimeout = 30 * time.Second
)

type pendingJob struct {
	jobType string
	args    ojs.Args
	opts    []ojs.EnqueueOption
}

type txState struct {
	mu            sync.Mutex
	jobs          []pendingJob
	validationErr error
}

func (s *txState) add(job pendingJob) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs = append(s.jobs, job)
}

func (s *txState) fail(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.validationErr = errors.Join(s.validationErr, err)
}

func (s *txState) snapshot() ([]pendingJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	jobs := append([]pendingJob(nil), s.jobs...)
	s.jobs = nil
	return jobs, s.validationErr
}

func (s *txState) discard() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs = nil
}

// Plugin implements the gorm.Plugin interface for OJS integration.
type Plugin struct {
	client *ojs.Client
}

// Name returns the plugin name.
func (p *Plugin) Name() string {
	return pluginName
}

// Initialize associates the plugin with DB sessions. Commit interception is
// installed lazily by EnqueueAfterCommit on the active transaction handle.
func (p *Plugin) Initialize(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("ojsgorm: database must not be nil")
	}
	if p == nil || p.client == nil {
		return fmt.Errorf("ojsgorm: OJS client must not be nil")
	}
	return nil
}

// Register installs the OJS plugin on a GORM DB instance.
func Register(db *gorm.DB, client *ojs.Client) error {
	if db == nil {
		return fmt.Errorf("ojsgorm: database must not be nil")
	}
	if client == nil {
		return fmt.Errorf("ojsgorm: OJS client must not be nil")
	}

	if err := db.Use(&Plugin{client: client}); err != nil {
		return fmt.Errorf("ojsgorm: registering plugin: %w", err)
	}
	return nil
}

// PostCommitError reports that the database commit succeeded but one or more
// OJS enqueue operations failed afterward. Callers must not retry the database
// transaction blindly; use the outbox API when atomic retryable delivery is
// required.
type PostCommitError struct {
	Err error
}

func (e *PostCommitError) Error() string {
	return fmt.Sprintf("ojsgorm: database committed but post-commit enqueue failed: %v", e.Err)
}

func (e *PostCommitError) Unwrap() error {
	return e.Err
}

// EnqueueAfterCommit schedules a job to be enqueued after the current
// transaction commits. If the transaction rolls back, the job is discarded.
//
// The exact top-level *gorm.DB transaction handle passed to
// gorm.DB.Transaction must be supplied. Nested savepoints and durable retryable
// delivery across process crashes should use Publish and Outbox instead.
//
// EnqueueAfterCommit has no error return, so misuse (a nil, unregistered, or
// non-transaction handle) is reported to the plugin's logger (or
// slog.Default) instead of being surfaced to the caller. It never mutates tx
// in that case: tx may be a long-lived shared/base *gorm.DB handle rather
// than a disposable transaction-scoped clone, and calling tx.AddError on it
// would permanently poison every future query issued through that handle.
// Callers that want the error returned explicitly should use
// EnqueueAfterCommitErr.
func EnqueueAfterCommit(tx *gorm.DB, jobType string, args ojs.Args, opts ...ojs.EnqueueOption) {
	if err := EnqueueAfterCommitErr(tx, jobType, args, opts...); err != nil && isInvalidTransactionErr(err) {
		logInvalidUsage(tx, err)
	}
}

// EnqueueAfterCommitErr is the strict, error-returning variant of
// EnqueueAfterCommit. It behaves identically but returns the validation
// error instead of only logging it, for callers who want to handle invalid
// usage explicitly (for example, by returning it from a transaction
// callback). Like EnqueueAfterCommit, it never mutates tx directly; once a
// valid, plugin-registered active transaction is confirmed, a delivery/
// outbox validation error still marks that transaction for rollback through
// the existing commit-hook contract (via txState), not by mutating tx.Error.
func EnqueueAfterCommitErr(tx *gorm.DB, jobType string, args ojs.Args, opts ...ojs.EnqueueOption) error {
	state, _, err := prepareTransaction(tx)
	if err != nil {
		return err
	}
	if err := validateEnqueueInput(jobType, opts...); err != nil {
		err = fmt.Errorf("ojsgorm: validating enqueue job: %w", err)
		state.fail(err)
		return err
	}

	clonedArgs, err := cloneArgs(args)
	if err != nil {
		err = fmt.Errorf("ojsgorm: encoding args for %q: %w", jobType, err)
		state.fail(err)
		return err
	}
	state.add(pendingJob{
		jobType: jobType,
		args:    clonedArgs,
		opts:    append([]ojs.EnqueueOption(nil), opts...),
	})
	return nil
}

// EnqueueAfterCommitJSON is like EnqueueAfterCommit but accepts
// pre-serialized JSON args. Invalid JSON marks the transaction for rollback
// when tx is a valid, plugin-registered active transaction; otherwise, like
// EnqueueAfterCommit, the error is logged rather than returned or written to
// tx. Use EnqueueAfterCommitJSONErr for the error-returning variant.
func EnqueueAfterCommitJSON(tx *gorm.DB, jobType string, args json.RawMessage, opts ...ojs.EnqueueOption) {
	if err := EnqueueAfterCommitJSONErr(tx, jobType, args, opts...); err != nil && isInvalidTransactionErr(err) {
		logInvalidUsage(tx, err)
	}
}

// EnqueueAfterCommitJSONErr is the strict, error-returning variant of
// EnqueueAfterCommitJSON. See EnqueueAfterCommitErr for the mutation and
// rollback contract.
func EnqueueAfterCommitJSONErr(tx *gorm.DB, jobType string, args json.RawMessage, opts ...ojs.EnqueueOption) error {
	state, _, err := prepareTransaction(tx)
	if err != nil {
		return err
	}

	decoded, err := decodeArgs(args)
	if err != nil {
		err = fmt.Errorf("ojsgorm: decoding args for %q: %w", jobType, err)
		state.fail(err)
		return err
	}
	return EnqueueAfterCommitErr(tx, jobType, decoded, opts...)
}

// invalidTransactionErr marks validation errors produced before an active,
// plugin-registered transaction is confirmed (i.e., prepareTransaction
// failures). Only these are eligible for logging by the void-signature
// helpers: once a transaction is confirmed, a validation error is already
// durably recorded on txState and will surface naturally when the caller's
// transaction fails to commit, so logging it again would be redundant.
type invalidTransactionErr struct{ error }

func (e *invalidTransactionErr) Unwrap() error { return e.error }

func isInvalidTransactionErr(err error) bool {
	var marker *invalidTransactionErr
	return errors.As(err, &marker)
}

// logInvalidUsage reports EnqueueAfterCommit/EnqueueAfterCommitJSON misuse
// through GORM's existing logger, falling back to slog.Default() when no
// usable handle exists. It deliberately does not touch tx.
func logInvalidUsage(tx *gorm.DB, err error) {
	if tx != nil && tx.Logger != nil {
		ctx := context.Background()
		if tx.Statement != nil && tx.Statement.Context != nil {
			ctx = tx.Statement.Context
		}
		tx.Logger.Error(ctx, "ojsgorm: invalid EnqueueAfterCommit usage: %v", err)
		return
	}
	slog.Default().Error("ojsgorm: invalid EnqueueAfterCommit usage", "error", err)
}

// prepareTransaction validates tx and returns the shared txState used to
// accumulate pending jobs for the active transaction, installing the commit
// hook on first use. It never mutates tx.Error: on failure it returns a
// wrapped invalidTransactionErr so callers can distinguish "tx is not a
// confirmed active transaction" (never safe to write to tx) from validation
// errors raised after a transaction is confirmed (safe to record on txState,
// per the existing rollback contract).
func prepareTransaction(tx *gorm.DB) (*txState, *Plugin, error) {
	if err := validateActiveTransaction(tx, "EnqueueAfterCommit"); err != nil {
		return nil, nil, &invalidTransactionErr{err}
	}

	value, ok := tx.Plugins[pluginName]
	if !ok {
		return nil, nil, &invalidTransactionErr{fmt.Errorf("ojsgorm: plugin is not registered")}
	}
	plugin, ok := value.(*Plugin)
	if !ok || plugin == nil || plugin.client == nil {
		return nil, nil, &invalidTransactionErr{fmt.Errorf("ojsgorm: plugin configuration is invalid")}
	}

	if hook, ok := tx.Statement.ConnPool.(*commitHookPool); ok {
		return hook.state, plugin, nil
	}

	committer, ok := tx.Statement.ConnPool.(gorm.TxCommitter)
	if !ok {
		return nil, plugin, &invalidTransactionErr{fmt.Errorf("ojsgorm: EnqueueAfterCommit requires an active transaction")}
	}

	state := &txState{}
	hook := &commitHookPool{
		ConnPool:    tx.Statement.ConnPool,
		TxCommitter: committer,
		plugin:      plugin,
		state:       state,
		ctx:         tx.Statement.Context,
	}
	tx.Statement.ConnPool = hook
	return state, plugin, nil
}

func validateActiveTransaction(tx *gorm.DB, helper string) error {
	if tx == nil {
		return fmt.Errorf("ojsgorm: transaction must not be nil")
	}
	if tx.Error != nil {
		return fmt.Errorf("ojsgorm: %s received an invalid database handle: %w", helper, tx.Error)
	}
	if tx.Config == nil || tx.Statement == nil || tx.Statement.ConnPool == nil {
		return fmt.Errorf("ojsgorm: %s received an invalid database handle", helper)
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return fmt.Errorf("ojsgorm: %s requires an active transaction", helper)
	}
	return nil
}

type commitHookPool struct {
	gorm.ConnPool
	gorm.TxCommitter

	mu       sync.Mutex
	finished bool
	plugin   *Plugin
	state    *txState
	ctx      context.Context
}

func (p *commitHookPool) Commit() error {
	p.mu.Lock()
	if p.finished {
		p.mu.Unlock()
		return gorm.ErrInvalidTransaction
	}

	jobs, validationErr := p.state.snapshot()
	if validationErr != nil {
		p.finished = true
		rollbackErr := p.TxCommitter.Rollback()
		p.mu.Unlock()
		return errors.Join(validationErr, rollbackErr)
	}

	if err := p.TxCommitter.Commit(); err != nil {
		p.mu.Unlock()
		return err
	}
	p.finished = true
	p.mu.Unlock()

	if err := p.plugin.publish(p.ctx, jobs); err != nil {
		return &PostCommitError{Err: err}
	}
	return nil
}

func (p *commitHookPool) Rollback() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.finished {
		return gorm.ErrInvalidTransaction
	}
	p.finished = true
	p.state.discard()
	return p.TxCommitter.Rollback()
}

func (p *Plugin) publish(ctx context.Context, jobs []pendingJob) error {
	if len(jobs) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	publishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), postCommitTimeout)
	defer cancel()

	var errs []error
	for _, job := range jobs {
		if _, err := p.client.Enqueue(publishCtx, job.jobType, job.args, job.opts...); err != nil {
			errs = append(errs, fmt.Errorf("enqueueing %q: %w", job.jobType, err))
		}
	}
	return errors.Join(errs...)
}

func cloneArgs(args ojs.Args) (ojs.Args, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	return decodeArgs(raw)
}

func decodeArgs(raw []byte) (ojs.Args, error) {
	var cloned ojs.Args
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&cloned); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values")
		}
		return nil, err
	}
	return cloned, nil
}
