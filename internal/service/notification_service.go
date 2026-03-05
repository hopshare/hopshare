package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"hopshare/internal/types"
)

const defaultMemberNotificationLimit = 20

func CreateMemberNotification(ctx context.Context, db *sql.DB, memberID int64, text string, href string) error {
	if db == nil {
		return ErrNilDB
	}
	return createMemberNotification(ctx, db, memberID, text, href)
}

func createMemberNotification(ctx context.Context, db execer, memberID int64, text string, href string) error {
	if memberID == 0 {
		return ErrMissingMemberID
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return ErrMissingField
	}
	href = strings.TrimSpace(href)

	var hrefValue any
	if href != "" {
		hrefValue = href
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO member_notifications (member_id, text, href)
		VALUES ($1, $2, $3)
	`, memberID, text, hrefValue); err != nil {
		return fmt.Errorf("create member notification: %w", err)
	}
	return nil
}

func ListMemberNotifications(ctx context.Context, db *sql.DB, memberID int64, limit int) ([]types.MemberNotification, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if memberID == 0 {
		return nil, ErrMissingMemberID
	}
	if limit <= 0 {
		limit = defaultMemberNotificationLimit
	}

	rows, err := db.QueryContext(ctx, `
		SELECT id, member_id, text, href, created_at
		FROM member_notifications
		WHERE member_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2
	`, memberID, limit)
	if err != nil {
		return nil, fmt.Errorf("list member notifications: %w", err)
	}
	defer rows.Close()

	out := make([]types.MemberNotification, 0, limit)
	for rows.Next() {
		var n types.MemberNotification
		var href sql.NullString
		if err := rows.Scan(&n.ID, &n.MemberID, &n.Text, &href, &n.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan member notification: %w", err)
		}
		if href.Valid {
			n.Href = &href.String
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list member notifications: %w", err)
	}
	return out, nil
}

func DeleteMemberNotification(ctx context.Context, db *sql.DB, memberID, notificationID int64) error {
	if db == nil {
		return ErrNilDB
	}
	if memberID == 0 {
		return ErrMissingMemberID
	}
	if notificationID == 0 {
		return ErrMissingField
	}

	if _, err := db.ExecContext(ctx, `
		DELETE FROM member_notifications
		WHERE id = $1 AND member_id = $2
	`, notificationID, memberID); err != nil {
		return fmt.Errorf("delete member notification: %w", err)
	}
	return nil
}
