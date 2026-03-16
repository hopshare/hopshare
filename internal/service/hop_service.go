package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"

	"hopshare/internal/types"
)

type CreateHopParams struct {
	OrganizationID int64
	MemberID       int64
	Kind           string
	Title          string
	Details        string
	EstimatedHours int
	NeededByKind   string
	NeededByDate   *time.Time
	IsPrivate      bool
}

type ReopenedAcceptedHop struct {
	ID             int64
	OrganizationID int64
	Title          string
	CreatedBy      int64
	AcceptedBy     int64
}

func normalizeHopKind(kind string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "":
		return types.HopKindAsk, nil
	case types.HopKindAsk:
		return types.HopKindAsk, nil
	case types.HopKindOffer:
		return types.HopKindOffer, nil
	default:
		return "", ErrMissingField
	}
}

func validateCreateHopBalance(kind string, balance int, policy timebankPolicy) error {
	switch kind {
	case types.HopKindAsk:
		if balance <= policy.MinBalance {
			return ErrHopRequestLimit
		}
	case types.HopKindOffer:
		if balance >= policy.MaxBalance {
			return ErrHopOfferLimit
		}
	default:
		return ErrMissingField
	}
	return nil
}

func validateInterestBalance(kind string, balance int, estimatedHours int, policy timebankPolicy) error {
	if kind != types.HopKindOffer {
		return nil
	}
	if balance-estimatedHours < policy.MinBalance {
		return ErrHopInterestLimit
	}
	return nil
}

func hopTransferParticipants(kind string, createdBy int64, matchedUser int64) (fromMemberID int64, toMemberID int64, err error) {
	switch kind {
	case types.HopKindAsk:
		return createdBy, matchedUser, nil
	case types.HopKindOffer:
		return matchedUser, createdBy, nil
	default:
		return 0, 0, ErrMissingField
	}
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
	hopKind, err := normalizeHopKind(p.Kind)
	if err != nil {
		return types.Hop{}, err
	}

	if err := requireActiveMembership(ctx, db, p.OrganizationID, p.MemberID); err != nil {
		return types.Hop{}, err
	}
	policy, err := loadTimebankPolicy(ctx, db, p.OrganizationID)
	if err != nil {
		return types.Hop{}, err
	}
	balance, err := loadMemberBalanceHours(ctx, db, p.OrganizationID, p.MemberID)
	if err != nil {
		return types.Hop{}, err
	}
	if err := validateCreateHopBalance(hopKind, balance, policy); err != nil {
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
		date := normalizeHopNeededByDate(*p.NeededByDate)
		if !hopNeededByDateIsFuture(date, time.Now()) {
			return types.Hop{}, ErrHopNeededByDate
		}
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
			hop_kind,
			created_user,
			title,
			details,
			estimated_hours,
			is_private,
			when_kind,
			when_at,
			expires_at,
			status
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, COALESCE($10, NOW() + INTERVAL '90 days'), $11)
		RETURNING id
	`, p.OrganizationID, hopKind, p.MemberID, title, nullableString(strings.TrimSpace(p.Details)), p.EstimatedHours, p.IsPrivate, neededByKind, nullableTime(neededByDate), nullableTime(expiresAt), types.HopStatusOpen).Scan(&hopID); err != nil {
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

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin accept hop: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var hopKind string
	var estimatedHours int
	if err = tx.QueryRowContext(ctx, `
		SELECT hop_kind, estimated_hours
		FROM hops
		WHERE id = $1 AND organization_id = $2 AND status = $3 AND created_user <> $4
		FOR UPDATE
	`, hopID, orgID, types.HopStatusOpen, accepterID).Scan(&hopKind, &estimatedHours); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrHopInvalidState
		}
		return fmt.Errorf("load hop for accept: %w", err)
	}
	policy, err := loadTimebankPolicy(ctx, tx, orgID)
	if err != nil {
		return err
	}
	balance, err := loadMemberBalanceHours(ctx, tx, orgID, accepterID)
	if err != nil {
		return err
	}
	if err := validateInterestBalance(hopKind, balance, estimatedHours, policy); err != nil {
		return err
	}

	res, err := tx.ExecContext(ctx, `
		UPDATE hops
		SET status = $1, matched_user = $2, accepted_at = NOW(), updated_at = NOW()
		WHERE id = $3 AND organization_id = $4 AND status = $5 AND created_user <> $2
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
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit accept hop: %w", err)
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
	var hopKind string
	var estimatedHours int
	var status string
	var title string
	var details sql.NullString
	if err = tx.QueryRowContext(ctx, `
		SELECT created_user, hop_kind, estimated_hours, status, title, details
		FROM hops
		WHERE id = $1 AND organization_id = $2
		FOR UPDATE
	`, p.HopID, p.OrganizationID).Scan(&createdBy, &hopKind, &estimatedHours, &status, &title, &details); err != nil {
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
	policy, err := loadTimebankPolicy(ctx, tx, p.OrganizationID)
	if err != nil {
		return err
	}
	balance, err := loadMemberBalanceHours(ctx, tx, p.OrganizationID, p.OffererID)
	if err != nil {
		return err
	}
	if err := validateInterestBalance(hopKind, balance, estimatedHours, policy); err != nil {
		return err
	}

	var existingStatus sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT status
		FROM hop_help_offers
		WHERE hop_id = $1 AND member_id = $2
		FOR UPDATE
	`, p.HopID, p.OffererID).Scan(&existingStatus)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO hop_help_offers (hop_id, member_id, offered_at)
			VALUES ($1, $2, NOW())
		`, p.HopID, p.OffererID); err != nil {
			return fmt.Errorf("create hop offer: %w", err)
		}
	case err != nil:
		return fmt.Errorf("check existing hop offer: %w", err)
	case !existingStatus.Valid:
		return ErrHopOfferExists
	case existingStatus.String == types.HopOfferStatusDenied:
		if _, err = tx.ExecContext(ctx, `
			UPDATE hop_help_offers
			SET offered_at = NOW(), status = NULL, accepted_at = NULL, denied_at = NULL
			WHERE hop_id = $1 AND member_id = $2
		`, p.HopID, p.OffererID); err != nil {
			return fmt.Errorf("reset denied hop offer: %w", err)
		}
	default:
		return ErrHopOfferExists
	}

	offererName := strings.TrimSpace(p.OffererName)
	if offererName == "" {
		return ErrMissingField
	}

	description := hopDescription(title, stringPtrFromNull(details))
	interestText := "is interested in your hop"
	if hopKind == types.HopKindAsk {
		interestText = "is interested in helping with your hop"
	}
	_ = createMemberNotification(
		ctx,
		tx,
		createdBy,
		offererName+" "+interestText+": "+description+".",
		hopDetailsHref(p.OrganizationID, p.HopID),
	)

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit offer hop: %w", err)
	}
	return nil
}

func AcceptPendingHopOffer(ctx context.Context, db *sql.DB, hopID, requesterID, offererID int64, responderName, responseBody string) error {
	if db == nil {
		return ErrNilDB
	}
	if hopID == 0 {
		return ErrHopNotFound
	}
	if requesterID == 0 || offererID == 0 {
		return ErrMissingMemberID
	}
	responderName = strings.TrimSpace(responderName)
	if responderName == "" {
		return ErrMissingField
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin accept pending hop offer: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if err = acceptPendingHopOfferTx(ctx, tx, hopID, requesterID, offererID, responderName, responseBody); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit accept pending hop offer: %w", err)
	}
	return nil
}

func DeclinePendingHopOffer(ctx context.Context, db *sql.DB, hopID, requesterID, offererID int64, responderName, responseBody string) error {
	if db == nil {
		return ErrNilDB
	}
	if hopID == 0 {
		return ErrHopNotFound
	}
	if requesterID == 0 || offererID == 0 {
		return ErrMissingMemberID
	}
	responderName = strings.TrimSpace(responderName)
	if responderName == "" {
		return ErrMissingField
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin decline pending hop offer: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if err = declinePendingHopOfferTx(ctx, tx, hopID, requesterID, offererID, responderName, responseBody); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit decline pending hop offer: %w", err)
	}
	return nil
}

func acceptPendingHopOfferTx(ctx context.Context, tx *sql.Tx, hopID, requesterID, offererID int64, responderName, responseBody string) error {
	if offererID == requesterID {
		return ErrHopForbidden
	}

	orgID, hopKind, estimatedHours, title, details, err := loadHopForPendingOfferAction(ctx, tx, hopID, requesterID)
	if err != nil {
		return err
	}
	otherOfferers, err := listOtherPendingHopOfferers(ctx, tx, hopID, offererID)
	if err != nil {
		return err
	}
	if err := requireActiveMembership(ctx, tx, orgID, requesterID); err != nil {
		return err
	}
	if err := requireActiveMembership(ctx, tx, orgID, offererID); err != nil {
		return err
	}
	policy, err := loadTimebankPolicy(ctx, tx, orgID)
	if err != nil {
		return err
	}
	balance, err := loadMemberBalanceHours(ctx, tx, orgID, offererID)
	if err != nil {
		return err
	}
	if err := validateInterestBalance(hopKind, balance, estimatedHours, policy); err != nil {
		return err
	}

	res, err := tx.ExecContext(ctx, `
		UPDATE hops
		SET status = $1, matched_user = $2, accepted_at = NOW(), updated_at = NOW()
		WHERE id = $3 AND organization_id = $4 AND status = $5
	`, types.HopStatusAccepted, offererID, hopID, orgID, types.HopStatusOpen)
	if err != nil {
		return fmt.Errorf("accept pending hop offer: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("accept pending hop offer rows affected: %w", err)
	}
	if affected == 0 {
		return ErrHopInvalidState
	}

	now := time.Now().UTC()
	res, err = tx.ExecContext(ctx, `
		UPDATE hop_help_offers
		SET status = $1, accepted_at = $2
		WHERE hop_id = $3 AND member_id = $4 AND status IS NULL
	`, types.HopOfferStatusAccepted, now, hopID, offererID)
	if err != nil {
		return fmt.Errorf("accept pending hop offer: %w", err)
	}
	affected, err = res.RowsAffected()
	if err != nil {
		return fmt.Errorf("accept pending hop offer rows affected: %w", err)
	}
	if affected == 0 {
		return ErrHopInvalidState
	}

	if _, err = tx.ExecContext(ctx, `
		UPDATE hop_help_offers
		SET status = $1, denied_at = $2
		WHERE hop_id = $3 AND member_id <> $4 AND status IS NULL
	`, types.HopOfferStatusDenied, now, hopID, offererID); err != nil {
		return fmt.Errorf("deny other pending hop offers: %w", err)
	}

	description := hopDescription(title, stringPtrFromNull(details))
	body := strings.TrimSpace(responseBody)
	if body == "" {
		body = fmt.Sprintf("You were matched on \"%s\".", description)
	}
	_ = createMemberNotification(
		ctx,
		tx,
		offererID,
		body,
		hopDetailsHref(orgID, hopID),
	)

	declineBody := fmt.Sprintf("%s matched this hop with someone else: %s.", responderName, title)
	for _, other := range otherOfferers {
		_ = createMemberNotification(
			ctx,
			tx,
			other.MemberID,
			declineBody,
			hopDetailsHref(orgID, hopID),
		)
	}

	return nil
}

func declinePendingHopOfferTx(ctx context.Context, tx *sql.Tx, hopID, requesterID, offererID int64, responderName, responseBody string) error {
	if offererID == requesterID {
		return ErrHopForbidden
	}

	orgID, _, _, title, details, err := loadHopForPendingOfferAction(ctx, tx, hopID, requesterID)
	if err != nil {
		return err
	}
	if err := requireActiveMembership(ctx, tx, orgID, requesterID); err != nil {
		return err
	}

	now := time.Now().UTC()
	res, err := tx.ExecContext(ctx, `
		UPDATE hop_help_offers
		SET status = $1, denied_at = $2
		WHERE hop_id = $3 AND member_id = $4 AND status IS NULL
	`, types.HopOfferStatusDenied, now, hopID, offererID)
	if err != nil {
		return fmt.Errorf("decline pending hop offer: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("decline pending hop offer rows affected: %w", err)
	}
	if affected == 0 {
		return ErrHopInvalidState
	}

	description := hopDescription(title, stringPtrFromNull(details))
	body := strings.TrimSpace(responseBody)
	if body == "" {
		body = fmt.Sprintf("Your offer to help with \"%s\" was declined.", description)
	}
	_ = createMemberNotification(
		ctx,
		tx,
		offererID,
		body,
		hopDetailsHref(orgID, hopID),
	)

	return nil
}

func loadHopForPendingOfferAction(ctx context.Context, tx *sql.Tx, hopID, requesterID int64) (int64, string, int, string, sql.NullString, error) {
	var orgID int64
	var createdBy int64
	var hopKind string
	var estimatedHours int
	var status string
	var title string
	var details sql.NullString
	if err := tx.QueryRowContext(ctx, `
		SELECT organization_id, created_user, hop_kind, estimated_hours, status, title, details
		FROM hops
		WHERE id = $1
		FOR UPDATE
	`, hopID).Scan(&orgID, &createdBy, &hopKind, &estimatedHours, &status, &title, &details); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, "", 0, "", sql.NullString{}, ErrHopNotFound
		}
		return 0, "", 0, "", sql.NullString{}, fmt.Errorf("load hop for pending offer action: %w", err)
	}
	if createdBy != requesterID {
		return 0, "", 0, "", sql.NullString{}, ErrHopForbidden
	}
	if status != types.HopStatusOpen {
		return 0, "", 0, "", sql.NullString{}, ErrHopInvalidState
	}
	return orgID, hopKind, estimatedHours, title, details, nil
}

func listOtherPendingHopOfferers(ctx context.Context, tx *sql.Tx, hopID, selectedOffererID int64) ([]types.PendingHopOffer, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT
			hho.member_id,
			COALESCE(NULLIF(TRIM(CONCAT_WS(' ', m.first_name, m.last_name)), ''), m.email),
			hho.offered_at
		FROM hop_help_offers hho
		JOIN members m ON m.id = hho.member_id
		WHERE hho.hop_id = $1
		  AND hho.member_id <> $2
		  AND hho.status IS NULL
		FOR UPDATE
	`, hopID, selectedOffererID)
	if err != nil {
		return nil, fmt.Errorf("list competing pending hop offers: %w", err)
	}
	defer rows.Close()

	var offerers []types.PendingHopOffer
	for rows.Next() {
		var offerer types.PendingHopOffer
		if err := rows.Scan(&offerer.MemberID, &offerer.MemberName, &offerer.OfferedAt); err != nil {
			return nil, fmt.Errorf("scan competing pending hop offer: %w", err)
		}
		offerers = append(offerers, offerer)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list competing pending hop offers: %w", err)
	}
	return offerers, nil
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

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin cancel hop: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var createdBy int64
	var acceptedBy sql.NullInt64
	var status string
	var title string
	var cancelerName string
	if err = tx.QueryRowContext(ctx, `
		SELECT
			h.created_user,
			h.matched_user,
			h.status,
			h.title,
			COALESCE(NULLIF(TRIM(CONCAT_WS(' ', m.first_name, m.last_name)), ''), m.email)
		FROM hops h
		JOIN members m ON m.id = h.created_user
		WHERE h.id = $1 AND h.organization_id = $2
		FOR UPDATE
	`, hopID, orgID).Scan(&createdBy, &acceptedBy, &status, &title, &cancelerName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrHopNotFound
		}
		return fmt.Errorf("load hop for cancel: %w", err)
	}

	if createdBy != cancelerID {
		return ErrHopInvalidState
	}
	if status != types.HopStatusOpen && status != types.HopStatusAccepted {
		return ErrHopInvalidState
	}

	res, err := tx.ExecContext(ctx, `
		UPDATE hops
		SET status = $1, canceled_by = $2, canceled_at = NOW(), updated_at = NOW()
		WHERE id = $3 AND organization_id = $4 AND created_user = $2 AND status IN ($5, $6)
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

	offerRows, err := tx.QueryContext(ctx, `
		SELECT member_id
		FROM hop_help_offers
		WHERE hop_id = $1 AND status IS NULL
		FOR UPDATE
	`, hopID)
	if err != nil {
		return fmt.Errorf("list pending hop offerers for cancel: %w", err)
	}
	var pendingOffererIDs []int64
	for offerRows.Next() {
		var memberID int64
		if scanErr := offerRows.Scan(&memberID); scanErr != nil {
			offerRows.Close()
			return fmt.Errorf("scan pending hop offerer for cancel: %w", scanErr)
		}
		pendingOffererIDs = append(pendingOffererIDs, memberID)
	}
	if err := offerRows.Err(); err != nil {
		offerRows.Close()
		return fmt.Errorf("list pending hop offerers for cancel: %w", err)
	}
	if err := offerRows.Close(); err != nil {
		return fmt.Errorf("close pending hop offerers for cancel: %w", err)
	}

	now := time.Now().UTC()
	if _, err = tx.ExecContext(ctx, `
		UPDATE hop_help_offers
		SET status = $1, denied_at = $2
		WHERE hop_id = $3 AND status IS NULL
	`, types.HopOfferStatusDenied, now, hopID); err != nil {
		return fmt.Errorf("cancel pending hop offers: %w", err)
	}
	cancelBody := fmt.Sprintf(
		"We wanted to let you know that %s has canceled their Hop titled, %s. Thanks anyway for the offer to help! Why not go check for some other Hops that need help?",
		cancelerName,
		title,
	)
	for _, offererID := range pendingOffererIDs {
		_ = createMemberNotification(
			ctx,
			tx,
			offererID,
			cancelBody,
			hopDetailsHref(orgID, hopID),
		)
	}

	if status == types.HopStatusAccepted && acceptedBy.Valid && acceptedBy.Int64 != cancelerID {
		_ = createMemberNotification(
			ctx,
			tx,
			acceptedBy.Int64,
			cancelBody,
			hopDetailsHref(orgID, hopID),
		)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit cancel hop: %w", err)
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

type CompleteHopResult struct {
	HopKind          string
	RequestedHours   int
	AwardedHours     int
	MinBalance       int
	MaxBalance       int
	PayerMinLimited  bool
	EarnerMaxLimited bool
}

func CompleteHop(ctx context.Context, db *sql.DB, p CompleteHopParams) error {
	_, err := CompleteHopWithResult(ctx, db, p)
	return err
}

func CompleteHopWithResult(ctx context.Context, db *sql.DB, p CompleteHopParams) (CompleteHopResult, error) {
	if db == nil {
		return CompleteHopResult{}, ErrNilDB
	}
	if p.OrganizationID == 0 {
		return CompleteHopResult{}, ErrMissingOrgID
	}
	if p.HopID == 0 {
		return CompleteHopResult{}, ErrHopNotFound
	}
	if p.CompletedBy == 0 {
		return CompleteHopResult{}, ErrMissingMemberID
	}
	comment := strings.TrimSpace(p.Comment)
	if comment == "" {
		return CompleteHopResult{}, ErrMissingField
	}

	if err := requireActiveMembership(ctx, db, p.OrganizationID, p.CompletedBy); err != nil {
		return CompleteHopResult{}, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return CompleteHopResult{}, fmt.Errorf("begin complete hop: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var createdBy int64
	var acceptedBy sql.NullInt64
	var hopKind string
	var estimatedHours int
	var status string
	var title string
	row := tx.QueryRowContext(ctx, `
		SELECT created_user, matched_user, hop_kind, estimated_hours, status, title
		FROM hops
		WHERE id = $1 AND organization_id = $2
		FOR UPDATE
	`, p.HopID, p.OrganizationID)
	if err = row.Scan(&createdBy, &acceptedBy, &hopKind, &estimatedHours, &status, &title); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CompleteHopResult{}, ErrHopNotFound
		}
		return CompleteHopResult{}, fmt.Errorf("load hop for completion: %w", err)
	}

	if status != types.HopStatusAccepted || !acceptedBy.Valid {
		return CompleteHopResult{}, ErrHopInvalidState
	}
	if p.CompletedBy != createdBy && p.CompletedBy != acceptedBy.Int64 {
		return CompleteHopResult{}, ErrHopForbidden
	}

	fromMemberID, toMemberID, err := hopTransferParticipants(hopKind, createdBy, acceptedBy.Int64)
	if err != nil {
		return CompleteHopResult{}, err
	}

	// The paying member can lower the final hours. The earning member cannot
	// increase them above the estimate.
	requestedHours := estimatedHours
	if p.CompletedBy == fromMemberID && p.CompletedHours > 0 {
		requestedHours = p.CompletedHours
	}
	if requestedHours <= 0 {
		return CompleteHopResult{}, ErrMissingField
	}
	result := CompleteHopResult{
		HopKind:        hopKind,
		RequestedHours: requestedHours,
	}
	policy, err := loadTimebankPolicy(ctx, tx, p.OrganizationID)
	if err != nil {
		return CompleteHopResult{}, err
	}
	result.MinBalance = policy.MinBalance
	result.MaxBalance = policy.MaxBalance

	// Serialize completions per member to prevent limit bypass when concurrent
	// hop completions update balances for the same members.
	if _, err = tx.ExecContext(ctx, `
		SELECT id
		FROM members
		WHERE id IN ($1, $2)
		FOR UPDATE
	`, createdBy, acceptedBy.Int64); err != nil {
		return CompleteHopResult{}, fmt.Errorf("lock members for completion: %w", err)
	}

	payerBalance, err := loadMemberBalanceHours(ctx, tx, p.OrganizationID, fromMemberID)
	if err != nil {
		return CompleteHopResult{}, err
	}
	earnerBalance, err := loadMemberBalanceHours(ctx, tx, p.OrganizationID, toMemberID)
	if err != nil {
		return CompleteHopResult{}, err
	}

	allowedByMin := payerBalance - policy.MinBalance
	allowedByMax := policy.MaxBalance - earnerBalance
	if allowedByMin < requestedHours {
		result.PayerMinLimited = true
	}
	if allowedByMax < requestedHours {
		result.EarnerMaxLimited = true
	}
	hours := requestedHours
	if allowedByMin < hours {
		hours = allowedByMin
	}
	if allowedByMax < hours {
		hours = allowedByMax
	}
	if hours < 0 {
		hours = 0
	}
	result.AwardedHours = hours

	if _, err = tx.ExecContext(ctx, `
		UPDATE hops
		SET status = $1, completed_by = $2, completed_at = NOW(), completed_hours = $3, completion_comment = $4, updated_at = NOW()
		WHERE id = $5 AND organization_id = $6 AND status = $7
	`, types.HopStatusCompleted, p.CompletedBy, hours, comment, p.HopID, p.OrganizationID, types.HopStatusAccepted); err != nil {
		return CompleteHopResult{}, fmt.Errorf("mark hop completed: %w", err)
	}

	if _, err = tx.ExecContext(ctx, `
		INSERT INTO hop_transactions (organization_id, hop_id, from_member_id, to_member_id, hours)
		VALUES ($1, $2, $3, $4, $5)
	`, p.OrganizationID, p.HopID, fromMemberID, toMemberID, hours); err != nil {
		return CompleteHopResult{}, fmt.Errorf("insert hop transaction: %w", err)
	}

	notifyMemberID := createdBy
	if p.CompletedBy == createdBy {
		notifyMemberID = acceptedBy.Int64
	}
	hopTitle := strings.TrimSpace(title)
	notificationText := "One of your in-progress hops was marked complete by the other member."
	if hopTitle != "" {
		notificationText = "Your in-progress hop was marked complete by the other member: " + hopTitle + "."
	}
	if notifyMemberID != 0 && notifyMemberID != p.CompletedBy {
		_ = createMemberNotification(
			ctx,
			tx,
			notifyMemberID,
			notificationText,
			hopDetailsHref(p.OrganizationID, p.HopID),
		)
	}

	if err = tx.Commit(); err != nil {
		return CompleteHopResult{}, fmt.Errorf("commit complete hop: %w", err)
	}
	return result, nil
}

func ExpireDueHops(ctx context.Context, db *sql.DB, now time.Time) (int64, error) {
	return expireHops(ctx, db, nil, now)
}

func ExpireHops(ctx context.Context, db *sql.DB, orgID int64, now time.Time) (int64, error) {
	if db == nil {
		return 0, ErrNilDB
	}
	if orgID == 0 {
		return 0, ErrMissingOrgID
	}

	return expireHops(ctx, db, &orgID, now)
}

func expireHops(ctx context.Context, db *sql.DB, orgID *int64, now time.Time) (int64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin expire hops: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var rows *sql.Rows
	if orgID != nil {
		rows, err = tx.QueryContext(ctx, `
			UPDATE hops
			SET status = $1, updated_at = NOW()
			WHERE organization_id = $2
				AND status IN ($3, $4)
				AND expires_at IS NOT NULL
				AND expires_at <= $5
			RETURNING id, organization_id, created_user, title
		`, types.HopStatusExpired, *orgID, types.HopStatusOpen, types.HopStatusAccepted, now)
	} else {
		rows, err = tx.QueryContext(ctx, `
			UPDATE hops
			SET status = $1, updated_at = NOW()
			WHERE status IN ($2, $3)
				AND expires_at IS NOT NULL
				AND expires_at <= $4
			RETURNING id, organization_id, created_user, title
		`, types.HopStatusExpired, types.HopStatusOpen, types.HopStatusAccepted, now)
	}
	if err != nil {
		return 0, fmt.Errorf("expire hops: %w", err)
	}
	defer rows.Close()

	type expiredHop struct {
		id             int64
		organizationID int64
		createdBy      int64
		title          string
	}

	var affected int64
	expired := make([]expiredHop, 0)
	for rows.Next() {
		var hopID int64
		var organizationID int64
		var createdBy int64
		var title string
		if err := rows.Scan(&hopID, &organizationID, &createdBy, &title); err != nil {
			return 0, fmt.Errorf("scan expired hop: %w", err)
		}
		affected++
		expired = append(expired, expiredHop{
			id:             hopID,
			organizationID: organizationID,
			createdBy:      createdBy,
			title:          title,
		})
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("expire hops rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close expired hops rows: %w", err)
	}

	for _, hop := range expired {
		hopTitle := strings.TrimSpace(hop.title)
		notificationText := "One of your pending hops expired."
		if hopTitle != "" {
			notificationText = "Your pending hop expired: " + hopTitle + "."
		}
		_ = createMemberNotification(
			ctx,
			tx,
			hop.createdBy,
			notificationText,
			hopDetailsHref(hop.organizationID, hop.id),
		)
	}

	if err = tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit expire hops: %w", err)
	}
	return affected, nil
}

func hopDetailsHref(orgID, hopID int64) string {
	if orgID <= 0 || hopID <= 0 {
		return myHopshareOrgHref(orgID)
	}
	return "/hops/view?org_id=" + strconv.FormatInt(orgID, 10) + "&hop_id=" + strconv.FormatInt(hopID, 10)
}

func AdminExpireHop(ctx context.Context, db *sql.DB, orgID, hopID int64) error {
	if db == nil {
		return ErrNilDB
	}
	if orgID == 0 {
		return ErrMissingOrgID
	}
	if hopID == 0 {
		return ErrHopNotFound
	}

	res, err := db.ExecContext(ctx, `
		UPDATE hops
		SET status = $1, updated_at = NOW()
		WHERE organization_id = $2
			AND id = $3
			AND status IN ($4, $5)
	`, types.HopStatusExpired, orgID, hopID, types.HopStatusOpen, types.HopStatusAccepted)
	if err != nil {
		return fmt.Errorf("admin expire hop: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("admin expire hop rows affected: %w", err)
	}
	if affected == 0 {
		var status string
		if err := db.QueryRowContext(ctx, `
			SELECT status
			FROM hops
			WHERE organization_id = $1 AND id = $2
		`, orgID, hopID).Scan(&status); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrHopNotFound
			}
			return fmt.Errorf("admin load hop status before expire: %w", err)
		}
		return ErrHopInvalidState
	}
	return nil
}

func AdminDeleteHop(ctx context.Context, db *sql.DB, orgID, hopID int64) error {
	if db == nil {
		return ErrNilDB
	}
	if orgID == 0 {
		return ErrMissingOrgID
	}
	if hopID == 0 {
		return ErrHopNotFound
	}

	res, err := db.ExecContext(ctx, `
		DELETE FROM hops
		WHERE organization_id = $1
			AND id = $2
			AND status IN ($3, $4)
	`, orgID, hopID, types.HopStatusOpen, types.HopStatusCanceled)
	if err != nil {
		return fmt.Errorf("admin delete hop: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("admin delete hop rows affected: %w", err)
	}
	if affected == 0 {
		var status string
		if err := db.QueryRowContext(ctx, `
			SELECT status
			FROM hops
			WHERE organization_id = $1 AND id = $2
		`, orgID, hopID).Scan(&status); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrHopNotFound
			}
			return fmt.Errorf("admin load hop status before delete: %w", err)
		}
		return ErrHopInvalidState
	}

	return nil
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
			r.id, r.organization_id, r.hop_kind, r.created_user, COALESCE(NULLIF(TRIM(CONCAT_WS(' ', mc.first_name, mc.last_name)), ''), mc.email),
			r.title, r.details, r.estimated_hours, r.is_private,
			r.when_kind, r.when_at, r.expires_at,
			r.status,
			r.matched_user, COALESCE(NULLIF(TRIM(CONCAT_WS(' ', ma.first_name, ma.last_name)), ''), ma.email), r.accepted_at,
			r.canceled_by, r.canceled_at,
			r.completed_by, r.completed_at, r.completed_hours, r.completion_comment,
			r.created_at, r.updated_at
		FROM hops r
		JOIN members mc ON mc.id = r.created_user
		LEFT JOIN members ma ON ma.id = r.matched_user
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

// HopOrganizationID returns the organization ID for the hop.
func HopOrganizationID(ctx context.Context, db *sql.DB, hopID int64) (int64, error) {
	if db == nil {
		return 0, ErrNilDB
	}
	if hopID == 0 {
		return 0, ErrHopNotFound
	}

	var orgID int64
	if err := db.QueryRowContext(ctx, `
		SELECT organization_id
		FROM hops
		WHERE id = $1
	`, hopID).Scan(&orgID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrHopNotFound
		}
		return 0, fmt.Errorf("get hop organization: %w", err)
	}
	return orgID, nil
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
			r.id, r.organization_id, r.hop_kind, r.created_user, COALESCE(NULLIF(TRIM(CONCAT_WS(' ', mc.first_name, mc.last_name)), ''), mc.email),
			r.title, r.details, r.estimated_hours, r.is_private,
			r.when_kind, r.when_at, r.expires_at,
			r.status,
			r.matched_user, COALESCE(NULLIF(TRIM(CONCAT_WS(' ', ma.first_name, ma.last_name)), ''), ma.email), r.accepted_at,
			r.canceled_by, r.canceled_at,
			r.completed_by, r.completed_at, r.completed_hours, r.completion_comment,
			r.created_at, r.updated_at
		FROM hops r
		JOIN members mc ON mc.id = r.created_user
		LEFT JOIN members ma ON ma.id = r.matched_user
		WHERE r.organization_id = $1
			AND (
				r.created_user = $2
				OR r.matched_user = $2
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

func ListRequestedHops(ctx context.Context, db *sql.DB, orgID, memberID int64) ([]types.Hop, error) {
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
			r.id, r.organization_id, r.hop_kind, r.created_user, COALESCE(NULLIF(TRIM(CONCAT_WS(' ', mc.first_name, mc.last_name)), ''), mc.email),
			r.title, r.details, r.estimated_hours, r.is_private,
			r.when_kind, r.when_at, r.expires_at,
			r.status,
			r.matched_user, COALESCE(NULLIF(TRIM(CONCAT_WS(' ', ma.first_name, ma.last_name)), ''), ma.email), r.accepted_at,
			r.canceled_by, r.canceled_at,
			r.completed_by, r.completed_at, r.completed_hours, r.completion_comment,
			r.created_at, r.updated_at
		FROM hops r
		JOIN members mc ON mc.id = r.created_user
		LEFT JOIN members ma ON ma.id = r.matched_user
		WHERE r.organization_id = $1
			AND r.created_user = $2
		ORDER BY r.created_at DESC
	`, orgID, memberID)
	if err != nil {
		return nil, fmt.Errorf("list requested hops: %w", err)
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
		return nil, fmt.Errorf("list requested hops: %w", err)
	}
	return out, nil
}

func ListHelpedHops(ctx context.Context, db *sql.DB, orgID, memberID int64) ([]types.Hop, error) {
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
			r.id, r.organization_id, r.hop_kind, r.created_user, COALESCE(NULLIF(TRIM(CONCAT_WS(' ', mc.first_name, mc.last_name)), ''), mc.email),
			r.title, r.details, r.estimated_hours, r.is_private,
			r.when_kind, r.when_at, r.expires_at,
			r.status,
			r.matched_user, COALESCE(NULLIF(TRIM(CONCAT_WS(' ', ma.first_name, ma.last_name)), ''), ma.email), r.accepted_at,
			r.canceled_by, r.canceled_at,
			r.completed_by, r.completed_at, r.completed_hours, r.completion_comment,
			r.created_at, r.updated_at
		FROM hops r
		JOIN members mc ON mc.id = r.created_user
		LEFT JOIN members ma ON ma.id = r.matched_user
		WHERE r.organization_id = $1
			AND r.matched_user = $2
			AND r.status IN ($3, $4)
		ORDER BY r.created_at DESC
	`, orgID, memberID, types.HopStatusAccepted, types.HopStatusCompleted)
	if err != nil {
		return nil, fmt.Errorf("list helped hops: %w", err)
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
		return nil, fmt.Errorf("list helped hops: %w", err)
	}
	return out, nil
}

func ListPendingOfferedHops(ctx context.Context, db *sql.DB, orgID, memberID int64) ([]types.Hop, error) {
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
			r.id, r.organization_id, r.hop_kind, r.created_user, COALESCE(NULLIF(TRIM(CONCAT_WS(' ', mc.first_name, mc.last_name)), ''), mc.email),
			r.title, r.details, r.estimated_hours, r.is_private,
			r.when_kind, r.when_at, r.expires_at,
			r.status,
			r.matched_user, COALESCE(NULLIF(TRIM(CONCAT_WS(' ', ma.first_name, ma.last_name)), ''), ma.email), r.accepted_at,
			r.canceled_by, r.canceled_at,
			r.completed_by, r.completed_at, r.completed_hours, r.completion_comment,
			r.created_at, r.updated_at
		FROM hops r
		JOIN members mc ON mc.id = r.created_user
		LEFT JOIN members ma ON ma.id = r.matched_user
		WHERE r.organization_id = $1
			AND r.status = $2
			AND EXISTS (
				SELECT 1
				FROM hop_help_offers hho
				WHERE hho.hop_id = r.id AND hho.member_id = $3 AND hho.status IS NULL
			)
		ORDER BY r.created_at DESC
	`, orgID, types.HopStatusOpen, memberID)
	if err != nil {
		return nil, fmt.Errorf("list pending offered hops: %w", err)
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
		return nil, fmt.Errorf("list pending offered hops: %w", err)
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
			r.id, r.organization_id, r.hop_kind, r.created_user, COALESCE(NULLIF(TRIM(CONCAT_WS(' ', mc.first_name, mc.last_name)), ''), mc.email),
			r.title, r.details, r.estimated_hours, r.is_private,
			r.when_kind, r.when_at, r.expires_at,
			r.status,
			r.matched_user, COALESCE(NULLIF(TRIM(CONCAT_WS(' ', ma.first_name, ma.last_name)), ''), ma.email), r.accepted_at,
			r.canceled_by, r.canceled_at,
			r.completed_by, r.completed_at, r.completed_hours, r.completion_comment,
			r.created_at, r.updated_at
		FROM hops r
		JOIN members mc ON mc.id = r.created_user
		LEFT JOIN members ma ON ma.id = r.matched_user
		WHERE r.organization_id = $1
			AND (
				(r.status = $2 AND r.created_user <> $3)
				OR (r.status = $4 AND r.matched_user = $3)
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

// RecentPendingHops returns recent open hops for an organization.
func RecentPendingHops(ctx context.Context, db *sql.DB, orgID int64, limit int) ([]types.Hop, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if orgID == 0 {
		return nil, ErrMissingOrgID
	}
	if limit <= 0 {
		limit = 100
	}

	rows, err := db.QueryContext(ctx, `
		SELECT
			r.id, r.organization_id, r.hop_kind, r.created_user, COALESCE(NULLIF(TRIM(CONCAT_WS(' ', mc.first_name, mc.last_name)), ''), mc.email),
			r.title, r.details, r.estimated_hours, r.is_private,
			r.when_kind, r.when_at, r.expires_at,
			r.status,
			r.matched_user, COALESCE(NULLIF(TRIM(CONCAT_WS(' ', ma.first_name, ma.last_name)), ''), ma.email), r.accepted_at,
			r.canceled_by, r.canceled_at,
			r.completed_by, r.completed_at, r.completed_hours, r.completion_comment,
			r.created_at, r.updated_at
		FROM hops r
		JOIN members mc ON mc.id = r.created_user
		LEFT JOIN members ma ON ma.id = r.matched_user
		WHERE r.organization_id = $1
			AND r.status = $2
		ORDER BY r.created_at DESC
		LIMIT $3
	`, orgID, types.HopStatusOpen, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent pending hops: %w", err)
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
		return nil, fmt.Errorf("list recent pending hops: %w", err)
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
			r.id, r.organization_id, r.hop_kind, r.created_user, COALESCE(NULLIF(TRIM(CONCAT_WS(' ', mc.first_name, mc.last_name)), ''), mc.email),
			r.title, r.details, r.estimated_hours, r.is_private,
			r.when_kind, r.when_at, r.expires_at,
			r.status,
			r.matched_user, COALESCE(NULLIF(TRIM(CONCAT_WS(' ', ma.first_name, ma.last_name)), ''), ma.email), r.accepted_at,
			r.canceled_by, r.canceled_at,
			r.completed_by, r.completed_at, r.completed_hours, r.completion_comment,
			r.created_at, r.updated_at
		FROM hops r
		JOIN members mc ON mc.id = r.created_user
		LEFT JOIN members ma ON ma.id = r.matched_user
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

// RecentAcceptedHops returns recent accepted hops for an organization.
func RecentAcceptedHops(ctx context.Context, db *sql.DB, orgID int64, limit int) ([]types.Hop, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if orgID == 0 {
		return nil, ErrMissingOrgID
	}
	if limit <= 0 {
		limit = 25
	}

	rows, err := db.QueryContext(ctx, `
		SELECT
			r.id, r.organization_id, r.hop_kind, r.created_user, COALESCE(NULLIF(TRIM(CONCAT_WS(' ', mc.first_name, mc.last_name)), ''), mc.email),
			r.title, r.details, r.estimated_hours, r.is_private,
			r.when_kind, r.when_at, r.expires_at,
			r.status,
			r.matched_user, COALESCE(NULLIF(TRIM(CONCAT_WS(' ', ma.first_name, ma.last_name)), ''), ma.email), r.accepted_at,
			r.canceled_by, r.canceled_at,
			r.completed_by, r.completed_at, r.completed_hours, r.completion_comment,
			r.created_at, r.updated_at
		FROM hops r
		JOIN members mc ON mc.id = r.created_user
		LEFT JOIN members ma ON ma.id = r.matched_user
		WHERE r.organization_id = $1
			AND r.status = $2
		ORDER BY r.accepted_at DESC
		LIMIT $3
	`, orgID, types.HopStatusAccepted, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent accepted hops: %w", err)
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
		return nil, fmt.Errorf("list recent accepted hops: %w", err)
	}
	return out, nil
}

// RecentPublicCompletedHops returns recent completed public hops for an organization.
func RecentPublicCompletedHops(ctx context.Context, db *sql.DB, orgID int64, limit int) ([]types.Hop, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if orgID == 0 {
		return nil, ErrMissingOrgID
	}
	if limit <= 0 {
		limit = 25
	}

	rows, err := db.QueryContext(ctx, `
		SELECT
			r.id, r.organization_id, r.hop_kind, r.created_user, COALESCE(NULLIF(TRIM(CONCAT_WS(' ', mc.first_name, mc.last_name)), ''), mc.email),
			r.title, r.details, r.estimated_hours, r.is_private,
			r.when_kind, r.when_at, r.expires_at,
			r.status,
			r.matched_user, COALESCE(NULLIF(TRIM(CONCAT_WS(' ', ma.first_name, ma.last_name)), ''), ma.email), r.accepted_at,
			r.canceled_by, r.canceled_at,
			r.completed_by, r.completed_at, r.completed_hours, r.completion_comment,
			r.created_at, r.updated_at
		FROM hops r
		JOIN members mc ON mc.id = r.created_user
		LEFT JOIN members ma ON ma.id = r.matched_user
		WHERE r.organization_id = $1
			AND r.status = $2
			AND r.is_private = FALSE
		ORDER BY r.completed_at DESC
		LIMIT $3
	`, orgID, types.HopStatusCompleted, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent public completed hops: %w", err)
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
		return nil, fmt.Errorf("list recent public completed hops: %w", err)
	}
	return out, nil
}

// RecentPublicAcceptedHops returns recent accepted public hops for an organization.
func RecentPublicAcceptedHops(ctx context.Context, db *sql.DB, orgID int64, limit int) ([]types.Hop, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if orgID == 0 {
		return nil, ErrMissingOrgID
	}
	if limit <= 0 {
		limit = 25
	}

	rows, err := db.QueryContext(ctx, `
		SELECT
			r.id, r.organization_id, r.hop_kind, r.created_user, COALESCE(NULLIF(TRIM(CONCAT_WS(' ', mc.first_name, mc.last_name)), ''), mc.email),
			r.title, r.details, r.estimated_hours, r.is_private,
			r.when_kind, r.when_at, r.expires_at,
			r.status,
			r.matched_user, COALESCE(NULLIF(TRIM(CONCAT_WS(' ', ma.first_name, ma.last_name)), ''), ma.email), r.accepted_at,
			r.canceled_by, r.canceled_at,
			r.completed_by, r.completed_at, r.completed_hours, r.completion_comment,
			r.created_at, r.updated_at
		FROM hops r
		JOIN members mc ON mc.id = r.created_user
		LEFT JOIN members ma ON ma.id = r.matched_user
		WHERE r.organization_id = $1
			AND r.status = $2
			AND r.is_private = FALSE
		ORDER BY r.accepted_at DESC
		LIMIT $3
	`, orgID, types.HopStatusAccepted, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent public accepted hops: %w", err)
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
		return nil, fmt.Errorf("list recent public accepted hops: %w", err)
	}
	return out, nil
}

// RecentPublicPendingHops returns recent open public hops for an organization.
func RecentPublicPendingHops(ctx context.Context, db *sql.DB, orgID int64, limit int) ([]types.Hop, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if orgID == 0 {
		return nil, ErrMissingOrgID
	}
	if limit <= 0 {
		limit = 100
	}

	rows, err := db.QueryContext(ctx, `
		SELECT
			r.id, r.organization_id, r.hop_kind, r.created_user, COALESCE(NULLIF(TRIM(CONCAT_WS(' ', mc.first_name, mc.last_name)), ''), mc.email),
			r.title, r.details, r.estimated_hours, r.is_private,
			r.when_kind, r.when_at, r.expires_at,
			r.status,
			r.matched_user, COALESCE(NULLIF(TRIM(CONCAT_WS(' ', ma.first_name, ma.last_name)), ''), ma.email), r.accepted_at,
			r.canceled_by, r.canceled_at,
			r.completed_by, r.completed_at, r.completed_hours, r.completion_comment,
			r.created_at, r.updated_at
		FROM hops r
		JOIN members mc ON mc.id = r.created_user
		LEFT JOIN members ma ON ma.id = r.matched_user
		WHERE r.organization_id = $1
			AND r.status = $2
			AND r.is_private = FALSE
		ORDER BY r.created_at DESC
		LIMIT $3
	`, orgID, types.HopStatusOpen, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent public pending hops: %w", err)
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
		return nil, fmt.Errorf("list recent public pending hops: %w", err)
	}
	return out, nil
}

// MarkLeftOrganizationParticipants annotates hops with whether creator/helper left the organization.
func MarkLeftOrganizationParticipants(ctx context.Context, db *sql.DB, orgID int64, hops []types.Hop) ([]types.Hop, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if orgID == 0 {
		return nil, ErrMissingOrgID
	}
	if len(hops) == 0 {
		return hops, nil
	}

	memberSet := make(map[int64]struct{}, len(hops)*2)
	for _, hop := range hops {
		if hop.CreatedBy != 0 {
			memberSet[hop.CreatedBy] = struct{}{}
		}
		if hop.AcceptedBy != nil && *hop.AcceptedBy != 0 {
			memberSet[*hop.AcceptedBy] = struct{}{}
		}
	}
	if len(memberSet) == 0 {
		return hops, nil
	}

	memberIDs := make([]int64, 0, len(memberSet))
	for memberID := range memberSet {
		memberIDs = append(memberIDs, memberID)
	}

	rows, err := db.QueryContext(ctx, `
		SELECT member_id
		FROM organization_memberships
		WHERE organization_id = $1
			AND left_at IS NULL
			AND member_id = ANY($2)
	`, orgID, pq.Array(memberIDs))
	if err != nil {
		return nil, fmt.Errorf("list active members for hop annotation: %w", err)
	}
	defer rows.Close()

	activeMembers := make(map[int64]struct{}, len(memberIDs))
	for rows.Next() {
		var memberID int64
		if err := rows.Scan(&memberID); err != nil {
			return nil, fmt.Errorf("scan active member for hop annotation: %w", err)
		}
		activeMembers[memberID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list active members for hop annotation: %w", err)
	}

	for i := range hops {
		if hops[i].CreatedBy != 0 {
			if _, ok := activeMembers[hops[i].CreatedBy]; !ok {
				hops[i].CreatedByLeftOrganization = true
			}
		}
		if hops[i].AcceptedBy != nil && *hops[i].AcceptedBy != 0 {
			if _, ok := activeMembers[*hops[i].AcceptedBy]; !ok {
				hops[i].AcceptedByLeftOrganization = true
			}
		}
	}

	return hops, nil
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

	if err := db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(hours), 0)
		FROM hop_transactions
		WHERE organization_id = $1
	`, orgID).Scan(&m.TotalHoursExchanged); err != nil {
		return types.OrgHopMetrics{}, fmt.Errorf("sum exchanged hours: %w", err)
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
	balance, err := loadMemberBalanceHours(ctx, db, orgID, memberID)
	if err != nil {
		return types.MemberHopStats{}, err
	}
	stats.BalanceHours = balance

	var lastMade sql.NullTime
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*), MAX(created_at)
		FROM hops
		WHERE organization_id = $1 AND created_user = $2
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
		WHERE organization_id = $1 AND matched_user = $2 AND status = $3
	`, orgID, memberID, types.HopStatusCompleted).Scan(&stats.HopsFulfilled, &lastFulfilled); err != nil {
		return types.MemberHopStats{}, fmt.Errorf("load hops fulfilled: %w", err)
	}
	if lastFulfilled.Valid {
		stats.LastHopFulfilledAt = &lastFulfilled.Time
	}

	return stats, nil
}

type memberBalanceLedgerRow struct {
	OccurredAt     time.Time
	Description    string
	HoursExchanged int
	BalanceDelta   int
	SourceRank     int
	SourceID       int64
}

// ListMemberBalanceTransactions returns the active member's balance history for
// one organization, newest first, with the running balance after each entry.
func ListMemberBalanceTransactions(ctx context.Context, db *sql.DB, orgID, memberID int64) ([]types.MemberBalanceTransaction, error) {
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

	rows := make([]memberBalanceLedgerRow, 0)

	adjustments, err := db.QueryContext(ctx, `
		SELECT id, created_at, hours_delta, reason, is_starting_balance
		FROM hour_balance_adjustments
		WHERE organization_id = $1 AND member_id = $2
	`, orgID, memberID)
	if err != nil {
		return nil, fmt.Errorf("list balance adjustments: %w", err)
	}
	defer adjustments.Close()

	for adjustments.Next() {
		var (
			id                int64
			occurredAt        time.Time
			hoursDelta        int
			reason            string
			isStartingBalance bool
		)
		if err := adjustments.Scan(&id, &occurredAt, &hoursDelta, &reason, &isStartingBalance); err != nil {
			return nil, fmt.Errorf("scan balance adjustment: %w", err)
		}

		description := "Adjustment: " + strings.TrimSpace(reason)
		if isStartingBalance {
			description = "Initial balance"
		}

		rows = append(rows, memberBalanceLedgerRow{
			OccurredAt:     occurredAt,
			Description:    description,
			HoursExchanged: hoursDelta,
			BalanceDelta:   hoursDelta,
			SourceRank:     0,
			SourceID:       id,
		})
	}
	if err := adjustments.Err(); err != nil {
		return nil, fmt.Errorf("list balance adjustments: %w", err)
	}

	transactions, err := db.QueryContext(ctx, `
		SELECT
			ht.id,
			ht.created_at,
			ht.hours,
			ht.from_member_id,
			ht.to_member_id,
			from_member.first_name,
			from_member.last_name,
			from_member.email,
			to_member.first_name,
			to_member.last_name,
			to_member.email
		FROM hop_transactions ht
		JOIN members from_member ON from_member.id = ht.from_member_id
		JOIN members to_member ON to_member.id = ht.to_member_id
		WHERE ht.organization_id = $1 AND (ht.from_member_id = $2 OR ht.to_member_id = $2)
	`, orgID, memberID)
	if err != nil {
		return nil, fmt.Errorf("list hop transactions: %w", err)
	}
	defer transactions.Close()

	for transactions.Next() {
		var (
			id            int64
			occurredAt    time.Time
			hours         int
			fromMemberID  int64
			toMemberID    int64
			fromFirstName string
			fromLastName  string
			fromEmail     string
			toFirstName   string
			toLastName    string
			toEmail       string
		)
		if err := transactions.Scan(
			&id,
			&occurredAt,
			&hours,
			&fromMemberID,
			&toMemberID,
			&fromFirstName,
			&fromLastName,
			&fromEmail,
			&toFirstName,
			&toLastName,
			&toEmail,
		); err != nil {
			return nil, fmt.Errorf("scan hop transaction: %w", err)
		}

		description := ""
		balanceDelta := -hours
		switch {
		case toMemberID == memberID:
			description = "You helped " + memberDisplayName(fromFirstName, fromLastName, fromEmail)
			balanceDelta = hours
		case fromMemberID == memberID:
			description = memberDisplayName(toFirstName, toLastName, toEmail) + " helped You"
		default:
			continue
		}

		rows = append(rows, memberBalanceLedgerRow{
			OccurredAt:     occurredAt,
			Description:    description,
			HoursExchanged: balanceDelta,
			BalanceDelta:   balanceDelta,
			SourceRank:     1,
			SourceID:       id,
		})
	}
	if err := transactions.Err(); err != nil {
		return nil, fmt.Errorf("list hop transactions: %w", err)
	}

	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].OccurredAt.Equal(rows[j].OccurredAt) {
			return rows[i].OccurredAt.Before(rows[j].OccurredAt)
		}
		if rows[i].SourceRank != rows[j].SourceRank {
			return rows[i].SourceRank < rows[j].SourceRank
		}
		return rows[i].SourceID < rows[j].SourceID
	})

	ledger := make([]types.MemberBalanceTransaction, 0, len(rows))
	balance := 0
	for _, row := range rows {
		balance += row.BalanceDelta
		ledger = append(ledger, types.MemberBalanceTransaction{
			OccurredAt:      row.OccurredAt,
			Description:     row.Description,
			HoursExchanged:  row.HoursExchanged,
			BalanceAfterTxn: balance,
		})
	}

	for i, j := 0, len(ledger)-1; i < j; i, j = i+1, j-1 {
		ledger[i], ledger[j] = ledger[j], ledger[i]
	}

	return ledger, nil
}

func ReopenAcceptedHopsForMember(ctx context.Context, tx *sql.Tx, memberID int64) ([]ReopenedAcceptedHop, error) {
	if tx == nil {
		return nil, ErrNilDB
	}
	if memberID == 0 {
		return nil, ErrMissingMemberID
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT id, organization_id, title, created_user, matched_user
		FROM hops
		WHERE status = $1
			AND matched_user IS NOT NULL
			AND (created_user = $2 OR matched_user = $2)
		FOR UPDATE
	`, types.HopStatusAccepted, memberID)
	if err != nil {
		return nil, fmt.Errorf("list accepted hops for member reopen: %w", err)
	}
	defer rows.Close()

	reopened := make([]ReopenedAcceptedHop, 0)
	for rows.Next() {
		var hop ReopenedAcceptedHop
		if err := rows.Scan(&hop.ID, &hop.OrganizationID, &hop.Title, &hop.CreatedBy, &hop.AcceptedBy); err != nil {
			return nil, fmt.Errorf("scan accepted hop for member reopen: %w", err)
		}
		reopened = append(reopened, hop)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list accepted hops for member reopen: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close accepted hops for member reopen: %w", err)
	}

	for _, hop := range reopened {
		if _, err := tx.ExecContext(ctx, `
			UPDATE hops
			SET status = $1,
				matched_user = NULL,
				accepted_at = NULL,
				updated_at = NOW()
			WHERE id = $2
			  AND status = $3
		`, types.HopStatusOpen, hop.ID, types.HopStatusAccepted); err != nil {
			return nil, fmt.Errorf("reopen accepted hop %d for member disable: %w", hop.ID, err)
		}
	}

	return reopened, nil
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

func ListPendingHopOffers(ctx context.Context, db *sql.DB, hopID, requesterID int64) ([]types.PendingHopOffer, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if hopID == 0 {
		return nil, ErrHopNotFound
	}
	if requesterID == 0 {
		return nil, ErrMissingMemberID
	}

	rows, err := db.QueryContext(ctx, `
		SELECT
			hho.member_id,
			COALESCE(NULLIF(TRIM(CONCAT_WS(' ', m.first_name, m.last_name)), ''), m.email),
			hho.offered_at
		FROM hops h
		JOIN hop_help_offers hho ON hho.hop_id = h.id
		JOIN members m ON m.id = hho.member_id
		WHERE h.id = $1
		  AND h.created_user = $2
		  AND h.status = $3
		  AND hho.status IS NULL
		ORDER BY hho.offered_at ASC, hho.member_id ASC
	`, hopID, requesterID, types.HopStatusOpen)
	if err != nil {
		return nil, fmt.Errorf("list pending hop offers for hop: %w", err)
	}
	defer rows.Close()

	var offers []types.PendingHopOffer
	for rows.Next() {
		var offer types.PendingHopOffer
		if err := rows.Scan(&offer.MemberID, &offer.MemberName, &offer.OfferedAt); err != nil {
			return nil, fmt.Errorf("scan pending hop offer for hop: %w", err)
		}
		offers = append(offers, offer)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list pending hop offers for hop: %w", err)
	}
	return offers, nil
}

func HasPendingHopOffer(ctx context.Context, db *sql.DB, hopID, memberID int64) (bool, error) {
	if db == nil {
		return false, ErrNilDB
	}
	if hopID == 0 {
		return false, ErrHopNotFound
	}
	if memberID == 0 {
		return false, ErrMissingMemberID
	}

	var exists bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM hop_help_offers
			WHERE hop_id = $1 AND member_id = $2 AND status IS NULL
		)
	`, hopID, memberID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check pending hop offer: %w", err)
	}
	return exists, nil
}

// HopImageData includes the blob and related hop access fields.
type HopImageData struct {
	ID             int64
	HopID          int64
	OrganizationID int64
	IsPrivate      bool
	CreatedBy      int64
	AcceptedBy     *int64
	ContentType    string
	Data           []byte
}

func SetHopPrivacy(ctx context.Context, db *sql.DB, orgID, hopID, memberID int64, isPrivate bool) error {
	if db == nil {
		return ErrNilDB
	}
	if orgID == 0 {
		return ErrMissingOrgID
	}
	if hopID == 0 {
		return ErrHopNotFound
	}
	if memberID == 0 {
		return ErrMissingMemberID
	}

	if err := requireActiveMembership(ctx, db, orgID, memberID); err != nil {
		return err
	}

	res, err := db.ExecContext(ctx, `
		UPDATE hops
		SET is_private = $1, updated_at = NOW()
		WHERE id = $2 AND organization_id = $3 AND (created_user = $4 OR matched_user = $4)
	`, isPrivate, hopID, orgID, memberID)
	if err != nil {
		return fmt.Errorf("update hop privacy: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update hop privacy rows affected: %w", err)
	}
	if affected == 0 {
		var exists bool
		if err := db.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM hops WHERE id = $1 AND organization_id = $2
			)
		`, hopID, orgID).Scan(&exists); err != nil {
			return fmt.Errorf("check hop privacy hop: %w", err)
		}
		if !exists {
			return ErrHopNotFound
		}
		return ErrHopForbidden
	}
	return nil
}

func ListHopComments(ctx context.Context, db *sql.DB, hopID int64) ([]types.HopComment, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if hopID == 0 {
		return nil, ErrHopNotFound
	}

	rows, err := db.QueryContext(ctx, `
		SELECT
			c.id, c.hop_id, c.member_id,
			COALESCE(NULLIF(TRIM(CONCAT_WS(' ', m.first_name, m.last_name)), ''), m.email),
			c.body, c.private_to_member_id, c.created_at
		FROM hop_comments c
		JOIN members m ON m.id = c.member_id
		WHERE c.hop_id = $1
		ORDER BY c.created_at DESC
	`, hopID)
	if err != nil {
		return nil, fmt.Errorf("list hop comments: %w", err)
	}
	defer rows.Close()

	var out []types.HopComment
	for rows.Next() {
		var c types.HopComment
		var privateToMemberID sql.NullInt64
		if err := rows.Scan(&c.ID, &c.HopID, &c.MemberID, &c.MemberName, &c.Body, &privateToMemberID, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan hop comment: %w", err)
		}
		if privateToMemberID.Valid {
			v := privateToMemberID.Int64
			c.PrivateToMemberID = &v
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list hop comments: %w", err)
	}
	return out, nil
}

func ListVisibleHopComments(ctx context.Context, db *sql.DB, hopID, viewerMemberID int64) ([]types.HopComment, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if hopID == 0 {
		return nil, ErrHopNotFound
	}
	if viewerMemberID == 0 {
		return nil, ErrMissingMemberID
	}

	rows, err := db.QueryContext(ctx, `
		SELECT
			c.id, c.hop_id, c.member_id,
			COALESCE(NULLIF(TRIM(CONCAT_WS(' ', m.first_name, m.last_name)), ''), m.email),
			c.body, c.private_to_member_id, c.created_at
		FROM hop_comments c
		JOIN members m ON m.id = c.member_id
		WHERE c.hop_id = $1
			AND (c.private_to_member_id IS NULL OR c.member_id = $2 OR c.private_to_member_id = $2)
		ORDER BY c.created_at DESC
	`, hopID, viewerMemberID)
	if err != nil {
		return nil, fmt.Errorf("list visible hop comments: %w", err)
	}
	defer rows.Close()

	var out []types.HopComment
	for rows.Next() {
		var c types.HopComment
		var privateToMemberID sql.NullInt64
		if err := rows.Scan(&c.ID, &c.HopID, &c.MemberID, &c.MemberName, &c.Body, &privateToMemberID, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan visible hop comment: %w", err)
		}
		if privateToMemberID.Valid {
			v := privateToMemberID.Int64
			c.PrivateToMemberID = &v
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list visible hop comments: %w", err)
	}
	return out, nil
}

type AddHopCommentParams struct {
	HopID             int64
	MemberID          int64
	Body              string
	PrivateToMemberID *int64
}

func AddHopComment(ctx context.Context, db *sql.DB, p AddHopCommentParams) error {
	if db == nil {
		return ErrNilDB
	}
	if p.HopID == 0 {
		return ErrHopNotFound
	}
	if p.MemberID == 0 {
		return ErrMissingMemberID
	}
	body := strings.TrimSpace(p.Body)
	if body == "" {
		return ErrMissingField
	}

	privateToMemberID, err := normalizeHopCommentAudience(ctx, db, p.HopID, p.MemberID, p.PrivateToMemberID)
	if err != nil {
		return err
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO hop_comments (hop_id, member_id, body, private_to_member_id)
		VALUES ($1, $2, $3, $4)
	`, p.HopID, p.MemberID, body, nullableInt64(privateToMemberID)); err != nil {
		return fmt.Errorf("create hop comment: %w", err)
	}
	return nil
}

func HopCommentVisibleToMember(ctx context.Context, db *sql.DB, hopID, commentID, viewerMemberID int64) (bool, error) {
	if db == nil {
		return false, ErrNilDB
	}
	if hopID == 0 || commentID == 0 {
		return false, ErrHopNotFound
	}
	if viewerMemberID == 0 {
		return false, ErrMissingMemberID
	}

	var visible bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM hop_comments
			WHERE id = $1
				AND hop_id = $2
				AND (private_to_member_id IS NULL OR member_id = $3 OR private_to_member_id = $3)
		)
	`, commentID, hopID, viewerMemberID).Scan(&visible); err != nil {
		return false, fmt.Errorf("check hop comment visibility: %w", err)
	}
	return visible, nil
}

func normalizeHopCommentAudience(ctx context.Context, db *sql.DB, hopID, memberID int64, privateToMemberID *int64) (*int64, error) {
	if privateToMemberID == nil {
		return nil, nil
	}
	if *privateToMemberID == 0 || *privateToMemberID == memberID {
		return nil, ErrHopForbidden
	}

	var createdBy int64
	var acceptedBy sql.NullInt64
	if err := db.QueryRowContext(ctx, `
		SELECT created_user, matched_user
		FROM hops
		WHERE id = $1
	`, hopID).Scan(&createdBy, &acceptedBy); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrHopNotFound
		}
		return nil, fmt.Errorf("load hop for comment audience: %w", err)
	}
	if !acceptedBy.Valid {
		return nil, ErrHopInvalidState
	}

	requesterID := createdBy
	helperID := acceptedBy.Int64
	validPair := (memberID == requesterID && *privateToMemberID == helperID) || (memberID == helperID && *privateToMemberID == requesterID)
	if !validPair {
		return nil, ErrHopForbidden
	}

	return privateToMemberID, nil
}

func ListHopImages(ctx context.Context, db *sql.DB, hopID int64) ([]types.HopImage, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if hopID == 0 {
		return nil, ErrHopNotFound
	}

	rows, err := db.QueryContext(ctx, `
		SELECT id, hop_id, member_id, created_at
		FROM hop_images
		WHERE hop_id = $1
		ORDER BY created_at DESC
	`, hopID)
	if err != nil {
		return nil, fmt.Errorf("list hop images: %w", err)
	}
	defer rows.Close()

	var out []types.HopImage
	for rows.Next() {
		var img types.HopImage
		if err := rows.Scan(&img.ID, &img.HopID, &img.MemberID, &img.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan hop image: %w", err)
		}
		out = append(out, img)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list hop images: %w", err)
	}
	return out, nil
}

func AddHopImage(ctx context.Context, db *sql.DB, hopID, memberID int64, contentType string, data []byte) error {
	if db == nil {
		return ErrNilDB
	}
	if hopID == 0 {
		return ErrHopNotFound
	}
	if memberID == 0 {
		return ErrMissingMemberID
	}
	contentType = strings.TrimSpace(contentType)
	if contentType == "" || len(data) == 0 {
		return ErrMissingField
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO hop_images (hop_id, member_id, content_type, data)
		VALUES ($1, $2, $3, $4)
	`, hopID, memberID, contentType, data); err != nil {
		return fmt.Errorf("create hop image: %w", err)
	}
	return nil
}

func DeleteHopImage(ctx context.Context, db *sql.DB, hopID, imageID int64) error {
	if db == nil {
		return ErrNilDB
	}
	if hopID == 0 {
		return ErrHopNotFound
	}
	if imageID == 0 {
		return ErrHopImageNotFound
	}

	res, err := db.ExecContext(ctx, `
		DELETE FROM hop_images
		WHERE id = $1 AND hop_id = $2
	`, imageID, hopID)
	if err != nil {
		return fmt.Errorf("delete hop image: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete hop image rows affected: %w", err)
	}
	if affected == 0 {
		return ErrHopImageNotFound
	}
	return nil
}

func GetHopImageData(ctx context.Context, db *sql.DB, imageID int64) (HopImageData, error) {
	if db == nil {
		return HopImageData{}, ErrNilDB
	}
	if imageID == 0 {
		return HopImageData{}, ErrHopImageNotFound
	}

	var img HopImageData
	var acceptedBy sql.NullInt64
	if err := db.QueryRowContext(ctx, `
		SELECT
			hi.id, hi.hop_id, h.organization_id, h.is_private,
			h.created_user, h.matched_user, hi.content_type, hi.data
		FROM hop_images hi
		JOIN hops h ON h.id = hi.hop_id
		WHERE hi.id = $1
	`, imageID).Scan(
		&img.ID, &img.HopID, &img.OrganizationID, &img.IsPrivate,
		&img.CreatedBy, &acceptedBy, &img.ContentType, &img.Data,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return HopImageData{}, ErrHopImageNotFound
		}
		return HopImageData{}, fmt.Errorf("load hop image: %w", err)
	}
	if acceptedBy.Valid {
		v := acceptedBy.Int64
		img.AcceptedBy = &v
	}
	return img, nil
}

func loadMemberBalanceHours(ctx context.Context, q queryer, orgID, memberID int64) (int, error) {
	if orgID == 0 {
		return 0, ErrMissingOrgID
	}
	if memberID == 0 {
		return 0, ErrMissingMemberID
	}

	var balance int
	if err := q.QueryRowContext(ctx, `
		SELECT
			COALESCE((
				SELECT
					COALESCE(SUM(CASE WHEN ht.to_member_id = $2 THEN ht.hours ELSE 0 END), 0) -
					COALESCE(SUM(CASE WHEN ht.from_member_id = $2 THEN ht.hours ELSE 0 END), 0)
				FROM hop_transactions ht
				WHERE ht.organization_id = $1 AND (ht.to_member_id = $2 OR ht.from_member_id = $2)
			), 0)
			+
			COALESCE((
				SELECT COALESCE(SUM(hba.hours_delta), 0)
				FROM hour_balance_adjustments hba
				WHERE hba.organization_id = $1 AND hba.member_id = $2
			), 0)
	`, orgID, memberID).Scan(&balance); err != nil {
		return 0, fmt.Errorf("load balance: %w", err)
	}
	return balance, nil
}

func requireActiveMembership(ctx context.Context, q queryer, orgID, memberID int64) error {
	var exists bool
	if err := q.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM organization_memberships om
			JOIN organizations o ON o.id = om.organization_id
			WHERE om.organization_id = $1
				AND om.member_id = $2
				AND om.left_at IS NULL
				AND o.enabled = TRUE
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
	loc := date.Location()
	if loc == nil {
		loc = time.UTC
	}
	expiry := time.Date(date.Year(), date.Month(), date.Day(), 23, 59, 59, 0, loc)
	if kind == types.HopNeededByAround {
		expiry = expiry.AddDate(0, 0, 2)
	}
	return expiry
}

func normalizeHopNeededByDate(date time.Time) time.Time {
	loc := date.Location()
	if loc == nil {
		loc = time.UTC
	}
	return time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, loc)
}

func hopNeededByDateIsFuture(date time.Time, now time.Time) bool {
	loc := date.Location()
	if loc == nil {
		loc = time.UTC
	}
	today := now.In(loc)
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc)
	return normalizeHopNeededByDate(date).After(today)
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

func nullableInt64(v *int64) interface{} {
	if v == nil || *v == 0 {
		return nil
	}
	return *v
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
		&r.ID, &r.OrganizationID, &r.Kind, &r.CreatedBy, &r.CreatedByName,
		&r.Title, &details, &r.EstimatedHours, &r.IsPrivate,
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
