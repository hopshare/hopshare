package worker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"hopshare/internal/auth"
	"hopshare/internal/database"
	"hopshare/internal/database/migrate"
	"hopshare/internal/service"
	"hopshare/internal/types"
)

var (
	workerDBOnce     sync.Once
	sharedWorkerDB   *sql.DB
	workerDBSetupErr error
)

func requireWorkerDB(t *testing.T) *sql.DB {
	t.Helper()

	workerDBOnce.Do(func() {
		dbURL := os.Getenv("HOPSHARE_DB_URL")
		if dbURL == "" {
			dbURL = os.Getenv("DATABASE_URL")
		}
		if dbURL == "" {
			workerDBSetupErr = errors.New("HOPSHARE_DB_URL or DATABASE_URL not set")
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		sharedWorkerDB, workerDBSetupErr = database.New(ctx, dbURL)
		if workerDBSetupErr != nil {
			return
		}
		workerDBSetupErr = migrate.Run(ctx, sharedWorkerDB)
	})

	if workerDBSetupErr != nil {
		if workerDBSetupErr.Error() == "HOPSHARE_DB_URL or DATABASE_URL not set" {
			t.Skip(workerDBSetupErr.Error())
		}
		t.Fatalf("worker database setup failed: %v", workerDBSetupErr)
	}
	return sharedWorkerDB
}

func TestStoreSyncClaimAndComplete(t *testing.T) {
	db := requireWorkerDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	jobName := fmt.Sprintf("worker_store_%d", time.Now().UnixNano())
	now := time.Now().UTC()
	job := Job{
		Name:       jobName,
		Interval:   time.Hour,
		LeaseTTL:   5 * time.Minute,
		Timeout:    time.Minute,
		RetryDelay: 10 * time.Minute,
		Run: func(context.Context, *sql.DB, time.Time) (string, error) {
			return "", nil
		},
	}
	t.Cleanup(func() {
		cleanupBackgroundJobRow(t, db, jobName)
	})

	if err := store.SyncDefinitions(ctx, []Job{job}, now); err != nil {
		t.Fatalf("sync definitions: %v", err)
	}

	claimed, err := store.TryClaim(ctx, job, now, "test-instance")
	if err != nil {
		t.Fatalf("claim job: %v", err)
	}
	if !claimed {
		t.Fatalf("expected job to be claimed")
	}

	claimedAgain, err := store.TryClaim(ctx, job, now, "other-instance")
	if err != nil {
		t.Fatalf("claim job second time: %v", err)
	}
	if claimedAgain {
		t.Fatalf("expected second claim to fail while lease is held")
	}

	finishedAt := now.Add(2 * time.Second)
	if err := store.MarkSuccess(ctx, job, finishedAt, 2*time.Second, "ok", "test-instance"); err != nil {
		t.Fatalf("mark success: %v", err)
	}

	var (
		state             string
		lockedBy          sql.NullString
		lastResult        sql.NullString
		consecutiveErrors int
	)
	if err := db.QueryRowContext(ctx, `
		SELECT state, locked_by, last_result, consecutive_failures
		FROM background_jobs
		WHERE name = $1
	`, jobName).Scan(&state, &lockedBy, &lastResult, &consecutiveErrors); err != nil {
		t.Fatalf("load background job row: %v", err)
	}
	if state != "idle" {
		t.Fatalf("expected idle state, got %q", state)
	}
	if lockedBy.Valid {
		t.Fatalf("expected lease to be cleared, got locked_by=%q", lockedBy.String)
	}
	if !lastResult.Valid || lastResult.String != "ok" {
		t.Fatalf("expected last result %q, got %+v", "ok", lastResult)
	}
	if consecutiveErrors != 0 {
		t.Fatalf("expected no consecutive failures, got %d", consecutiveErrors)
	}
}

func TestStoreFailureSchedulesRetry(t *testing.T) {
	db := requireWorkerDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	jobName := fmt.Sprintf("worker_retry_%d", time.Now().UnixNano())
	now := time.Now().UTC()
	job := Job{
		Name:       jobName,
		Interval:   time.Hour,
		LeaseTTL:   5 * time.Minute,
		Timeout:    time.Minute,
		RetryDelay: 7 * time.Minute,
		Run: func(context.Context, *sql.DB, time.Time) (string, error) {
			return "", nil
		},
	}
	t.Cleanup(func() {
		cleanupBackgroundJobRow(t, db, jobName)
	})

	if err := store.SyncDefinitions(ctx, []Job{job}, now); err != nil {
		t.Fatalf("sync definitions: %v", err)
	}
	claimed, err := store.TryClaim(ctx, job, now, "test-instance")
	if err != nil {
		t.Fatalf("claim job: %v", err)
	}
	if !claimed {
		t.Fatalf("expected job to be claimed")
	}

	finishedAt := now.Add(time.Second)
	if err := store.MarkFailure(ctx, job, finishedAt, time.Second, "boom", "", "test-instance"); err != nil {
		t.Fatalf("mark failure: %v", err)
	}

	var (
		failures  int
		lastError sql.NullString
		nextRunAt time.Time
	)
	if err := db.QueryRowContext(ctx, `
		SELECT consecutive_failures, last_error, next_run_at
		FROM background_jobs
		WHERE name = $1
	`, jobName).Scan(&failures, &lastError, &nextRunAt); err != nil {
		t.Fatalf("load background job row: %v", err)
	}
	if failures != 1 {
		t.Fatalf("expected one failure, got %d", failures)
	}
	if !lastError.Valid || lastError.String != "boom" {
		t.Fatalf("expected last error %q, got %+v", "boom", lastError)
	}
	wantNextRun := finishedAt.Add(job.RetryDelay)
	if nextRunAt.Before(wantNextRun.Add(-time.Second)) || nextRunAt.After(wantNextRun.Add(time.Second)) {
		t.Fatalf("next run: got %v want about %v", nextRunAt, wantNextRun)
	}
}

func TestExpireDueHopsJobLogic(t *testing.T) {
	db := requireWorkerDB(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	now := time.Now().UTC()
	orgA, ownerA := createWorkerTestOrganization(t, ctx, db, "expire_a")
	orgB, ownerB := createWorkerTestOrganization(t, ctx, db, "expire_b")

	futureDate := now.AddDate(0, 0, 2)

	expiredA, err := service.CreateHop(ctx, db, service.CreateHopParams{
		OrganizationID: orgA.ID,
		MemberID:       ownerA.ID,
		Title:          "Expired A",
		EstimatedHours: 1,
		NeededByKind:   types.HopNeededByOn,
		NeededByDate:   &futureDate,
	})
	if err != nil {
		t.Fatalf("create expired hop A: %v", err)
	}
	expiredB, err := service.CreateHop(ctx, db, service.CreateHopParams{
		OrganizationID: orgB.ID,
		MemberID:       ownerB.ID,
		Title:          "Expired B",
		EstimatedHours: 1,
		NeededByKind:   types.HopNeededByOn,
		NeededByDate:   &futureDate,
	})
	if err != nil {
		t.Fatalf("create expired hop B: %v", err)
	}
	activeHop, err := service.CreateHop(ctx, db, service.CreateHopParams{
		OrganizationID: orgA.ID,
		MemberID:       ownerA.ID,
		Title:          "Active",
		EstimatedHours: 1,
		NeededByKind:   types.HopNeededByOn,
		NeededByDate:   &futureDate,
	})
	if err != nil {
		t.Fatalf("create active hop: %v", err)
	}

	expiredAt := now.Add(-2 * time.Hour)
	if _, err := db.ExecContext(ctx, `
		UPDATE hops
		SET expires_at = $1
		WHERE id IN ($2, $3)
	`, expiredAt, expiredA.ID, expiredB.ID); err != nil {
		t.Fatalf("mark hops expired: %v", err)
	}

	count, err := service.ExpireDueHops(ctx, db, now)
	if err != nil {
		t.Fatalf("expire due hops: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 expired hops, got %d", count)
	}

	checkHopStatus(t, ctx, db, orgA.ID, expiredA.ID, types.HopStatusExpired)
	checkHopStatus(t, ctx, db, orgB.ID, expiredB.ID, types.HopStatusExpired)
	checkHopStatus(t, ctx, db, orgA.ID, activeHop.ID, types.HopStatusOpen)
}

func TestDeleteExpiredSessionsJobLogic(t *testing.T) {
	db := requireWorkerDB(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	member := createWorkerTestMember(t, ctx, db, "session_gc")
	now := time.Now().UTC()

	expiredTokenID := fmt.Sprintf("%032x", now.UnixNano())
	activeTokenID := fmt.Sprintf("%032x", now.UnixNano()+1)
	expiredHash := strings.Repeat("a", 64)
	activeHash := strings.Repeat("b", 64)

	if _, err := db.ExecContext(ctx, `
		INSERT INTO member_sessions (
			token_id,
			token_hash,
			member_id,
			created_at,
			last_activity_at,
			absolute_expires_at,
			idle_expires_at
		)
		VALUES
			($1, $2, $3, $4, $4, $5, $6),
			($7, $8, $3, $4, $4, $9, $10)
	`, expiredTokenID, expiredHash, member.ID, now.Add(-2*time.Hour), now.Add(-time.Minute), now.Add(-time.Minute), activeTokenID, activeHash, now.Add(24*time.Hour), now.Add(24*time.Hour)); err != nil {
		t.Fatalf("insert sessions: %v", err)
	}

	count, err := auth.DeleteExpiredSessions(ctx, db, now)
	if err != nil {
		t.Fatalf("delete expired sessions: %v", err)
	}
	// This suite uses a shared integration database, so the global cleanup may
	// remove other expired sessions left behind by unrelated tests.
	if count < 1 {
		t.Fatalf("expected at least 1 deleted session, got %d", count)
	}

	var expiredExists bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM member_sessions
			WHERE token_id = $1
		)
	`, expiredTokenID).Scan(&expiredExists); err != nil {
		t.Fatalf("check expired session deletion: %v", err)
	}
	if expiredExists {
		t.Fatalf("expected expired session %q to be deleted", expiredTokenID)
	}

	var activeExists bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM member_sessions
			WHERE token_id = $1
		)
	`, activeTokenID).Scan(&activeExists); err != nil {
		t.Fatalf("check active session remains: %v", err)
	}
	if !activeExists {
		t.Fatalf("expected active session %q to remain", activeTokenID)
	}
}

func TestExpireNotificationsJobLogic(t *testing.T) {
	db := requireWorkerDB(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	member := createWorkerTestMember(t, ctx, db, "notification_gc")
	now := time.Now().UTC()

	var expiredID int64
	if err := db.QueryRowContext(ctx, `
		INSERT INTO member_notifications (member_id, text, created_at)
		VALUES ($1, $2, $3)
		RETURNING id
	`, member.ID, "expired notification", now.Add(-6*24*time.Hour)).Scan(&expiredID); err != nil {
		t.Fatalf("insert expired notification: %v", err)
	}

	var activeID int64
	if err := db.QueryRowContext(ctx, `
		INSERT INTO member_notifications (member_id, text, created_at)
		VALUES ($1, $2, $3)
		RETURNING id
	`, member.ID, "active notification", now.Add(-24*time.Hour)).Scan(&activeID); err != nil {
		t.Fatalf("insert active notification: %v", err)
	}

	jobs := DefaultJobs(JobConfig{
		ExpireNotificationAge:      5 * 24 * time.Hour,
		ExpireNotificationInterval: 24 * time.Hour,
	})

	var job Job
	found := false
	for _, candidate := range jobs {
		if candidate.Name == ExpireNotificationsJobName {
			job = candidate
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %q job to be configured", ExpireNotificationsJobName)
	}
	if job.Interval != 24*time.Hour {
		t.Fatalf("notification job interval: got %s want %s", job.Interval, 24*time.Hour)
	}

	result, err := job.Run(ctx, db, now)
	if err != nil {
		t.Fatalf("run notification expiration job: %v", err)
	}
	if !strings.Contains(result, "deleted=") {
		t.Fatalf("unexpected job result %q", result)
	}

	var expiredExists bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM member_notifications
			WHERE id = $1
		)
	`, expiredID).Scan(&expiredExists); err != nil {
		t.Fatalf("check expired notification deletion: %v", err)
	}
	if expiredExists {
		t.Fatalf("expected expired notification %d to be deleted", expiredID)
	}

	var activeExists bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM member_notifications
			WHERE id = $1
		)
	`, activeID).Scan(&activeExists); err != nil {
		t.Fatalf("check active notification remains: %v", err)
	}
	if !activeExists {
		t.Fatalf("expected active notification %d to remain", activeID)
	}
}

func createWorkerTestOrganization(t *testing.T, ctx context.Context, db *sql.DB, label string) (types.Organization, types.Member) {
	t.Helper()

	suffix := fmt.Sprintf("%s_%d", label, time.Now().UnixNano())
	member := createWorkerTestMember(t, ctx, db, suffix)
	org, err := service.CreateOrganization(ctx, db, "Worker "+suffix, "Test City", "TS", "Worker org", member.ID)
	if err != nil {
		t.Fatalf("create organization %s: %v", label, err)
	}
	return org, member
}

func createWorkerTestMember(t *testing.T, ctx context.Context, db *sql.DB, label string) types.Member {
	t.Helper()

	suffix := fmt.Sprintf("%s_%d", label, time.Now().UnixNano())
	member, err := service.CreateMember(ctx, db, types.Member{
		FirstName:        "Worker",
		LastName:         "Test",
		Email:            suffix + "@example.com",
		PasswordHash:     "hashed_password",
		PreferredContact: suffix + "@example.com",
		Enabled:          true,
		Verified:         true,
	})
	if err != nil {
		t.Fatalf("create member %s: %v", label, err)
	}
	return member
}

func checkHopStatus(t *testing.T, ctx context.Context, db *sql.DB, orgID, hopID int64, want string) {
	t.Helper()

	hop, err := service.GetHopByID(ctx, db, orgID, hopID)
	if err != nil {
		t.Fatalf("load hop %d: %v", hopID, err)
	}
	if hop.Status != want {
		t.Fatalf("hop %d status: got %q want %q", hopID, hop.Status, want)
	}
}

func cleanupBackgroundJobRow(t *testing.T, db *sql.DB, jobName string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := db.ExecContext(ctx, `DELETE FROM background_jobs WHERE name = $1`, jobName); err != nil {
		t.Fatalf("cleanup background job %q: %v", jobName, err)
	}
}
