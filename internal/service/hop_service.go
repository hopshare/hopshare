package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"hopshare/internal/types"
)

type CreateHopParams struct {
	OrganizationID int64
	MemberID       int64
	Title          string
	Details        string
	EstimatedHours int
	NeededByKind   string
	NeededByDate   *time.Time
}

func CreateHop(ctx context.Context, db *sql.DB, p CreateHopParams) (types.Hop, error) {
	if db == nil {
		return types.Hop{}, ErrNilDB
	}
	if p.OrganizationID == 0 {
		return types.Hop{}, ErrMissingOrgID
	}
	if p.MemberID == 0 {
		return types.Hop{}, ErrMissingMemberID
	}
	title := strings.TrimSpace(p.Title)
	if title == "" {
		return types.Hop{}, ErrMissingField
	}
	if p.EstimatedHours < 1 || p.EstimatedHours > 8 {
		return types.Hop{}, ErrMissingField
	}

	if err := requireActiveMembership(ctx, db, p.OrganizationID, p.MemberID); err != nil {
		return types.Hop{}, err
	}

	neededByKind := strings.TrimSpace(p.NeededByKind)
	var neededByDate sql.NullTime
	var expiresAt sql.NullTime
	switch neededByKind {
	case types.HopNeededByAnytime:
	case types.HopNeededByOn, types.HopNeededByAround, types.HopNeededByNoLaterThan:
		if p.NeededByDate == nil || p.NeededByDate.IsZero() {
			return types.Hop{}, ErrMissingField
		}
		date := *p.NeededByDate
		neededByDate = sql.NullTime{Time: date, Valid: true}
		expiry := hopExpiryAt(neededByKind, date)
		expiresAt = sql.NullTime{Time: expiry, Valid: true}
	default:
		return types.Hop{}, ErrMissingField
	}

	var hopID int64
	if err := db.QueryRowContext(ctx, `
		INSERT INTO hops (
			organization_id,
			created_by,
			title,
			details,
			estimated_hours,
			needed_by_kind,
			needed_by_date,
			expires_at,
			status
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id
	`, p.OrganizationID, p.MemberID, title, nullableString(strings.TrimSpace(p.Details)), p.EstimatedHours, neededByKind, nullableTime(neededByDate), nullableTime(expiresAt), types.HopStatusOpen).Scan(&hopID); err != nil {
		return types.Hop{}, fmt.Errorf("create hop: %w", err)
	}

	req, err := GetHopByID(ctx, db, p.OrganizationID, hopID)
	if err != nil {
		return types.Hop{}, err
	}
	return req, nil
}

func AcceptHop(ctx context.Context, db *sql.DB, orgID, hopID, accepterID int64) error {
	if db == nil {
		return ErrNilDB
	}
	if orgID == 0 {
		return ErrMissingOrgID
	}
	if hopID == 0 {
		return ErrHopNotFound
	}
	if accepterID == 0 {
		return ErrMissingMemberID
	}

	if err := requireActiveMembership(ctx, db, orgID, accepterID); err != nil {
		return err
	}

	res, err := db.ExecContext(ctx, `
		UPDATE hops
		SET status = $1, accepted_by = $2, accepted_at = NOW(), updated_at = NOW()
		WHERE id = $3 AND organization_id = $4 AND status = $5 AND created_by <> $2
	`, types.HopStatusAccepted, accepterID, hopID, orgID, types.HopStatusOpen)
	if err != nil {
		return fmt.Errorf("accept hop: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("accept hop rows affected: %w", err)
	}
	if affected == 0 {
		return ErrHopInvalidState
	}
	return nil
}

type OfferHopParams struct {
	OrganizationID int64
	HopID          int64
	OffererID      int64
	OffererName    string
}

func OfferHopHelp(ctx context.Context, db *sql.DB, p OfferHopParams) error {
	if db == nil {
		return ErrNilDB
	}
	if p.OrganizationID == 0 {
		return ErrMissingOrgID
	}
	if p.HopID == 0 {
		return ErrHopNotFound
	}
	if p.OffererID == 0 {
		return ErrMissingMemberID
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin offer hop: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if err := requireActiveMembership(ctx, tx, p.OrganizationID, p.OffererID); err != nil {
		return err
	}

	var createdBy int64
	var status string
	var title string
	var details sql.NullString
	if err = tx.QueryRowContext(ctx, `
		SELECT created_by, status, title, details
		FROM hops
		WHERE id = $1 AND organization_id = $2
	`, p.HopID, p.OrganizationID).Scan(&createdBy, &status, &title, &details); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrHopNotFound
		}
		return fmt.Errorf("load hop for offer: %w", err)
	}
	if status != types.HopStatusOpen {
		return ErrHopInvalidState
	}
	if createdBy == p.OffererID {
		return ErrHopForbidden
	}

	if _, err = tx.ExecContext(ctx, `
		INSERT INTO hop_help_offers (hop_id, member_id, offered_at)
		VALUES ($1, $2, NOW())
	`, p.HopID, p.OffererID); err != nil {
		return fmt.Errorf("create hop offer: %w", err)
	}

	offererName := strings.TrimSpace(p.OffererName)
	if offererName == "" {
		return ErrMissingField
	}

	description := hopDescription(title, stringPtrFromNull(details))
	body := fmt.Sprintf(
		"Congratulations! %s has offered to help you with your Hop request! If you wish to accept their help, use the button below!\n\nHop request:\n%s",
		offererName,
		description,
	)
	senderID := p.OffererID
	if err = insertMessage(ctx, tx, createdBy, &senderID, offererName, types.MessageTypeAction, &p.HopID, nil, nil, "Hop help offer", body); err != nil {
		return err
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit offer hop: %w", err)
	}
	return nil
}

func AcceptHopOfferMessage(ctx context.Context, db *sql.DB, messageID, recipientID int64, responderName, responseBody string) error {
	if db == nil {
		return ErrNilDB
	}
	if messageID == 0 {
		return ErrMessageNotFound
	}
	if recipientID == 0 {
		return ErrMissingMemberID
	}
	responderName = strings.TrimSpace(responderName)
	if responderName == "" {
		return ErrMissingField
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin accept offer: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	offererID, hopID, err := loadPendingActionMessage(ctx, tx, messageID, recipientID)
	if err != nil {
		return err
	}
	if offererID == recipientID {
		return ErrHopForbidden
	}

	var orgID int64
	var createdBy int64
	var status string
	var title string
	var details sql.NullString
	if err = tx.QueryRowContext(ctx, `
		SELECT organization_id, created_by, status, title, details
		FROM hops
		WHERE id = $1
		FOR UPDATE
	`, hopID).Scan(&orgID, &createdBy, &status, &title, &details); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrHopNotFound
		}
		return fmt.Errorf("load hop for accept offer: %w", err)
	}
	if createdBy != recipientID {
		return ErrHopForbidden
	}
	if status != types.HopStatusOpen {
		return ErrHopInvalidState
	}

	if err := requireActiveMembership(ctx, tx, orgID, recipientID); err != nil {
		return err
	}
	if err := requireActiveMembership(ctx, tx, orgID, offererID); err != nil {
		return err
	}

	if _, err = tx.ExecContext(ctx, `
		UPDATE hops
		SET status = $1, accepted_by = $2, accepted_at = NOW(), updated_at = NOW()
		WHERE id = $3 AND organization_id = $4
	`, types.HopStatusAccepted, offererID, hopID, orgID); err != nil {
		return fmt.Errorf("accept hop offer: %w", err)
	}

	now := time.Now().UTC()

	res, err := tx.ExecContext(ctx, `
		UPDATE hop_help_offers
		SET status = $1, accepted_at = $2
		WHERE hop_id = $3 AND member_id = $4 AND status IS NULL
	`, types.HopOfferStatusAccepted, now, hopID, offererID)
	if err != nil {
		return fmt.Errorf("accept hop offer: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("accept hop offer rows affected: %w", err)
	}
	if affected == 0 {
		return ErrInvalidMessage
	}

	if err = markActionMessage(ctx, tx, messageID, recipientID, types.MessageActionAccepted, now); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `
		UPDATE messages
		SET action_status = $1, action_taken_at = $2, read_at = COALESCE(read_at, $2)
		WHERE recipient_member_id = $3 AND hop_id = $4 AND message_type = $5 AND action_status IS NULL AND id <> $6
	`, types.MessageActionDeclined, now, recipientID, hopID, types.MessageTypeAction, messageID); err != nil {
		return fmt.Errorf("decline other offers: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `
		UPDATE hop_help_offers
		SET status = $1, denied_at = $2
		WHERE hop_id = $3 AND member_id <> $4 AND status IS NULL
	`, types.HopOfferStatusDenied, now, hopID, offererID); err != nil {
		return fmt.Errorf("deny other hop offers: %w", err)
	}

	description := hopDescription(title, stringPtrFromNull(details))
	subject := "Accepted: " + truncateRunes(description, 100)
	body := strings.TrimSpace(responseBody)
	if body == "" {
		body = fmt.Sprintf("Your offer to help with \"%s\" was accepted.", description)
	}
	senderID := recipientID
	if err = insertMessage(ctx, tx, offererID, &senderID, responderName, types.MessageTypeInformation, nil, nil, nil, subject, body); err != nil {
		return err
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit accept offer: %w", err)
	}
	return nil
}

func DeclineHopOfferMessage(ctx context.Context, db *sql.DB, messageID, recipientID int64, responderName, responseBody string) error {
	if db == nil {
		return ErrNilDB
	}
	if messageID == 0 {
		return ErrMessageNotFound
	}
	if recipientID == 0 {
		return ErrMissingMemberID
	}
	responderName = strings.TrimSpace(responderName)
	if responderName == "" {
		return ErrMissingField
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin decline offer: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	offererID, hopID, err := loadPendingActionMessage(ctx, tx, messageID, recipientID)
	if err != nil {
		return err
	}
	if offererID == recipientID {
		return ErrHopForbidden
	}

	now := time.Now().UTC()
	if err = markActionMessage(ctx, tx, messageID, recipientID, types.MessageActionDeclined, now); err != nil {
		return err
	}

	res, err := tx.ExecContext(ctx, `
		UPDATE hop_help_offers
		SET status = $1, denied_at = $2
		WHERE hop_id = $3 AND member_id = $4 AND status IS NULL
	`, types.HopOfferStatusDenied, now, hopID, offererID)
	if err != nil {
		return fmt.Errorf("deny hop offer: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("deny hop offer rows affected: %w", err)
	}
	if affected == 0 {
		return ErrInvalidMessage
	}

	var title string
	var details sql.NullString
	if err = tx.QueryRowContext(ctx, `
		SELECT title, details
		FROM hops
		WHERE id = $1
	`, hopID).Scan(&title, &details); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("load hop title: %w", err)
	}

	description := hopDescription(title, stringPtrFromNull(details))
	subject := "Declined: " + truncateRunes(description, 100)
	body := strings.TrimSpace(responseBody)
	if body == "" {
		body = fmt.Sprintf("Your offer to help with \"%s\" was declined.", description)
	}
	senderID := recipientID
	if err = insertMessage(ctx, tx, offererID, &senderID, responderName, types.MessageTypeInformation, nil, nil, nil, subject, body); err != nil {
		return err
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit decline offer: %w", err)
	}
	return nil
}

func CancelHop(ctx context.Context, db *sql.DB, orgID, hopID, cancelerID int64) error {
	if db == nil {
		return ErrNilDB
	}
	if orgID == 0 {
		return ErrMissingOrgID
	}
	if hopID == 0 {
		return ErrHopNotFound
	}
	if cancelerID == 0 {
		return ErrMissingMemberID
	}

	if err := requireActiveMembership(ctx, db, orgID, cancelerID); err != nil {
		return err
	}

	res, err := db.ExecContext(ctx, `
		UPDATE hops
		SET status = $1, canceled_by = $2, canceled_at = NOW(), updated_at = NOW()
		WHERE id = $3 AND organization_id = $4 AND created_by = $2 AND status IN ($5, $6)
	`, types.HopStatusCanceled, cancelerID, hopID, orgID, types.HopStatusOpen, types.HopStatusAccepted)
	if err != nil {
		return fmt.Errorf("cancel hop: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("cancel hop rows affected: %w", err)
	}
	if affected == 0 {
		return ErrHopInvalidState
	}
	return nil
}

type CompleteHopParams struct {
	OrganizationID int64
	HopID          int64
	CompletedBy    int64
	Comment        string
	CompletedHours int
}

func CompleteHop(ctx context.Context, db *sql.DB, p CompleteHopParams) error {
	if db == nil {
		return ErrNilDB
	}
	if p.OrganizationID == 0 {
		return ErrMissingOrgID
	}
	if p.HopID == 0 {
		return ErrHopNotFound
	}
	if p.CompletedBy == 0 {
		return ErrMissingMemberID
	}
	comment := strings.TrimSpace(p.Comment)
	if comment == "" {
		return ErrMissingField
	}

	if err := requireActiveMembership(ctx, db, p.OrganizationID, p.CompletedBy); err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin complete hop: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var createdBy int64
	var acceptedBy sql.NullInt64
	var estimatedHours int
	var status string
	row := tx.QueryRowContext(ctx, `
		SELECT created_by, accepted_by, estimated_hours, status
		FROM hops
		WHERE id = $1 AND organization_id = $2
		FOR UPDATE
	`, p.HopID, p.OrganizationID)
	if err = row.Scan(&createdBy, &acceptedBy, &estimatedHours, &status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrHopNotFound
		}
		return fmt.Errorf("load hop for completion: %w", err)
	}

	if status != types.HopStatusAccepted || !acceptedBy.Valid {
		return ErrHopInvalidState
	}
	if p.CompletedBy != createdBy && p.CompletedBy != acceptedBy.Int64 {
		return ErrHopForbidden
	}

	hours := p.CompletedHours
	if hours <= 0 {
		hours = estimatedHours
	}
	if hours <= 0 {
		return ErrMissingField
	}

	if _, err = tx.ExecContext(ctx, `
		UPDATE hops
		SET status = $1, completed_by = $2, completed_at = NOW(), completed_hours = $3, completion_comment = $4, updated_at = NOW()
		WHERE id = $5 AND organization_id = $6 AND status = $7
	`, types.HopStatusCompleted, p.CompletedBy, hours, comment, p.HopID, p.OrganizationID, types.HopStatusAccepted); err != nil {
		return fmt.Errorf("mark hop completed: %w", err)
	}

	if _, err = tx.ExecContext(ctx, `
		INSERT INTO hop_transactions (organization_id, hop_id, from_member_id, to_member_id, hours)
		VALUES ($1, $2, $3, $4, $5)
	`, p.OrganizationID, p.HopID, createdBy, acceptedBy.Int64, hours); err != nil {
		return fmt.Errorf("insert hop transaction: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit complete hop: %w", err)
	}
	return nil
}

func ExpireHops(ctx context.Context, db *sql.DB, orgID int64, now time.Time) (int64, error) {
	if db == nil {
		return 0, ErrNilDB
	}
	if orgID == 0 {
		return 0, ErrMissingOrgID
	}

	res, err := db.ExecContext(ctx, `
		UPDATE hops
		SET status = $1, updated_at = NOW()
		WHERE organization_id = $2
			AND status IN ($3, $4)
			AND expires_at IS NOT NULL
			AND expires_at <= $5
	`, types.HopStatusExpired, orgID, types.HopStatusOpen, types.HopStatusAccepted, now)
	if err != nil {
		return 0, fmt.Errorf("expire hops: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("expire hops rows affected: %w", err)
	}
	return affected, nil
}

func GetHopByID(ctx context.Context, db *sql.DB, orgID, hopID int64) (types.Hop, error) {
	if db == nil {
		return types.Hop{}, ErrNilDB
	}
	if orgID == 0 {
		return types.Hop{}, ErrMissingOrgID
	}
	if hopID == 0 {
		return types.Hop{}, ErrHopNotFound
	}

	row := db.QueryRowContext(ctx, `
		SELECT
			r.id, r.organization_id, r.created_by, mc.username,
			r.title, r.details, r.estimated_hours,
			r.needed_by_kind, r.needed_by_date, r.expires_at,
			r.status,
			r.accepted_by, ma.username, r.accepted_at,
			r.canceled_by, r.canceled_at,
			r.completed_by, r.completed_at, r.completed_hours, r.completion_comment,
			r.created_at, r.updated_at
		FROM hops r
		JOIN members mc ON mc.id = r.created_by
		LEFT JOIN members ma ON ma.id = r.accepted_by
		WHERE r.organization_id = $1 AND r.id = $2
	`, orgID, hopID)
	req, err := scanHopRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return types.Hop{}, ErrHopNotFound
		}
		return types.Hop{}, fmt.Errorf("get hop: %w", err)
	}
	return req, nil
}

func ListMemberHops(ctx context.Context, db *sql.DB, orgID, memberID int64) ([]types.Hop, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if orgID == 0 {
		return nil, ErrMissingOrgID
	}
	if memberID == 0 {
		return nil, ErrMissingMemberID
	}

	if err := requireActiveMembership(ctx, db, orgID, memberID); err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx, `
		SELECT
			r.id, r.organization_id, r.created_by, mc.username,
			r.title, r.details, r.estimated_hours,
			r.needed_by_kind, r.needed_by_date, r.expires_at,
			r.status,
			r.accepted_by, ma.username, r.accepted_at,
			r.canceled_by, r.canceled_at,
			r.completed_by, r.completed_at, r.completed_hours, r.completion_comment,
			r.created_at, r.updated_at
		FROM hops r
		JOIN members mc ON mc.id = r.created_by
		LEFT JOIN members ma ON ma.id = r.accepted_by
		WHERE r.organization_id = $1
			AND (
				r.created_by = $2
				OR r.accepted_by = $2
				OR r.canceled_by = $2
				OR r.completed_by = $2
				OR EXISTS (
					SELECT 1
					FROM hop_help_offers hho
					WHERE hho.hop_id = r.id AND hho.member_id = $2 AND hho.status IS NULL
				)
			)
		ORDER BY r.created_at DESC
	`, orgID, memberID)
	if err != nil {
		return nil, fmt.Errorf("list member hops: %w", err)
	}
	defer rows.Close()

	var out []types.Hop
	for rows.Next() {
		req, err := scanHopRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan hop: %w", err)
		}
		out = append(out, req)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list member hops: %w", err)
	}
	return out, nil
}

func ListHopsToHelp(ctx context.Context, db *sql.DB, orgID, memberID int64) ([]types.Hop, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if orgID == 0 {
		return nil, ErrMissingOrgID
	}
	if memberID == 0 {
		return nil, ErrMissingMemberID
	}

	if err := requireActiveMembership(ctx, db, orgID, memberID); err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx, `
		SELECT
			r.id, r.organization_id, r.created_by, mc.username,
			r.title, r.details, r.estimated_hours,
			r.needed_by_kind, r.needed_by_date, r.expires_at,
			r.status,
			r.accepted_by, ma.username, r.accepted_at,
			r.canceled_by, r.canceled_at,
			r.completed_by, r.completed_at, r.completed_hours, r.completion_comment,
			r.created_at, r.updated_at
		FROM hops r
		JOIN members mc ON mc.id = r.created_by
		LEFT JOIN members ma ON ma.id = r.accepted_by
		WHERE r.organization_id = $1
			AND (
				(r.status = $2 AND r.created_by <> $3)
				OR (r.status = $4 AND r.accepted_by = $3)
			)
		ORDER BY r.created_at DESC
	`, orgID, types.HopStatusOpen, memberID, types.HopStatusAccepted)
	if err != nil {
		return nil, fmt.Errorf("list hops to help: %w", err)
	}
	defer rows.Close()

	var out []types.Hop
	for rows.Next() {
		req, err := scanHopRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan hop: %w", err)
		}
		out = append(out, req)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list hops to help: %w", err)
	}
	return out, nil
}

func RecentCompletedHops(ctx context.Context, db *sql.DB, orgID int64, limit int) ([]types.Hop, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if orgID == 0 {
		return nil, ErrMissingOrgID
	}
	if limit <= 0 {
		limit = 5
	}

	rows, err := db.QueryContext(ctx, `
		SELECT
			r.id, r.organization_id, r.created_by, mc.username,
			r.title, r.details, r.estimated_hours,
			r.needed_by_kind, r.needed_by_date, r.expires_at,
			r.status,
			r.accepted_by, ma.username, r.accepted_at,
			r.canceled_by, r.canceled_at,
			r.completed_by, r.completed_at, r.completed_hours, r.completion_comment,
			r.created_at, r.updated_at
		FROM hops r
		JOIN members mc ON mc.id = r.created_by
		LEFT JOIN members ma ON ma.id = r.accepted_by
		WHERE r.organization_id = $1 AND r.status = $2
		ORDER BY r.completed_at DESC
		LIMIT $3
	`, orgID, types.HopStatusCompleted, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent completed hops: %w", err)
	}
	defer rows.Close()

	var out []types.Hop
	for rows.Next() {
		req, err := scanHopRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan hop: %w", err)
		}
		out = append(out, req)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list recent completed hops: %w", err)
	}
	return out, nil
}

func OrgMetrics(ctx context.Context, db *sql.DB, orgID int64) (types.OrgHopMetrics, error) {
	if db == nil {
		return types.OrgHopMetrics{}, ErrNilDB
	}
	if orgID == 0 {
		return types.OrgHopMetrics{}, ErrMissingOrgID
	}

	var m types.OrgHopMetrics
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM organization_memberships
		WHERE organization_id = $1 AND left_at IS NULL
	`, orgID).Scan(&m.MemberCount); err != nil {
		return types.OrgHopMetrics{}, fmt.Errorf("count members: %w", err)
	}

	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM hops
		WHERE organization_id = $1 AND status IN ($2, $3)
	`, orgID, types.HopStatusOpen, types.HopStatusAccepted).Scan(&m.PendingCount); err != nil {
		return types.OrgHopMetrics{}, fmt.Errorf("count pending hops: %w", err)
	}

	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM hops
		WHERE organization_id = $1 AND status = $2
	`, orgID, types.HopStatusCompleted).Scan(&m.CompletedCount); err != nil {
		return types.OrgHopMetrics{}, fmt.Errorf("count completed hops: %w", err)
	}

	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM hops
		WHERE organization_id = $1 AND status = $2 AND completed_at >= NOW() - INTERVAL '7 days'
	`, orgID, types.HopStatusCompleted).Scan(&m.CompletedThisWeek); err != nil {
		return types.OrgHopMetrics{}, fmt.Errorf("count completed hops this week: %w", err)
	}

	return m, nil
}

func MemberStats(ctx context.Context, db *sql.DB, orgID, memberID int64) (types.MemberHopStats, error) {
	if db == nil {
		return types.MemberHopStats{}, ErrNilDB
	}
	if orgID == 0 {
		return types.MemberHopStats{}, ErrMissingOrgID
	}
	if memberID == 0 {
		return types.MemberHopStats{}, ErrMissingMemberID
	}

	if err := requireActiveMembership(ctx, db, orgID, memberID); err != nil {
		return types.MemberHopStats{}, err
	}

	var stats types.MemberHopStats
	if err := db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN to_member_id = $2 THEN hours ELSE 0 END), 0) -
			COALESCE(SUM(CASE WHEN from_member_id = $2 THEN hours ELSE 0 END), 0)
		FROM hop_transactions
		WHERE organization_id = $1 AND (to_member_id = $2 OR from_member_id = $2)
	`, orgID, memberID).Scan(&stats.BalanceHours); err != nil {
		return types.MemberHopStats{}, fmt.Errorf("load balance: %w", err)
	}

	var lastMade sql.NullTime
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*), MAX(created_at)
		FROM hops
		WHERE organization_id = $1 AND created_by = $2
	`, orgID, memberID).Scan(&stats.HopsMade, &lastMade); err != nil {
		return types.MemberHopStats{}, fmt.Errorf("load hops made: %w", err)
	}
	if lastMade.Valid {
		stats.LastHopMadeAt = &lastMade.Time
	}

	var lastFulfilled sql.NullTime
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*), MAX(completed_at)
		FROM hops
		WHERE organization_id = $1 AND accepted_by = $2 AND status = $3
	`, orgID, memberID, types.HopStatusCompleted).Scan(&stats.HopsFulfilled, &lastFulfilled); err != nil {
		return types.MemberHopStats{}, fmt.Errorf("load hops fulfilled: %w", err)
	}
	if lastFulfilled.Valid {
		stats.LastHopFulfilledAt = &lastFulfilled.Time
	}

	return stats, nil
}

func PendingHopOfferIDs(ctx context.Context, db *sql.DB, memberID int64) (map[int64]struct{}, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if memberID == 0 {
		return nil, ErrMissingMemberID
	}

	rows, err := db.QueryContext(ctx, `
		SELECT hop_id
		FROM hop_help_offers
		WHERE member_id = $1 AND status IS NULL
	`, memberID)
	if err != nil {
		return nil, fmt.Errorf("list pending hop offers: %w", err)
	}
	defer rows.Close()

	out := make(map[int64]struct{})
	for rows.Next() {
		var hopID int64
		if err := rows.Scan(&hopID); err != nil {
			return nil, fmt.Errorf("scan pending hop offer: %w", err)
		}
		out[hopID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list pending hop offers: %w", err)
	}
	return out, nil
}

func requireActiveMembership(ctx context.Context, q queryer, orgID, memberID int64) error {
	var exists bool
	if err := q.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM organization_memberships
			WHERE organization_id = $1 AND member_id = $2 AND left_at IS NULL
		)
	`, orgID, memberID).Scan(&exists); err != nil {
		return fmt.Errorf("check membership: %w", err)
	}
	if !exists {
		return ErrHopForbidden
	}
	return nil
}

func hopExpiryAt(kind string, date time.Time) time.Time {
	expiry := time.Date(date.Year(), date.Month(), date.Day(), 23, 59, 59, 0, time.UTC)
	if kind == types.HopNeededByAround {
		expiry = expiry.AddDate(0, 0, 2)
	}
	return expiry
}

func nullableString(v string) interface{} {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

func nullableTime(nt sql.NullTime) interface{} {
	if !nt.Valid {
		return nil
	}
	return nt.Time
}

func stringPtrFromNull(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	v := strings.TrimSpace(ns.String)
	if v == "" {
		return nil
	}
	return &v
}

func hopDescription(title string, details *string) string {
	desc := strings.TrimSpace(title)
	if details != nil {
		detailsValue := strings.TrimSpace(*details)
		if detailsValue != "" {
			if desc != "" {
				desc = desc + ": " + detailsValue
			} else {
				desc = detailsValue
			}
		}
	}
	if desc == "" {
		desc = "Hop request"
	}
	return desc
}

func truncateRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit])
}

type scanFunc interface {
	Scan(dest ...any) error
}

func scanHopRow(s scanFunc) (types.Hop, error) {
	var r types.Hop
	var details sql.NullString
	var neededByDate sql.NullTime
	var expiresAt sql.NullTime
	var acceptedBy sql.NullInt64
	var acceptedByName sql.NullString
	var acceptedAt sql.NullTime
	var canceledBy sql.NullInt64
	var canceledAt sql.NullTime
	var completedBy sql.NullInt64
	var completedAt sql.NullTime
	var completedHours sql.NullInt64
	var completionComment sql.NullString

	if err := s.Scan(
		&r.ID, &r.OrganizationID, &r.CreatedBy, &r.CreatedByName,
		&r.Title, &details, &r.EstimatedHours,
		&r.NeededByKind, &neededByDate, &expiresAt,
		&r.Status,
		&acceptedBy, &acceptedByName, &acceptedAt,
		&canceledBy, &canceledAt,
		&completedBy, &completedAt, &completedHours, &completionComment,
		&r.CreatedAt, &r.UpdatedAt,
	); err != nil {
		return types.Hop{}, err
	}

	if details.Valid {
		r.Details = &details.String
	}
	if neededByDate.Valid {
		t := neededByDate.Time
		r.NeededByDate = &t
	}
	if expiresAt.Valid {
		t := expiresAt.Time
		r.ExpiresAt = &t
	}
	if acceptedBy.Valid {
		v := acceptedBy.Int64
		r.AcceptedBy = &v
	}
	if acceptedByName.Valid {
		v := acceptedByName.String
		r.AcceptedByName = &v
	}
	if acceptedAt.Valid {
		t := acceptedAt.Time
		r.AcceptedAt = &t
	}
	if canceledBy.Valid {
		v := canceledBy.Int64
		r.CanceledBy = &v
	}
	if canceledAt.Valid {
		t := canceledAt.Time
		r.CanceledAt = &t
	}
	if completedBy.Valid {
		v := completedBy.Int64
		r.CompletedBy = &v
	}
	if completedAt.Valid {
		t := completedAt.Time
		r.CompletedAt = &t
	}
	if completedHours.Valid {
		v := int(completedHours.Int64)
		r.CompletedHours = &v
	}
	if completionComment.Valid {
		v := completionComment.String
		r.CompletionComment = &v
	}
	return r, nil
}
