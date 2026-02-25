// Package ojsgorm provides transactional job enqueue via GORM for Open Job Spec.
//
// It ensures jobs are only enqueued after the database transaction commits
// successfully, preventing ghost jobs from rolled-back transactions.
package ojsgorm

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	ojs "github.com/openjobspec/ojs-go-sdk"
	"gorm.io/gorm"
)

const pluginName = "ojs:enqueue"

type pendingJob struct {
	jobType string
	args    ojs.Args
	opts    []ojs.EnqueueOption
}

type txState struct {
	mu   sync.Mutex
	jobs []pendingJob
}

// Plugin implements the gorm.Plugin interface for OJS integration.
type Plugin struct {
	client *ojs.Client
}

// Name returns the plugin name.
func (p *Plugin) Name() string {
	return pluginName
}

// Initialize registers after-commit callbacks with GORM.
func (p *Plugin) Initialize(db *gorm.DB) error {
	cb := db.Callback()
	if err := cb.Create().After("gorm:commit").Register(pluginName+":create", p.afterCommit); err != nil {
		return fmt.Errorf("ojsgorm: failed to register create callback: %w", err)
	}
	if err := cb.Update().After("gorm:commit").Register(pluginName+":update", p.afterCommit); err != nil {
		return fmt.Errorf("ojsgorm: failed to register update callback: %w", err)
	}
	if err := cb.Delete().After("gorm:commit").Register(pluginName+":delete", p.afterCommit); err != nil {
		return fmt.Errorf("ojsgorm: failed to register delete callback: %w", err)
	}
	return nil
}

func (p *Plugin) afterCommit(db *gorm.DB) {
	v, ok := db.Get(pluginName)
	if !ok {
		return
	}
	state, ok := v.(*txState)
	if !ok {
		return
	}

	state.mu.Lock()
	jobs := make([]pendingJob, len(state.jobs))
	copy(jobs, state.jobs)
	state.jobs = nil
	state.mu.Unlock()

	for _, j := range jobs {
		_, err := p.client.Enqueue(context.Background(), j.jobType, j.args, j.opts...)
		if err != nil {
			db.Logger.Error(context.Background(), "ojsgorm: failed to enqueue job %s: %v", j.jobType, err)
		}
	}
}

// Register installs the OJS plugin on a GORM DB instance.
func Register(db *gorm.DB, client *ojs.Client) error {
	return db.Use(&Plugin{client: client})
}

// EnqueueAfterCommit schedules a job to be enqueued after the current
// transaction commits. If the transaction rolls back, the job is discarded.
func EnqueueAfterCommit(tx *gorm.DB, jobType string, args ojs.Args, opts ...ojs.EnqueueOption) {
	v, ok := tx.Get(pluginName)
	var state *txState
	if ok {
		state, _ = v.(*txState)
	}
	if state == nil {
		state = &txState{}
		tx.Set(pluginName, state)
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	state.jobs = append(state.jobs, pendingJob{
		jobType: jobType,
		args:    args,
		opts:    opts,
	})
}

// EnqueueAfterCommitJSON is like EnqueueAfterCommit but accepts
// pre-serialized JSON args.
func EnqueueAfterCommitJSON(tx *gorm.DB, jobType string, args json.RawMessage, opts ...ojs.EnqueueOption) {
	var decoded ojs.Args
	if err := json.Unmarshal(args, &decoded); err != nil {
		tx.Logger.Error(context.Background(), "ojsgorm: failed to unmarshal args: %v", err)
		return
	}
	EnqueueAfterCommit(tx, jobType, decoded, opts...)
}

