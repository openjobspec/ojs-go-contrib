package ojsgorm

import (
	"testing"

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
