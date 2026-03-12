package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// DeleteExpiredSessions removes sessions whose absolute or idle expiry has passed.
func DeleteExpiredSessions(ctx context.Context, db *sql.DB, now time.Time) (int64, error) {
	if db == nil {
		return 0, errors.New("nil database")
	}

	res, err := db.ExecContext(ctx, `
		DELETE FROM member_sessions
		WHERE (absolute_expires_at IS NOT NULL AND absolute_expires_at <= $1)
			OR (idle_expires_at IS NOT NULL AND idle_expires_at <= $1)
	`, now.UTC())
	if err != nil {
		return 0, fmt.Errorf("delete expired sessions: %w", err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("expired session rows affected: %w", err)
	}
	return rowsAffected, nil
}
