package worker

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"time"
)

type Runner struct {
	store        *Store
	db           *sql.DB
	jobs         []Job
	pollInterval time.Duration
	instanceID   string
	now          func() time.Time
}

func NewRunner(db *sql.DB, pollInterval time.Duration, jobs []Job) (*Runner, error) {
	store, err := NewStore(db)
	if err != nil {
		return nil, err
	}
	if pollInterval <= 0 {
		pollInterval = time.Minute
	}
	if len(jobs) == 0 {
		return nil, fmt.Errorf("no jobs configured")
	}
	for _, job := range jobs {
		if job.Name == "" {
			return nil, fmt.Errorf("job name is required")
		}
		if job.Interval <= 0 || job.LeaseTTL <= 0 || job.Timeout <= 0 || job.RetryDelay <= 0 {
			return nil, fmt.Errorf("job %s has invalid scheduling", job.Name)
		}
		if job.Run == nil {
			return nil, fmt.Errorf("job %s has nil runner", job.Name)
		}
	}
	return &Runner{
		store:        store,
		db:           db,
		jobs:         jobs,
		pollInterval: pollInterval,
		instanceID:   newInstanceID(),
		now:          time.Now,
	}, nil
}

func (r *Runner) Run(ctx context.Context) error {
	if err := r.runDueJobs(ctx); err != nil && !isContextError(err) {
		log.Printf("worker sync instance=%s: %v", r.instanceID, err)
	}

	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := r.runDueJobs(ctx); err != nil && !isContextError(err) {
				log.Printf("worker poll instance=%s: %v", r.instanceID, err)
			}
		}
	}
}

func (r *Runner) runDueJobs(ctx context.Context) error {
	now := r.now().UTC()
	if err := r.store.SyncDefinitions(ctx, r.jobs, now); err != nil {
		return err
	}

	for _, job := range r.jobs {
		claimed, err := r.store.TryClaim(ctx, job, now, r.instanceID)
		if err != nil {
			log.Printf("worker job=%s instance=%s claim failed: %v", job.Name, r.instanceID, err)
			continue
		}
		if !claimed {
			continue
		}
		r.runJob(ctx, job)
	}

	return nil
}

func (r *Runner) runJob(ctx context.Context, job Job) {
	startedAt := r.now().UTC()
	jobCtx, cancel := context.WithTimeout(ctx, job.Timeout)
	defer cancel()

	result, runErr := job.Run(jobCtx, r.db, startedAt)
	finishedAt := r.now().UTC()
	duration := finishedAt.Sub(startedAt)

	updateCtx, updateCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer updateCancel()

	if runErr != nil {
		if err := r.store.MarkFailure(updateCtx, job, finishedAt, duration, runErr.Error(), result, r.instanceID); err != nil {
			log.Printf("worker job=%s instance=%s failure update: %v", job.Name, r.instanceID, err)
		}
		log.Printf("worker job=%s instance=%s failed duration=%s: %v", job.Name, r.instanceID, duration.Round(time.Millisecond), runErr)
		return
	}

	if err := r.store.MarkSuccess(updateCtx, job, finishedAt, duration, result, r.instanceID); err != nil {
		log.Printf("worker job=%s instance=%s success update: %v", job.Name, r.instanceID, err)
		return
	}
	log.Printf("worker job=%s instance=%s completed duration=%s result=%q", job.Name, r.instanceID, duration.Round(time.Millisecond), result)
}

func newInstanceID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%s-%d", host, os.Getpid())
	}
	return fmt.Sprintf("%s-%d-%s", host, os.Getpid(), hex.EncodeToString(buf))
}

func isContextError(err error) bool {
	return err == context.Canceled || err == context.DeadlineExceeded
}
