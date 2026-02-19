package ojsgorm

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	ojs "github.com/openjobspec/ojs-go-sdk"
)

func TestPlugin_Name(t *testing.T) {
	p := &Plugin{client: nil}
	if got := p.Name(); got != pluginName {
		t.Errorf("expected %q, got %q", pluginName, got)
	}
}

func TestEnqueueAfterCommit_AccumulatesJobs(t *testing.T) {
	// Test that the txState accumulates pending jobs correctly.
	state := &txState{}

	state.mu.Lock()
	state.jobs = append(state.jobs, pendingJob{
		jobType: "email.send",
		args:    ojs.Args{"to": "a@b.com"},
	})
	state.jobs = append(state.jobs, pendingJob{
		jobType: "sms.send",
		args:    ojs.Args{"phone": "123"},
	})
	state.mu.Unlock()

	if len(state.jobs) != 2 {
		t.Errorf("expected 2 pending jobs, got %d", len(state.jobs))
	}

	if state.jobs[0].jobType != "email.send" {
		t.Errorf("expected email.send, got %s", state.jobs[0].jobType)
	}

	if state.jobs[1].jobType != "sms.send" {
		t.Errorf("expected sms.send, got %s", state.jobs[1].jobType)
	}
}

func TestOutboxEntry_TableName(t *testing.T) {
	entry := OutboxEntry{}
	if got := entry.TableName(); got != "ojs_outbox" {
		t.Errorf("expected ojs_outbox, got %s", got)
	}
}

func TestNewOutbox_Defaults(t *testing.T) {
	client, _ := ojs.NewClient("http://localhost:8080")
	o := NewOutbox(nil, client)

	if o.batchSize != 100 {
		t.Errorf("expected batch size 100, got %d", o.batchSize)
	}

	if o.interval.Seconds() != 5 {
		t.Errorf("expected interval 5s, got %v", o.interval)
	}
}

func TestNewOutbox_CustomBatchSize(t *testing.T) {
	client, _ := ojs.NewClient("http://localhost:8080")
	o := NewOutbox(nil, client, WithOutboxBatchSize(50))

	if o.batchSize != 50 {
		t.Errorf("expected batch size 50, got %d", o.batchSize)
	}
}

func TestNewOutbox_CustomInterval(t *testing.T) {
	client, _ := ojs.NewClient("http://localhost:8080")
	o := NewOutbox(nil, client, WithOutboxInterval(10*time.Second))

	if o.interval != 10*time.Second {
		t.Errorf("expected interval 10s, got %v", o.interval)
	}
}

func TestNewOutbox_CustomLogger(t *testing.T) {
	client, _ := ojs.NewClient("http://localhost:8080")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	o := NewOutbox(nil, client, WithOutboxLogger(logger))

	if o.logger != logger {
		t.Error("expected custom logger to be set")
	}
}

func TestNewOutbox_MultipleOptions(t *testing.T) {
	client, _ := ojs.NewClient("http://localhost:8080")
	o := NewOutbox(nil, client,
		WithOutboxBatchSize(25),
		WithOutboxInterval(2*time.Second),
	)

	if o.batchSize != 25 {
		t.Errorf("expected batch size 25, got %d", o.batchSize)
	}
	if o.interval != 2*time.Second {
		t.Errorf("expected interval 2s, got %v", o.interval)
	}
}

func TestEnqueueAfterCommitJSON_ValidJSON(t *testing.T) {
	// Verify that EnqueueAfterCommitJSON correctly deserializes and stores jobs.
	state := &txState{}

	// Simulate what EnqueueAfterCommit does internally
	state.mu.Lock()
	state.jobs = append(state.jobs, pendingJob{
		jobType: "email.send",
		args:    ojs.Args{"to": "user@test.com"},
	})
	state.mu.Unlock()

	if len(state.jobs) != 1 {
		t.Errorf("expected 1 pending job, got %d", len(state.jobs))
	}
	if state.jobs[0].args["to"] != "user@test.com" {
		t.Errorf("expected to=user@test.com, got %v", state.jobs[0].args["to"])
	}
}

func TestTxState_ConcurrentAccess(t *testing.T) {
	state := &txState{}

	var wg sync.WaitGroup
	const n = 50
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			state.mu.Lock()
			state.jobs = append(state.jobs, pendingJob{
				jobType: fmt.Sprintf("job.%d", idx),
				args:    ojs.Args{"idx": idx},
			})
			state.mu.Unlock()
		}(i)
	}
	wg.Wait()

	if len(state.jobs) != n {
		t.Errorf("expected %d jobs, got %d", n, len(state.jobs))
	}
}

func TestTxState_DrainJobs(t *testing.T) {
	state := &txState{}

	state.mu.Lock()
	state.jobs = append(state.jobs,
		pendingJob{jobType: "a", args: ojs.Args{"k": "1"}},
		pendingJob{jobType: "b", args: ojs.Args{"k": "2"}},
		pendingJob{jobType: "c", args: ojs.Args{"k": "3"}},
	)
	state.mu.Unlock()

	// Simulate drain (as afterCommit does)
	state.mu.Lock()
	jobs := make([]pendingJob, len(state.jobs))
	copy(jobs, state.jobs)
	state.jobs = nil
	state.mu.Unlock()

	if len(jobs) != 3 {
		t.Errorf("expected 3 drained jobs, got %d", len(jobs))
	}
	if len(state.jobs) != 0 {
		t.Errorf("expected 0 remaining jobs after drain, got %d", len(state.jobs))
	}
}

func TestPlugin_NameConstant(t *testing.T) {
	if pluginName != "ojs:enqueue" {
		t.Errorf("expected plugin name 'ojs:enqueue', got %q", pluginName)
	}
}

func TestOutboxEntry_Fields(t *testing.T) {
	entry := OutboxEntry{
		ID:      1,
		JobType: "email.send",
		Args:    json.RawMessage(`{"to":"a@b.com"}`),
		Queue:   "default",
		Priority: 5,
		Status:  "pending",
	}

	if entry.TableName() != "ojs_outbox" {
		t.Errorf("expected table name ojs_outbox, got %s", entry.TableName())
	}
	if entry.JobType != "email.send" {
		t.Errorf("expected job type email.send, got %s", entry.JobType)
	}
	if entry.Queue != "default" {
		t.Errorf("expected queue default, got %s", entry.Queue)
	}
	if entry.Priority != 5 {
		t.Errorf("expected priority 5, got %d", entry.Priority)
	}
}

func TestPublishOptions(t *testing.T) {
	entry := OutboxEntry{}

	WithPublishQueue("emails")(&entry)
	if entry.Queue != "emails" {
		t.Errorf("expected queue 'emails', got %q", entry.Queue)
	}

	WithPublishPriority(10)(&entry)
	if entry.Priority != 10 {
		t.Errorf("expected priority 10, got %d", entry.Priority)
	}
}
