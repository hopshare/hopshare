package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"hopshare/internal/types"
)

const defaultAdminMemberSearchLimit = 25

type AdjustMemberHourBalanceParams struct {
	OrganizationID int64
	MemberID       int64
	AdminMemberID  int64
	HoursDelta     int
	Reason         string
}

type AdminDeleteMemberResult struct {
	ReopenedAcceptedHopCount int
	CanceledOpenHopCount     int
	WithdrawnOfferCount      int
	EndedMembershipCount     int
}

func SearchMembersForAdmin(ctx context.Context, db *sql.DB, query string, limit int) ([]types.AdminUserSearchResult, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if limit <= 0 {
		limit = defaultAdminMemberSearchLimit
	}

	query = strings.TrimSpace(query)
	searchPattern := "%"
	if query != "" {
		searchPattern = "%" + strings.ToLower(query) + "%"
	}

	rows, err := db.QueryContext(ctx, `
			SELECT
				id,
				first_name,
				last_name,
				email,
			enabled,
			last_login_at
		FROM members
			WHERE (
				$1 = '%'
				OR LOWER(email) LIKE $1
				OR LOWER(first_name) LIKE $1
				OR LOWER(last_name) LIKE $1
			)
			AND deleted_at IS NULL
			ORDER BY LOWER(last_name) ASC, LOWER(first_name) ASC, LOWER(email) ASC
			LIMIT $2
		`, searchPattern, limit)
	if err != nil {
		return nil, fmt.Errorf("search members for admin: %w", err)
	}
	defer rows.Close()

	results := make([]types.AdminUserSearchResult, 0)
	for rows.Next() {
		var row types.AdminUserSearchResult
		var lastLoginAt sql.NullTime
		if err := rows.Scan(
			&row.MemberID,
			&row.FirstName,
			&row.LastName,
			&row.Email,
			&row.Enabled,
			&lastLoginAt,
		); err != nil {
			return nil, fmt.Errorf("scan admin member search row: %w", err)
		}
		if lastLoginAt.Valid {
			v := lastLoginAt.Time
			row.LastLoginAt = &v
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search members for admin: %w", err)
	}

	return results, nil
}

func AdminUserDetail(ctx context.Context, db *sql.DB, memberID int64) (types.AdminUserDetail, error) {
	if db == nil {
		return types.AdminUserDetail{}, ErrNilDB
	}
	if memberID == 0 {
		return types.AdminUserDetail{}, ErrMissingMemberID
	}

	var detail types.AdminUserDetail
	if err := db.QueryRowContext(ctx, `
			SELECT id, first_name, last_name, email, enabled, verified, last_login_at, created_at, updated_at
			FROM members
			WHERE id = $1
				AND deleted_at IS NULL
		`, memberID).Scan(
		&detail.MemberID,
		&detail.FirstName,
		&detail.LastName,
		&detail.Email,
		&detail.Enabled,
		&detail.Verified,
		&detail.LastLoginAt,
		&detail.CreatedAt,
		&detail.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return types.AdminUserDetail{}, sql.ErrNoRows
		}
		return types.AdminUserDetail{}, fmt.Errorf("load admin user detail: %w", err)
	}

	memberships, err := adminUserMembershipTimeline(ctx, db, memberID)
	if err != nil {
		return types.AdminUserDetail{}, err
	}
	detail.Memberships = memberships

	balances, err := adminUserActiveBalances(ctx, db, memberID)
	if err != nil {
		return types.AdminUserDetail{}, err
	}
	detail.ActiveBalances = balances

	return detail, nil
}

func SetMemberEnabled(ctx context.Context, db *sql.DB, memberID int64, enabled bool) error {
	if db == nil {
		return ErrNilDB
	}
	if memberID == 0 {
		return ErrMissingMemberID
	}

	res, err := db.ExecContext(ctx, `
		UPDATE members
		SET enabled = $1, updated_at = NOW()
		WHERE id = $2
	`, enabled, memberID)
	if err != nil {
		return fmt.Errorf("set member enabled: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("set member enabled rows affected: %w", err)
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func AdminForcePasswordReset(ctx context.Context, db *sql.DB, memberID int64) error {
	if db == nil {
		return ErrNilDB
	}
	if memberID == 0 {
		return ErrMissingMemberID
	}

	tempSecret, err := randomAdminResetSecret()
	if err != nil {
		return fmt.Errorf("generate forced reset secret: %w", err)
	}
	hash, err := HashPassword(tempSecret)
	if err != nil {
		return err
	}
	if err := UpdateMemberPassword(ctx, db, memberID, hash); err != nil {
		return err
	}
	return nil
}

func AdjustMemberHourBalance(ctx context.Context, db *sql.DB, p AdjustMemberHourBalanceParams) error {
	if db == nil {
		return ErrNilDB
	}
	if p.OrganizationID == 0 {
		return ErrMissingOrgID
	}
	if p.MemberID == 0 || p.AdminMemberID == 0 {
		return ErrMissingMemberID
	}
	if p.HoursDelta == 0 {
		return ErrInvalidHoursDelta
	}
	reason := strings.TrimSpace(p.Reason)
	if reason == "" {
		return ErrMissingField
	}

	var membershipExists bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM organization_memberships
			WHERE organization_id = $1
				AND member_id = $2
				AND left_at IS NULL
		)
	`, p.OrganizationID, p.MemberID).Scan(&membershipExists); err != nil {
		return fmt.Errorf("check member organization membership: %w", err)
	}
	if !membershipExists {
		return ErrMembershipNotFound
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO hour_balance_adjustments (
			organization_id,
			member_id,
			admin_member_id,
			hours_delta,
			reason
		)
		VALUES ($1, $2, $3, $4, $5)
	`, p.OrganizationID, p.MemberID, p.AdminMemberID, p.HoursDelta, reason); err != nil {
		return fmt.Errorf("insert hour balance adjustment: %w", err)
	}

	return nil
}

func AdminDisableMember(ctx context.Context, db *sql.DB, memberID, actorMemberID int64, actorName string) (int, error) {
	if db == nil {
		return 0, ErrNilDB
	}
	if memberID == 0 {
		return 0, ErrMissingMemberID
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin disable member: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		UPDATE members
		SET enabled = FALSE, updated_at = NOW()
		WHERE id = $1
	`, memberID)
	if err != nil {
		return 0, fmt.Errorf("disable member: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("disable member rows affected: %w", err)
	}
	if affected == 0 {
		return 0, sql.ErrNoRows
	}

	actorName = strings.TrimSpace(actorName)
	if actorName == "" {
		actorName = "hopShare Admin"
	}

	reopenedHops, err := ReopenAcceptedHopsForMember(ctx, tx, memberID)
	if err != nil {
		return 0, err
	}
	if len(reopenedHops) > 0 {
		if err := sendReopenedHopNotifications(ctx, tx, reopenedHops, memberID, actorMemberID, actorName); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit disable member: %w", err)
	}
	return len(reopenedHops), nil
}

func AdminDeleteMember(ctx context.Context, db *sql.DB, memberID, actorMemberID int64, actorName string) (AdminDeleteMemberResult, error) {
	if db == nil {
		return AdminDeleteMemberResult{}, ErrNilDB
	}
	if memberID == 0 || actorMemberID == 0 {
		return AdminDeleteMemberResult{}, ErrMissingMemberID
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return AdminDeleteMemberResult{}, fmt.Errorf("begin delete member: %w", err)
	}
	defer tx.Rollback()

	var deletedAt sql.NullTime
	if err := tx.QueryRowContext(ctx, `
		SELECT deleted_at
		FROM members
		WHERE id = $1
		FOR UPDATE
	`, memberID).Scan(&deletedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AdminDeleteMemberResult{}, sql.ErrNoRows
		}
		return AdminDeleteMemberResult{}, fmt.Errorf("lock member for delete: %w", err)
	}
	if deletedAt.Valid {
		return AdminDeleteMemberResult{}, ErrMemberAlreadyDeleted
	}

	var isSoleOwner bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM organization_memberships om
			WHERE om.member_id = $1
				AND om.role = 'owner'
				AND om.left_at IS NULL
				AND NOT EXISTS (
					SELECT 1
					FROM organization_memberships other
					WHERE other.organization_id = om.organization_id
						AND other.role = 'owner'
						AND other.left_at IS NULL
						AND other.member_id <> $1
				)
		)
	`, memberID).Scan(&isSoleOwner); err != nil {
		return AdminDeleteMemberResult{}, fmt.Errorf("check sole owner before delete: %w", err)
	}
	if isSoleOwner {
		return AdminDeleteMemberResult{}, ErrMemberDeleteBlocked
	}

	actorName = strings.TrimSpace(actorName)
	if actorName == "" {
		actorName = "hopShare Admin"
	}

	result := AdminDeleteMemberResult{}

	reopenedHops, err := ReopenAcceptedHopsForMember(ctx, tx, memberID)
	if err != nil {
		return AdminDeleteMemberResult{}, err
	}
	result.ReopenedAcceptedHopCount = len(reopenedHops)
	if len(reopenedHops) > 0 {
		if err := sendReopenedHopNotifications(ctx, tx, reopenedHops, memberID, actorMemberID, actorName); err != nil {
			return AdminDeleteMemberResult{}, err
		}
	}

	now := time.Now().UTC()
	canceledOpenCount, err := adminCancelOpenHopsForMember(ctx, tx, memberID, actorMemberID, actorName, now)
	if err != nil {
		return AdminDeleteMemberResult{}, err
	}
	result.CanceledOpenHopCount = canceledOpenCount

	withdrawnOfferCount, err := adminWithdrawPendingOffersForMember(ctx, tx, memberID, now)
	if err != nil {
		return AdminDeleteMemberResult{}, err
	}
	result.WithdrawnOfferCount = withdrawnOfferCount

	membershipRes, err := tx.ExecContext(ctx, `
		UPDATE organization_memberships
		SET left_at = GREATEST($2, joined_at + INTERVAL '1 microsecond')
		WHERE member_id = $1
			AND left_at IS NULL
	`, memberID, now)
	if err != nil {
		return AdminDeleteMemberResult{}, fmt.Errorf("close active memberships for delete: %w", err)
	}
	endedMemberships, err := membershipRes.RowsAffected()
	if err != nil {
		return AdminDeleteMemberResult{}, fmt.Errorf("close active memberships rows affected: %w", err)
	}
	result.EndedMembershipCount = int(endedMemberships)

	placeholderSecret, err := randomAdminResetSecret()
	if err != nil {
		return AdminDeleteMemberResult{}, fmt.Errorf("generate tombstone password secret: %w", err)
	}
	placeholderHash, err := HashPassword(placeholderSecret)
	if err != nil {
		return AdminDeleteMemberResult{}, fmt.Errorf("generate tombstone password hash: %w", err)
	}
	tombstoneEmail := deletedMemberEmail(memberID, now)

	memberRes, err := tx.ExecContext(ctx, `
		UPDATE members
		SET first_name = $1,
			last_name = $2,
			email = $3,
			password_hash = $4,
			preferred_contact = $5,
			profile_picture_url = NULL,
			avatar_content_type = NULL,
			avatar_data = NULL,
			city = NULL,
			state = NULL,
			current_organization = NULL,
			enabled = FALSE,
			verified = FALSE,
			deleted_at = $6,
			updated_at = $6
		WHERE id = $7
			AND deleted_at IS NULL
	`, "Deleted", "User", tombstoneEmail, placeholderHash, "", now, memberID)
	if err != nil {
		return AdminDeleteMemberResult{}, fmt.Errorf("tombstone member: %w", err)
	}
	memberAffected, err := memberRes.RowsAffected()
	if err != nil {
		return AdminDeleteMemberResult{}, fmt.Errorf("tombstone member rows affected: %w", err)
	}
	if memberAffected == 0 {
		return AdminDeleteMemberResult{}, ErrMemberAlreadyDeleted
	}

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM member_tokens
		WHERE member_id = $1
	`, memberID); err != nil {
		return AdminDeleteMemberResult{}, fmt.Errorf("delete member tokens: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM member_sessions
		WHERE member_id = $1
	`, memberID); err != nil {
		return AdminDeleteMemberResult{}, fmt.Errorf("delete member sessions: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return AdminDeleteMemberResult{}, fmt.Errorf("commit delete member: %w", err)
	}

	return result, nil
}

func adminUserMembershipTimeline(ctx context.Context, db *sql.DB, memberID int64) ([]types.AdminUserMembershipTimelineEntry, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT
			om.organization_id,
			o.name,
			o.url_name,
			om.role,
			om.joined_at,
			om.left_at
		FROM organization_memberships om
		JOIN organizations o ON o.id = om.organization_id
		WHERE om.member_id = $1
		ORDER BY om.joined_at DESC, om.id DESC
	`, memberID)
	if err != nil {
		return nil, fmt.Errorf("list admin user memberships: %w", err)
	}
	defer rows.Close()

	entries := make([]types.AdminUserMembershipTimelineEntry, 0)
	for rows.Next() {
		var row types.AdminUserMembershipTimelineEntry
		var leftAt sql.NullTime
		if err := rows.Scan(
			&row.OrganizationID,
			&row.OrganizationName,
			&row.OrganizationURLName,
			&row.Role,
			&row.JoinedAt,
			&leftAt,
		); err != nil {
			return nil, fmt.Errorf("scan admin user membership: %w", err)
		}
		if leftAt.Valid {
			v := leftAt.Time
			row.LeftAt = &v
		}
		entries = append(entries, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list admin user memberships: %w", err)
	}
	return entries, nil
}

func adminUserActiveBalances(ctx context.Context, db *sql.DB, memberID int64) ([]types.AdminUserBalanceEntry, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT
			om.organization_id,
			o.name,
			o.url_name,
			COALESCE((
				SELECT
					COALESCE(SUM(CASE WHEN ht.to_member_id = $1 THEN ht.hours ELSE 0 END), 0)
					- COALESCE(SUM(CASE WHEN ht.from_member_id = $1 THEN ht.hours ELSE 0 END), 0)
				FROM hop_transactions ht
				WHERE ht.organization_id = om.organization_id
					AND (ht.to_member_id = $1 OR ht.from_member_id = $1)
			), 0)
			+
			COALESCE((
				SELECT COALESCE(SUM(hba.hours_delta), 0)
				FROM hour_balance_adjustments hba
				WHERE hba.organization_id = om.organization_id
					AND hba.member_id = $1
			), 0)
			AS balance_hours
		FROM organization_memberships om
		JOIN organizations o ON o.id = om.organization_id
		WHERE om.member_id = $1
			AND om.left_at IS NULL
		ORDER BY o.name ASC
	`, memberID)
	if err != nil {
		return nil, fmt.Errorf("list admin user active balances: %w", err)
	}
	defer rows.Close()

	entries := make([]types.AdminUserBalanceEntry, 0)
	for rows.Next() {
		var row types.AdminUserBalanceEntry
		if err := rows.Scan(
			&row.OrganizationID,
			&row.OrganizationName,
			&row.OrganizationURLName,
			&row.BalanceHours,
		); err != nil {
			return nil, fmt.Errorf("scan admin user active balance: %w", err)
		}
		entries = append(entries, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list admin user active balances: %w", err)
	}
	return entries, nil
}

func sendReopenedHopNotifications(ctx context.Context, tx *sql.Tx, hops []ReopenedAcceptedHop, disabledMemberID, actorMemberID int64, actorName string) error {
	senderName := strings.TrimSpace(actorName)
	if senderName == "" {
		senderName = "hopShare Admin"
	}

	var senderID *int64
	if actorMemberID > 0 {
		senderID = &actorMemberID
	}

	for _, hop := range hops {
		recipients := make([]int64, 0, 2)
		if hop.CreatedBy != disabledMemberID {
			recipients = append(recipients, hop.CreatedBy)
		}
		if hop.AcceptedBy != disabledMemberID && hop.AcceptedBy != hop.CreatedBy {
			recipients = append(recipients, hop.AcceptedBy)
		}

		subject := "Accepted hop reopened: " + truncateRunes(strings.TrimSpace(hop.Title), 80)
		body := fmt.Sprintf(
			"An administrator disabled a user account involved in this accepted hop, so the hop was moved back to open status and needs a new acceptance.\n\nHop: %s\nHop ID: %d",
			strings.TrimSpace(hop.Title),
			hop.ID,
		)

		for _, recipientID := range recipients {
			if err := insertMessage(ctx, tx, recipientID, senderID, senderName, types.MessageTypeInformation, nil, nil, nil, subject, body); err != nil {
				return err
			}
		}
	}

	return nil
}

func adminCancelOpenHopsForMember(ctx context.Context, tx *sql.Tx, memberID, actorMemberID int64, actorName string, now time.Time) (int, error) {
	rows, err := tx.QueryContext(ctx, `
		UPDATE hops
		SET status = $1,
			canceled_by = $2,
			canceled_at = GREATEST($3, created_at + INTERVAL '1 microsecond'),
			updated_at = GREATEST($3, created_at + INTERVAL '1 microsecond')
		WHERE created_by = $4
			AND status = $5
		RETURNING id, title
	`, types.HopStatusCanceled, actorMemberID, now, memberID, types.HopStatusOpen)
	if err != nil {
		return 0, fmt.Errorf("cancel open hops for deleted member: %w", err)
	}
	defer rows.Close()

	type canceledHop struct {
		ID    int64
		Title string
	}
	canceled := make([]canceledHop, 0)
	for rows.Next() {
		var hop canceledHop
		if err := rows.Scan(&hop.ID, &hop.Title); err != nil {
			return 0, fmt.Errorf("scan canceled hop for deleted member: %w", err)
		}
		canceled = append(canceled, hop)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("cancel open hops for deleted member: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close canceled hops for deleted member: %w", err)
	}

	senderName := strings.TrimSpace(actorName)
	if senderName == "" {
		senderName = "hopShare Admin"
	}
	var senderID *int64
	if actorMemberID > 0 {
		senderID = &actorMemberID
	}

	for _, hop := range canceled {
		offerRows, err := tx.QueryContext(ctx, `
			SELECT member_id
			FROM hop_help_offers
			WHERE hop_id = $1
				AND status IS NULL
			FOR UPDATE
		`, hop.ID)
		if err != nil {
			return 0, fmt.Errorf("list pending offerers for canceled deleted-member hop: %w", err)
		}
		recipients := make([]int64, 0)
		for offerRows.Next() {
			var recipientID int64
			if err := offerRows.Scan(&recipientID); err != nil {
				offerRows.Close()
				return 0, fmt.Errorf("scan pending offerer for canceled deleted-member hop: %w", err)
			}
			recipients = append(recipients, recipientID)
		}
		if err := offerRows.Err(); err != nil {
			offerRows.Close()
			return 0, fmt.Errorf("list pending offerers for canceled deleted-member hop: %w", err)
		}
		if err := offerRows.Close(); err != nil {
			return 0, fmt.Errorf("close pending offerers for canceled deleted-member hop: %w", err)
		}

		if _, err := tx.ExecContext(ctx, `
			UPDATE hop_help_offers
			SET status = $1,
				denied_at = $2
			WHERE hop_id = $3
				AND status IS NULL
		`, types.HopOfferStatusDenied, now, hop.ID); err != nil {
			return 0, fmt.Errorf("close pending offers for canceled deleted-member hop: %w", err)
		}
		subject := "Hop canceled by administrator: " + truncateRunes(strings.TrimSpace(hop.Title), 80)
		body := fmt.Sprintf(
			"An administrator permanently deleted the account that created this hop, so the hop was canceled.\n\nHop: %s\nHop ID: %d",
			strings.TrimSpace(hop.Title),
			hop.ID,
		)
		for _, recipientID := range recipients {
			if err := insertMessage(ctx, tx, recipientID, senderID, senderName, types.MessageTypeInformation, nil, nil, nil, subject, body); err != nil {
				return 0, err
			}
		}
	}

	return len(canceled), nil
}

func adminWithdrawPendingOffersForMember(ctx context.Context, tx *sql.Tx, memberID int64, now time.Time) (int, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT hop_id
		FROM hop_help_offers
		WHERE member_id = $1
			AND status IS NULL
		FOR UPDATE
	`, memberID)
	if err != nil {
		return 0, fmt.Errorf("list pending offers for deleted member: %w", err)
	}
	defer rows.Close()

	hopIDs := make([]int64, 0)
	for rows.Next() {
		var hopID int64
		if err := rows.Scan(&hopID); err != nil {
			return 0, fmt.Errorf("scan pending offer for deleted member: %w", err)
		}
		hopIDs = append(hopIDs, hopID)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("list pending offers for deleted member: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close pending offers for deleted member: %w", err)
	}
	if len(hopIDs) == 0 {
		return 0, nil
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE hop_help_offers
		SET status = $1,
			denied_at = $2
		WHERE member_id = $3
			AND status IS NULL
	`, types.HopOfferStatusDenied, now, memberID); err != nil {
		return 0, fmt.Errorf("withdraw pending offers for deleted member: %w", err)
	}
	return len(hopIDs), nil
}

func deletedMemberEmail(memberID int64, now time.Time) string {
	return fmt.Sprintf("deleted+%d-%d@deleted.hopshare.local", memberID, now.UnixNano())
}

func randomAdminResetSecret() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
