package ojsgin

import (
	"context"
	"fmt"
	"strings"

	ojs "github.com/openjobspec/ojs-go-sdk"
)

// CronConfig describes a recurring job to register with the OJS server.
// It is a simplified view of [ojs.CronJobRequest] designed for declarative
// configuration (e.g. from a config file or struct literal).
type CronConfig struct {
	// Name is a unique identifier for this cron job.
	Name string
	// Schedule is a cron expression (e.g. "*/5 * * * *").
	Schedule string
	// Timezone is an IANA timezone string (e.g. "America/New_York").
	// Leave empty for UTC.
	Timezone string
	// JobType is the OJS job type that will be enqueued on each tick.
	JobType string
	// Args are passed to every enqueued job instance.
	Args ojs.Args
	// Options are enqueue options applied to every job instance.
	Options []ojs.EnqueueOption
}

// RegisterCrons registers a batch of cron jobs with the OJS server.
// It returns the first error encountered; already-registered crons before
// the failure are not rolled back (the server is idempotent on name).
//
//	crons := []ojsgin.CronConfig{
//	    {Name: "daily-digest", Schedule: "0 9 * * *", JobType: "email.digest"},
//	    {Name: "cleanup",      Schedule: "0 */6 * * *", JobType: "maintenance.cleanup"},
//	}
//	if err := ojsgin.RegisterCrons(ctx, client, crons); err != nil {
//	    log.Fatal(err)
//	}
func RegisterCrons(ctx context.Context, client *ojs.Client, crons []CronConfig) error {
	if ctx == nil {
		return fmt.Errorf("ojsgin: context must not be nil")
	}
	for i, c := range crons {
		if strings.TrimSpace(c.Name) == "" ||
			strings.TrimSpace(c.Schedule) == "" ||
			strings.TrimSpace(c.JobType) == "" {
			return fmt.Errorf("ojsgin: cron %d requires Name, Schedule, and JobType", i)
		}
		if client == nil {
			return fmt.Errorf("ojsgin: OJS client must not be nil")
		}
		req := ojs.CronJobRequest{
			Name:     c.Name,
			Cron:     c.Schedule,
			Timezone: c.Timezone,
			Type:     c.JobType,
			Args:     c.Args,
			Options:  c.Options,
		}
		if _, err := client.RegisterCronJob(ctx, req); err != nil {
			return fmt.Errorf("ojsgin: registering cron %q: %w", c.Name, err)
		}
	}
	return nil
}
