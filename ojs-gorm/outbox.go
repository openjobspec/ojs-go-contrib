package ojsgorm

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	ojs "github.com/openjobspec/ojs-go-sdk"	"gorm.io/gorm"
)

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

// Outbox polls an outbox table for pending jobs and publishes them to OJS.
type Outbox struct {
	db        *gorm.DB
	client    *ojs.Client
	interval  time.Duration
	batchSize int
	logger    *slog.Logger
	mu        sync.Mutex
}

// NewOutbox creates a new outbox publisher.
func NewOutbox(db *gorm.DB, client *ojs.Client, opts ...OutboxOption) *Outbox {
	o := &Outbox{
		db:        db,
		client:    client,
		interval:  5 * time.Second,
		batchSize: 100,
		logger:    slog.Default(),
	}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// AutoMigrate creates the outbox table if it doesn't exist.
func (o *Outbox) AutoMigrate() error {
	return o.db.AutoMigrate(&OutboxEntry{})
}

// Run starts the outbox publisher. It polls the outbox table at the configured
// interval and enqueues pending jobs. Blocks until the context is cancelled.
func (o *Outbox) Run(ctx context.Context) error {
	ticker := time.NewTicker(o.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := o.processBatch(ctx); err != nil {
				o.logger.Error("outbox batch processing failed", "error", err)
			}
		}
	}
}

func (o *Outbox) processBatch(ctx context.Context) error {
	var entries []OutboxEntry
	result := o.db.WithContext(ctx).
		Where("status = ?", "pending").
		Order("id ASC").
		Limit(o.batchSize).
		Find(&entries)
	if result.Error != nil {
		return result.Error
	}

	for _, entry := range entries {
		var args ojs.Args
		if err := json.Unmarshal(entry.Args, &args); err != nil {
			o.logger.Error("failed to unmarshal outbox args",
				"id", entry.ID,
				"error", err,
			)
			o.db.Model(&entry).Update("status", "failed")
			continue
		}

		var opts []ojs.EnqueueOption
		if entry.Queue != "" {
			opts = append(opts, ojs.WithQueue(entry.Queue))
		}
		if entry.Priority != 0 {
			opts = append(opts, ojs.WithPriority(entry.Priority))
		}

		if _, err := o.client.Enqueue(ctx, entry.JobType, args, opts...); err != nil {
			o.logger.Error("failed to enqueue outbox entry",
				"id", entry.ID,
				"job_type", entry.JobType,
				"error", err,
			)
			continue
		}

		o.db.Model(&entry).Update("status", "published")
	}

	return nil
}

// Publish adds an entry to the outbox table within the given transaction.
// The entry will be picked up and published by the outbox publisher.
func Publish(tx *gorm.DB, jobType string, args ojs.Args, opts ...publishOption) error {
	entry := OutboxEntry{
		JobType: jobType,
		Status:  "pending",
	}

	for _, opt := range opts {
		opt(&entry)
	}

	raw, err := json.Marshal(args)
	if err != nil {
		return err
	}
	entry.Args = raw

	return tx.Create(&entry).Error
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

