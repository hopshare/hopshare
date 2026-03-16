package service

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"fmt"
	"net/mail"
	"sort"
	"strconv"
	"strings"
	"time"

	"hopshare/internal/types"
)

const (
	OrganizationInviteDailyLimit      = 30
	OrganizationInviteTTL             = 14 * 24 * time.Hour
	organizationInvitePendingEmailIdx = "organization_invitations_pending_email_lower_idx"
)

type InviteResolution struct {
	InvitationID int64
	Organization types.Organization
	InvitedEmail string
	Status       string
	InvitedAt    time.Time
	ExpiresAt    time.Time
}

type AcceptOrganizationInviteResult struct {
	Organization  types.Organization
	AlreadyMember bool
}

type OrganizationInvitationIssue struct {
	InvitationID int64
	InvitedEmail string
	RawToken     string
	ExpiresAt    time.Time
}

type OrganizationInviteBlastResult struct {
	SentCount                   int
	RemainingToday              int
	InvalidEmails               []string
	DuplicateEmails             []string
	DisabledEmails              []string
	AlreadyMemberEmails         []string
	QuotaSkippedEmails          []string
	SendFailedEmails            []string
	ExpiredPreviousPendingCount int64
}

func ParseAndNormalizeInviteEmails(raw string) (normalized []string, invalid []string, duplicates []string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil, nil
	}

	parts := strings.Split(raw, ",")
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		email := strings.ToLower(strings.TrimSpace(part))
		if email == "" {
			continue
		}
		if !isValidInviteEmail(email) {
			invalid = append(invalid, email)
			continue
		}
		if _, ok := seen[email]; ok {
			duplicates = append(duplicates, email)
			continue
		}
		seen[email] = struct{}{}
		normalized = append(normalized, email)
	}

	sort.Strings(normalized)
	sort.Strings(invalid)
	sort.Strings(duplicates)
	return normalized, invalid, duplicates
}

func isValidInviteEmail(email string) bool {
	addr, err := mail.ParseAddress(email)
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(addr.Address), strings.TrimSpace(email))
}

func InviteDayWindowUTC(now time.Time, appLoc *time.Location) (time.Time, time.Time) {
	if appLoc == nil {
		appLoc = time.UTC
	}
	local := now.In(appLoc)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 1, 0, appLoc)
	end := time.Date(local.Year(), local.Month(), local.Day(), 23, 59, 59, 0, appLoc)
	return start.UTC(), end.UTC()
}

func CountSuccessfulInvitesToday(ctx context.Context, db *sql.DB, orgID int64, now time.Time, appLoc *time.Location) (int, error) {
	if db == nil {
		return 0, ErrNilDB
	}
	if orgID == 0 {
		return 0, ErrMissingOrgID
	}
	startUTC, endUTC := InviteDayWindowUTC(now, appLoc)
	return CountSuccessfulInvitesInWindow(ctx, db, orgID, startUTC, endUTC)
}

func CountSuccessfulInvitesInWindow(ctx context.Context, db *sql.DB, orgID int64, fromUTC, toUTC time.Time) (int, error) {
	if db == nil {
		return 0, ErrNilDB
	}
	if orgID == 0 {
		return 0, ErrMissingOrgID
	}

	var count int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM organization_invitations
		WHERE organization_id = $1
			AND sent_at IS NOT NULL
			AND sent_at >= $2
			AND sent_at <= $3
	`, orgID, fromUTC.UTC(), toUTC.UTC()).Scan(&count); err != nil {
		return 0, fmt.Errorf("count successful invites: %w", err)
	}
	return count, nil
}

func RemainingOrganizationInviteSlotsToday(ctx context.Context, db *sql.DB, orgID int64, now time.Time, appLoc *time.Location) (int, error) {
	count, err := CountSuccessfulInvitesToday(ctx, db, orgID, now, appLoc)
	if err != nil {
		return 0, err
	}
	remaining := OrganizationInviteDailyLimit - count
	if remaining < 0 {
		return 0, nil
	}
	return remaining, nil
}

func MemberHasActiveMembershipByEmail(ctx context.Context, db *sql.DB, orgID int64, email string) (bool, error) {
	if db == nil {
		return false, ErrNilDB
	}
	if orgID == 0 {
		return false, ErrMissingOrgID
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return false, ErrMissingField
	}

	var exists bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM organization_memberships om
			JOIN members m ON m.id = om.member_id
			WHERE om.organization_id = $1
				AND om.left_at IS NULL
				AND LOWER(m.email) = LOWER($2)
		)
	`, orgID, email).Scan(&exists); err != nil {
		return false, fmt.Errorf("check active membership by email: %w", err)
	}
	return exists, nil
}

func ExpirePendingOrganizationInvitesByEmail(ctx context.Context, db *sql.DB, orgID int64, email string, at time.Time) (int64, error) {
	if db == nil {
		return 0, ErrNilDB
	}
	if orgID == 0 {
		return 0, ErrMissingOrgID
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return 0, ErrMissingField
	}

	res, err := db.ExecContext(ctx, `
		UPDATE organization_invitations
		SET status = 'expired',
			responded_at = $3
		WHERE organization_id = $1
			AND LOWER(invited_email) = LOWER($2)
			AND status = 'pending'
	`, orgID, email, at.UTC())
	if err != nil {
		return 0, fmt.Errorf("expire pending invites by email: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("expire pending invites by email rows affected: %w", err)
	}
	return affected, nil
}

func CreateOrganizationInvitation(ctx context.Context, db *sql.DB, orgID, invitedBy int64, email string, now time.Time) (OrganizationInvitationIssue, error) {
	if db == nil {
		return OrganizationInvitationIssue{}, ErrNilDB
	}
	if orgID == 0 {
		return OrganizationInvitationIssue{}, ErrMissingOrgID
	}
	if invitedBy == 0 {
		return OrganizationInvitationIssue{}, ErrMissingMemberID
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if !isValidInviteEmail(email) {
		return OrganizationInvitationIssue{}, ErrMissingField
	}

	tokenID, err := randomHexToken(16)
	if err != nil {
		return OrganizationInvitationIssue{}, fmt.Errorf("generate invitation token id: %w", err)
	}
	tokenSecret, err := randomHexToken(32)
	if err != nil {
		return OrganizationInvitationIssue{}, fmt.Errorf("generate invitation token secret: %w", err)
	}
	expiresAt := now.UTC().Add(OrganizationInviteTTL)
	rawToken := buildOrganizationInviteToken(tokenID, tokenSecret)

	var invitationID int64
	if err := db.QueryRowContext(ctx, `
		INSERT INTO organization_invitations (
			organization_id,
			invited_email,
			role,
			invited_by,
			status,
			invited_at,
			expires_at,
			token_id,
			token_hash
		)
		VALUES ($1, $2, 'member', $3, 'pending', $4, $5, $6, $7)
		RETURNING id
	`, orgID, email, invitedBy, now.UTC(), expiresAt, tokenID, hashTokenSecret(tokenSecret)).Scan(&invitationID); err != nil {
		if isUniqueConstraintViolation(err, organizationInvitePendingEmailIdx) {
			return OrganizationInvitationIssue{}, ErrInviteAlreadyExists
		}
		return OrganizationInvitationIssue{}, fmt.Errorf("create organization invitation: %w", err)
	}

	return OrganizationInvitationIssue{
		InvitationID: invitationID,
		InvitedEmail: email,
		RawToken:     rawToken,
		ExpiresAt:    expiresAt,
	}, nil
}

func MarkOrganizationInvitationSent(ctx context.Context, db *sql.DB, invitationID int64, at time.Time) error {
	if db == nil {
		return ErrNilDB
	}
	if invitationID == 0 {
		return ErrInviteInvalid
	}

	res, err := db.ExecContext(ctx, `
		UPDATE organization_invitations
		SET sent_at = $2
		WHERE id = $1
			AND status = 'pending'
	`, invitationID, at.UTC())
	if err != nil {
		return fmt.Errorf("mark organization invitation sent: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("mark organization invitation sent rows affected: %w", err)
	}
	if affected == 0 {
		return ErrInviteInvalid
	}
	return nil
}

func ExpireOrganizationInvitation(ctx context.Context, db *sql.DB, invitationID int64, at time.Time) error {
	if db == nil {
		return ErrNilDB
	}
	if invitationID == 0 {
		return ErrInviteInvalid
	}
	_, err := db.ExecContext(ctx, `
		UPDATE organization_invitations
		SET status = 'expired',
			responded_at = $2
		WHERE id = $1
			AND status = 'pending'
	`, invitationID, at.UTC())
	if err != nil {
		return fmt.Errorf("expire organization invitation: %w", err)
	}
	return nil
}

func ListOrganizationInvitations(ctx context.Context, db *sql.DB, orgID int64, limit int) ([]types.OrganizationInvitation, error) {
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
			oi.id,
			oi.organization_id,
			COALESCE(oi.invited_email, ''),
			oi.role,
			oi.status,
			oi.invited_by,
			COALESCE(NULLIF(TRIM(CONCAT_WS(' ', inviter.first_name, inviter.last_name)), ''), inviter.email, ''),
			oi.invited_at,
			oi.sent_at,
			oi.expires_at,
			oi.responded_at,
			oi.accepted_at,
			oi.accepted_member_id
		FROM organization_invitations oi
		LEFT JOIN members inviter ON inviter.id = oi.invited_by
		WHERE oi.organization_id = $1
		ORDER BY oi.invited_at DESC, oi.id DESC
		LIMIT $2
	`, orgID, limit)
	if err != nil {
		return nil, fmt.Errorf("list organization invitations: %w", err)
	}
	defer rows.Close()

	out := make([]types.OrganizationInvitation, 0, limit)
	for rows.Next() {
		var inv types.OrganizationInvitation
		var invitedBy sql.NullInt64
		var sentAt sql.NullTime
		var expiresAt sql.NullTime
		var respondedAt sql.NullTime
		var acceptedAt sql.NullTime
		var acceptedMemberID sql.NullInt64
		if err := rows.Scan(
			&inv.ID,
			&inv.OrganizationID,
			&inv.InvitedEmail,
			&inv.Role,
			&inv.Status,
			&invitedBy,
			&inv.InvitedByName,
			&inv.InvitedAt,
			&sentAt,
			&expiresAt,
			&respondedAt,
			&acceptedAt,
			&acceptedMemberID,
		); err != nil {
			return nil, fmt.Errorf("scan organization invitation: %w", err)
		}
		if invitedBy.Valid {
			inv.InvitedBy = &invitedBy.Int64
		}
		if sentAt.Valid {
			t := sentAt.Time
			inv.SentAt = &t
		}
		if expiresAt.Valid {
			t := expiresAt.Time
			inv.ExpiresAt = &t
		}
		if respondedAt.Valid {
			t := respondedAt.Time
			inv.RespondedAt = &t
		}
		if acceptedAt.Valid {
			t := acceptedAt.Time
			inv.AcceptedAt = &t
		}
		if acceptedMemberID.Valid {
			inv.AcceptedMemberID = &acceptedMemberID.Int64
		}
		out = append(out, inv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list organization invitations: %w", err)
	}
	return out, nil
}

func ResolveOrganizationInvite(ctx context.Context, db *sql.DB, rawToken string, now time.Time) (InviteResolution, error) {
	if db == nil {
		return InviteResolution{}, ErrNilDB
	}

	tokenID, tokenSecret, ok := splitOrganizationInviteToken(rawToken)
	if !ok {
		return InviteResolution{}, ErrInviteInvalid
	}

	var res InviteResolution
	var tokenHash string
	if err := db.QueryRowContext(ctx, `
		SELECT
			oi.id,
			oi.organization_id,
			oi.invited_email,
			oi.status,
			oi.invited_at,
			oi.expires_at,
			oi.token_hash,
			o.id,
			o.name,
			o.url_name,
			o.city,
			o.state,
			o.description,
			o.timebank_min_balance,
			o.timebank_max_balance,
			o.timebank_starting_balance,
			o.logo_content_type,
			(o.logo_data IS NOT NULL),
			o.theme,
			o.banner_content_type,
			(o.banner_data IS NOT NULL),
			o.enabled,
			o.created_by,
			o.created_at,
			o.updated_at
		FROM organization_invitations oi
		JOIN organizations o ON o.id = oi.organization_id
		WHERE oi.token_id = $1
	`, tokenID).Scan(
		&res.InvitationID,
		&res.Organization.ID,
		&res.InvitedEmail,
		&res.Status,
		&res.InvitedAt,
		&res.ExpiresAt,
		&tokenHash,
		&res.Organization.ID,
		&res.Organization.Name,
		&res.Organization.URLName,
		&res.Organization.City,
		&res.Organization.State,
		&res.Organization.Description,
		&res.Organization.TimebankMinBalance,
		&res.Organization.TimebankMaxBalance,
		&res.Organization.TimebankStartingBalance,
		&res.Organization.LogoContentType,
		&res.Organization.HasLogo,
		&res.Organization.Theme,
		&res.Organization.BannerContentType,
		&res.Organization.HasBanner,
		&res.Organization.Enabled,
		&res.Organization.CreatedBy,
		&res.Organization.CreatedAt,
		&res.Organization.UpdatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return InviteResolution{}, ErrInviteInvalid
		}
		return InviteResolution{}, fmt.Errorf("resolve organization invite: %w", err)
	}

	if subtle.ConstantTimeCompare([]byte(tokenHash), []byte(hashTokenSecret(tokenSecret))) != 1 {
		return InviteResolution{}, ErrInviteInvalid
	}
	res.Organization.Theme = types.NormalizeOrganizationTheme(res.Organization.Theme)
	if !res.Organization.Enabled {
		return res, ErrOrganizationDisabled
	}
	if res.Status == "expired" {
		return res, ErrInviteExpired
	}
	if res.Status == "pending" && now.UTC().After(res.ExpiresAt.UTC()) {
		return res, ErrInviteExpired
	}
	return res, nil
}

func AcceptOrganizationInvite(ctx context.Context, db *sql.DB, rawToken string, memberID int64, memberEmail string, now time.Time) (AcceptOrganizationInviteResult, error) {
	if db == nil {
		return AcceptOrganizationInviteResult{}, ErrNilDB
	}
	if memberID == 0 {
		return AcceptOrganizationInviteResult{}, ErrMissingMemberID
	}
	memberEmail = strings.ToLower(strings.TrimSpace(memberEmail))
	if memberEmail == "" {
		return AcceptOrganizationInviteResult{}, ErrMissingField
	}

	tokenID, tokenSecret, ok := splitOrganizationInviteToken(rawToken)
	if !ok {
		return AcceptOrganizationInviteResult{}, ErrInviteInvalid
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return AcceptOrganizationInviteResult{}, fmt.Errorf("begin accept organization invite: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var (
		invitationID     int64
		orgID            int64
		orgName          string
		orgURLName       string
		orgCity          string
		orgState         string
		orgDescription   string
		orgMinBalance    int
		orgMaxBalance    int
		orgStarting      int
		orgLogoType      sql.NullString
		orgHasLogo       bool
		orgTheme         string
		orgBannerType    sql.NullString
		orgHasBanner     bool
		orgEnabled       bool
		orgCreatedBy     sql.NullInt64
		orgCreatedAt     time.Time
		orgUpdatedAt     time.Time
		invitedEmail     string
		status           string
		expiresAt        time.Time
		storedHash       string
		invitedBy        sql.NullInt64
		acceptedMemberID sql.NullInt64
	)
	if err = tx.QueryRowContext(ctx, `
		SELECT
			oi.id,
			oi.organization_id,
			oi.invited_email,
			oi.status,
			oi.expires_at,
			oi.token_hash,
			oi.invited_by,
			oi.accepted_member_id,
			o.name,
			o.url_name,
			o.city,
			o.state,
			o.description,
			o.timebank_min_balance,
			o.timebank_max_balance,
			o.timebank_starting_balance,
			o.logo_content_type,
			(o.logo_data IS NOT NULL),
			o.theme,
			o.banner_content_type,
			(o.banner_data IS NOT NULL),
			o.enabled,
			o.created_by,
			o.created_at,
			o.updated_at
		FROM organization_invitations oi
		JOIN organizations o ON o.id = oi.organization_id
		WHERE oi.token_id = $1
		FOR UPDATE
	`, tokenID).Scan(
		&invitationID,
		&orgID,
		&invitedEmail,
		&status,
		&expiresAt,
		&storedHash,
		&invitedBy,
		&acceptedMemberID,
		&orgName,
		&orgURLName,
		&orgCity,
		&orgState,
		&orgDescription,
		&orgMinBalance,
		&orgMaxBalance,
		&orgStarting,
		&orgLogoType,
		&orgHasLogo,
		&orgTheme,
		&orgBannerType,
		&orgHasBanner,
		&orgEnabled,
		&orgCreatedBy,
		&orgCreatedAt,
		&orgUpdatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return AcceptOrganizationInviteResult{}, ErrInviteInvalid
		}
		return AcceptOrganizationInviteResult{}, fmt.Errorf("load organization invite for accept: %w", err)
	}

	if subtle.ConstantTimeCompare([]byte(storedHash), []byte(hashTokenSecret(tokenSecret))) != 1 {
		return AcceptOrganizationInviteResult{}, ErrInviteInvalid
	}
	if !orgEnabled {
		return AcceptOrganizationInviteResult{}, ErrOrganizationDisabled
	}
	if !strings.EqualFold(invitedEmail, memberEmail) {
		return AcceptOrganizationInviteResult{}, ErrInviteEmailMismatch
	}
	if status == "expired" {
		return AcceptOrganizationInviteResult{}, ErrInviteExpired
	}
	if status == "pending" && now.UTC().After(expiresAt.UTC()) {
		if _, err = tx.ExecContext(ctx, `
			UPDATE organization_invitations
			SET status = 'expired',
				responded_at = $2
			WHERE id = $1
				AND status = 'pending'
		`, invitationID, now.UTC()); err != nil {
			return AcceptOrganizationInviteResult{}, fmt.Errorf("expire invitation on accept: %w", err)
		}
		return AcceptOrganizationInviteResult{}, ErrInviteExpired
	}
	if status == "accepted" {
		if acceptedMemberID.Valid && acceptedMemberID.Int64 != memberID {
			return AcceptOrganizationInviteResult{}, ErrInviteEmailMismatch
		}
		result := AcceptOrganizationInviteResult{
			Organization: types.Organization{
				ID:                      orgID,
				Name:                    orgName,
				URLName:                 orgURLName,
				City:                    orgCity,
				State:                   orgState,
				Description:             orgDescription,
				TimebankMinBalance:      orgMinBalance,
				TimebankMaxBalance:      orgMaxBalance,
				TimebankStartingBalance: orgStarting,
				HasLogo:                 orgHasLogo,
				Theme:                   types.NormalizeOrganizationTheme(orgTheme),
				HasBanner:               orgHasBanner,
				Enabled:                 orgEnabled,
				CreatedAt:               orgCreatedAt,
				UpdatedAt:               orgUpdatedAt,
			},
			AlreadyMember: true,
		}
		if orgLogoType.Valid {
			result.Organization.LogoContentType = &orgLogoType.String
		}
		if orgBannerType.Valid {
			result.Organization.BannerContentType = &orgBannerType.String
		}
		if orgCreatedBy.Valid {
			result.Organization.CreatedBy = &orgCreatedBy.Int64
		}
		if err = tx.Commit(); err != nil {
			return AcceptOrganizationInviteResult{}, fmt.Errorf("commit organization invite accepted no-op: %w", err)
		}
		return result, nil
	}
	if status != "pending" {
		return AcceptOrganizationInviteResult{}, ErrInviteInvalid
	}

	actorID := memberID
	if invitedBy.Valid && invitedBy.Int64 != 0 {
		actorID = invitedBy.Int64
	}
	alreadyMember, ensureErr := ensureActiveMembershipAndStartingBalance(ctx, tx, orgID, memberID, actorID)
	if ensureErr != nil {
		return AcceptOrganizationInviteResult{}, ensureErr
	}

	if _, err = tx.ExecContext(ctx, `
		UPDATE organization_invitations
		SET status = 'accepted',
			responded_at = $2,
			accepted_at = $2,
			accepted_member_id = $3
		WHERE id = $1
	`, invitationID, now.UTC(), memberID); err != nil {
		return AcceptOrganizationInviteResult{}, fmt.Errorf("accept organization invitation: %w", err)
	}

	notificationHref := ""
	if strings.TrimSpace(orgURLName) != "" {
		notificationHref = "/organization/" + orgURLName
	}
	_ = createMemberNotification(
		ctx,
		tx,
		memberID,
		"Your membership in "+orgName+" was approved.",
		notificationHref,
	)

	if err = tx.Commit(); err != nil {
		return AcceptOrganizationInviteResult{}, fmt.Errorf("commit organization invite acceptance: %w", err)
	}

	result := AcceptOrganizationInviteResult{
		Organization: types.Organization{
			ID:                      orgID,
			Name:                    orgName,
			URLName:                 orgURLName,
			City:                    orgCity,
			State:                   orgState,
			Description:             orgDescription,
			TimebankMinBalance:      orgMinBalance,
			TimebankMaxBalance:      orgMaxBalance,
			TimebankStartingBalance: orgStarting,
			HasLogo:                 orgHasLogo,
			Theme:                   types.NormalizeOrganizationTheme(orgTheme),
			HasBanner:               orgHasBanner,
			Enabled:                 orgEnabled,
			CreatedAt:               orgCreatedAt,
			UpdatedAt:               orgUpdatedAt,
		},
		AlreadyMember: alreadyMember,
	}
	if orgLogoType.Valid {
		result.Organization.LogoContentType = &orgLogoType.String
	}
	if orgBannerType.Valid {
		result.Organization.BannerContentType = &orgBannerType.String
	}
	if orgCreatedBy.Valid {
		result.Organization.CreatedBy = &orgCreatedBy.Int64
	}
	return result, nil
}

func ensureActiveMembershipAndStartingBalance(ctx context.Context, tx *sql.Tx, orgID, memberID, actorMemberID int64) (bool, error) {
	var hadAnyMembership bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM organization_memberships
			WHERE organization_id = $1
				AND member_id = $2
		)
	`, orgID, memberID).Scan(&hadAnyMembership); err != nil {
		return false, fmt.Errorf("check membership history: %w", err)
	}

	var exists bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM organization_memberships
			WHERE organization_id = $1
				AND member_id = $2
				AND left_at IS NULL
		)
	`, orgID, memberID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check existing membership: %w", err)
	}
	if !exists {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO organization_memberships (organization_id, member_id, role)
			VALUES ($1, $2, 'member')
		`, orgID, memberID); err != nil {
			return false, fmt.Errorf("create membership: %w", err)
		}
	}

	if !hadAnyMembership {
		policy, policyErr := loadTimebankPolicy(ctx, tx, orgID)
		if policyErr != nil {
			return false, policyErr
		}
		if policy.StartingBalance > 0 {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO hour_balance_adjustments (
					organization_id,
					member_id,
					admin_member_id,
					hours_delta,
					reason,
					is_starting_balance
				)
				VALUES ($1, $2, $3, $4, $5, TRUE)
			`, orgID, memberID, actorMemberID, policy.StartingBalance, "Organization starting balance"); err != nil {
				return false, fmt.Errorf("insert starting balance adjustment: %w", err)
			}
		}
	}

	return exists, nil
}

func buildOrganizationInviteToken(tokenID, tokenSecret string) string {
	return tokenID + memberTokenSeparator + tokenSecret
}

func splitOrganizationInviteToken(rawToken string) (tokenID string, tokenSecret string, ok bool) {
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" {
		return "", "", false
	}
	tokenID, tokenSecret, ok = strings.Cut(rawToken, memberTokenSeparator)
	if !ok || tokenID == "" || tokenSecret == "" {
		return "", "", false
	}
	if !isLowerHex(tokenID) || !isLowerHex(tokenSecret) {
		return "", "", false
	}
	return tokenID, tokenSecret, true
}

func SendOwnerInviteBlastSummaryMessage(ctx context.Context, db *sql.DB, ownerMemberID, orgID int64, orgName string, result OrganizationInviteBlastResult) error {
	if db == nil {
		return ErrNilDB
	}
	if ownerMemberID == 0 {
		return ErrMissingMemberID
	}
	if orgID == 0 {
		return ErrMissingOrgID
	}

	parts := []string{
		fmt.Sprintf("Invite blast complete for %s:", strings.TrimSpace(orgName)),
		fmt.Sprintf("%d sent", result.SentCount),
	}
	if len(result.InvalidEmails) > 0 {
		parts = append(parts, fmt.Sprintf("%d invalid", len(result.InvalidEmails)))
	}
	if len(result.DuplicateEmails) > 0 {
		parts = append(parts, fmt.Sprintf("%d duplicate", len(result.DuplicateEmails)))
	}
	if len(result.DisabledEmails) > 0 {
		parts = append(parts, fmt.Sprintf("%d disabled", len(result.DisabledEmails)))
	}
	if len(result.AlreadyMemberEmails) > 0 {
		parts = append(parts, fmt.Sprintf("%d already members", len(result.AlreadyMemberEmails)))
	}
	if len(result.QuotaSkippedEmails) > 0 {
		parts = append(parts, fmt.Sprintf("%d quota skipped", len(result.QuotaSkippedEmails)))
	}
	if len(result.SendFailedEmails) > 0 {
		parts = append(parts, fmt.Sprintf("%d send failed", len(result.SendFailedEmails)))
	}
	if result.ExpiredPreviousPendingCount > 0 {
		parts = append(parts, fmt.Sprintf("%d prior invites replaced", result.ExpiredPreviousPendingCount))
	}
	parts = append(parts, fmt.Sprintf("%d remaining today", result.RemainingToday))

	return CreateMemberNotification(
		ctx,
		db,
		ownerMemberID,
		strings.Join(parts, ", "),
		"/organizations/manage?org_id="+strconv.FormatInt(orgID, 10)+"&tab=invite",
	)
}
