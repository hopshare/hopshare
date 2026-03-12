package worker

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("nil database")
	}
	return &Store{db: db}, nil
}

func (s *Store) SyncDefinitions(ctx context.Context, jobs []Job, now time.Time) error {
	for _, job := range jobs {
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO background_jobs (
				name,
				enabled,
				interval_seconds,
				lease_seconds,
				next_run_at,
				state
			)
			VALUES ($1, TRUE, $2, $3, $4, 'idle')
			ON CONFLICT (name) DO UPDATE
			SET interval_seconds = EXCLUDED.interval_seconds,
				lease_seconds = EXCLUDED.lease_seconds
		`, job.Name, durationSeconds(job.Interval), durationSeconds(job.LeaseTTL), now.UTC()); err != nil {
			return fmt.Errorf("sync job definition %s: %w", job.Name, err)
		}
	}
	return nil
}

func (s *Store) TryClaim(ctx context.Context, job Job, now time.Time, instanceID string) (bool, error) {
	var claimedName string
	err := s.db.QueryRowContext(ctx, `
		UPDATE background_jobs
		SET lease_until = $3,
			locked_by = $4,
			state = 'running',
			last_started_at = $2,
			last_error = NULL
		WHERE name = $1
			AND enabled = TRUE
			AND next_run_at <= $2
			AND (lease_until IS NULL OR lease_until <= $2)
		RETURNING name
	`, job.Name, now.UTC(), now.UTC().Add(job.LeaseTTL), instanceID).Scan(&claimedName)
	if err == nil {
		return true, nil
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	return false, fmt.Errorf("claim job %s: %w", job.Name, err)
}

func (s *Store) MarkSuccess(ctx context.Context, job Job, finishedAt time.Time, duration time.Duration, result, instanceID string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE background_jobs
		SET lease_until = NULL,
			locked_by = NULL,
			state = 'idle',
			last_finished_at = $2,
			last_success_at = $2,
			last_duration_ms = $3,
			consecutive_failures = 0,
			last_error = NULL,
			last_result = $4,
			next_run_at = $5
		WHERE name = $1
			AND locked_by = $6
	`, job.Name, finishedAt.UTC(), duration.Milliseconds(), nullableString(result), finishedAt.UTC().Add(job.Interval), instanceID)
	if err != nil {
		return fmt.Errorf("mark job %s success: %w", job.Name, err)
	}
	return ensureRowUpdated(res, job.Name, "success")
}

func (s *Store) MarkFailure(ctx context.Context, job Job, finishedAt time.Time, duration time.Duration, errText, result, instanceID string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE background_jobs
		SET lease_until = NULL,
			locked_by = NULL,
			state = 'idle',
			last_finished_at = $2,
			last_duration_ms = $3,
			consecutive_failures = consecutive_failures + 1,
			last_error = $4,
			last_result = $5,
			next_run_at = $6
		WHERE name = $1
			AND locked_by = $7
	`, job.Name, finishedAt.UTC(), duration.Milliseconds(), errText, nullableString(result), finishedAt.UTC().Add(job.RetryDelay), instanceID)
	if err != nil {
		return fmt.Errorf("mark job %s failure: %w", job.Name, err)
	}
	return ensureRowUpdated(res, job.Name, "failure")
}

func ensureRowUpdated(res sql.Result, jobName, action string) error {
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s rows affected for %s: %w", action, jobName, err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("job %s lost lease before %s update", jobName, action)
	}
	return nil
}

func durationSeconds(d time.Duration) int {
	if d <= 0 {
		return 1
	}
	seconds := int(d / time.Second)
	if d%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		return 1
	}
	return seconds
}

func nullableString(val string) any {
	if val == "" {
		return nil
	}
	return val
}
