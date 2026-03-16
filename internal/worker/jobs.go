package worker

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"hopshare/internal/auth"
	"hopshare/internal/service"
)

const (
	ExpireDueHopsJobName         = "expire_due_hops"
	DeleteExpiredSessionsJobName = "delete_expired_sessions"
	ExpireNotificationsJobName   = "expire_member_notifications"
)

type Job struct {
	Name       string
	Interval   time.Duration
	LeaseTTL   time.Duration
	Timeout    time.Duration
	RetryDelay time.Duration
	Run        func(ctx context.Context, db *sql.DB, now time.Time) (string, error)
}

type JobConfig struct {
	ExpireDueHopsInterval         time.Duration
	DeleteExpiredSessionsInterval time.Duration
	ExpireNotificationAge         time.Duration
	ExpireNotificationInterval    time.Duration
}

func DefaultJobs(cfg JobConfig) []Job {
	expireInterval := cfg.ExpireDueHopsInterval
	if expireInterval <= 0 {
		expireInterval = time.Hour
	}

	sessionInterval := cfg.DeleteExpiredSessionsInterval
	if sessionInterval <= 0 {
		sessionInterval = 6 * time.Hour
	}

	expireNotificationAge := cfg.ExpireNotificationAge
	if expireNotificationAge < 0 {
		expireNotificationAge = 0
	}

	expireNotificationInterval := cfg.ExpireNotificationInterval
	if expireNotificationInterval <= 0 {
		expireNotificationInterval = 24 * time.Hour
	}

	jobs := []Job{
		{
			Name:       ExpireDueHopsJobName,
			Interval:   expireInterval,
			LeaseTTL:   10 * time.Minute,
			Timeout:    2 * time.Minute,
			RetryDelay: 15 * time.Minute,
			Run: func(ctx context.Context, db *sql.DB, now time.Time) (string, error) {
				count, err := service.ExpireDueHops(ctx, db, now)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("expired=%d", count), nil
			},
		},
		{
			Name:       DeleteExpiredSessionsJobName,
			Interval:   sessionInterval,
			LeaseTTL:   10 * time.Minute,
			Timeout:    time.Minute,
			RetryDelay: 15 * time.Minute,
			Run: func(ctx context.Context, db *sql.DB, now time.Time) (string, error) {
				count, err := auth.DeleteExpiredSessions(ctx, db, now)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("deleted=%d", count), nil
			},
		},
	}

	if expireNotificationAge > 0 {
		jobs = append(jobs, Job{
			Name:       ExpireNotificationsJobName,
			Interval:   expireNotificationInterval,
			LeaseTTL:   10 * time.Minute,
			Timeout:    time.Minute,
			RetryDelay: 15 * time.Minute,
			Run: func(ctx context.Context, db *sql.DB, now time.Time) (string, error) {
				count, err := service.DeleteMemberNotificationsBefore(ctx, db, now.Add(-expireNotificationAge))
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("deleted=%d", count), nil
			},
		})
	}

	return jobs
}
