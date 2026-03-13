package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lib/pq"

	"hopshare/internal/types"
)

const (
	organizationURLNameMaxLen           = 63
	organizationURLNameDefault          = "organization"
	organizationURLNameUniqueConstraint = "organizations_url_name_key"
	organizationURLNameMaxAttempts      = 1000
)

// PrimaryOwnedOrganization returns one enabled organization where the member is an active owner.
// When multiple organizations match, the first by name is returned.
func PrimaryOwnedOrganization(ctx context.Context, db *sql.DB, memberID int64) (types.Organization, error) {
	if db == nil {
		return types.Organization{}, ErrNilDB
	}
	if memberID == 0 {
		return types.Organization{}, ErrMissingMemberID
	}

	row := db.QueryRowContext(ctx, `
		SELECT o.id, o.name, o.url_name, o.city, o.state, o.description, o.timebank_min_balance, o.timebank_max_balance, o.timebank_starting_balance, o.logo_content_type, (o.logo_data IS NOT NULL), o.enabled, o.created_by, o.created_at, o.updated_at
		FROM organizations o
		JOIN organization_memberships om ON om.organization_id = o.id
		WHERE om.member_id = $1 AND om.role = 'owner' AND om.left_at IS NULL AND o.enabled = TRUE
		ORDER BY o.name ASC
		LIMIT 1
	`, memberID)

	var o types.Organization
	if err := row.Scan(&o.ID, &o.Name, &o.URLName, &o.City, &o.State, &o.Description, &o.TimebankMinBalance, &o.TimebankMaxBalance, &o.TimebankStartingBalance, &o.LogoContentType, &o.HasLogo, &o.Enabled, &o.CreatedBy, &o.CreatedAt, &o.UpdatedAt); err != nil {
		return types.Organization{}, err
	}
	return o, nil
}

// MemberCreatedOrganization reports whether the member has created any organization.
func MemberCreatedOrganization(ctx context.Context, db *sql.DB, memberID int64) (bool, error) {
	if db == nil {
		return false, ErrNilDB
	}
	if memberID == 0 {
		return false, ErrMissingMemberID
	}

	var exists bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM organizations
			WHERE created_by = $1
		)
	`, memberID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check member created organization: %w", err)
	}
	return exists, nil
}

// ActiveOrganizationsForMember returns all organizations the member currently belongs to (left_at IS NULL).
func ActiveOrganizationsForMember(ctx context.Context, db *sql.DB, memberID int64) ([]types.Organization, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if memberID == 0 {
		return nil, ErrMissingMemberID
	}

	rows, err := db.QueryContext(ctx, `
		SELECT o.id, o.name, o.url_name, o.city, o.state, o.description, o.timebank_min_balance, o.timebank_max_balance, o.timebank_starting_balance, o.logo_content_type, (o.logo_data IS NOT NULL), o.enabled, o.created_by, o.created_at, o.updated_at
		FROM organizations o
		JOIN organization_memberships om ON om.organization_id = o.id
		WHERE om.member_id = $1 AND om.left_at IS NULL AND o.enabled = TRUE
		ORDER BY o.name
	`, memberID)
	if err != nil {
		return nil, fmt.Errorf("list member organizations: %w", err)
	}
	defer rows.Close()

	var orgs []types.Organization
	for rows.Next() {
		var o types.Organization
		if err := rows.Scan(&o.ID, &o.Name, &o.URLName, &o.City, &o.State, &o.Description, &o.TimebankMinBalance, &o.TimebankMaxBalance, &o.TimebankStartingBalance, &o.LogoContentType, &o.HasLogo, &o.Enabled, &o.CreatedBy, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan organization: %w", err)
		}
		orgs = append(orgs, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list member organizations: %w", err)
	}

	return orgs, nil
}

// MemberOrganizations returns all organizations for a member with role information.
func MemberOrganizations(ctx context.Context, db *sql.DB, memberID int64) ([]types.MemberOrganization, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if memberID == 0 {
		return nil, ErrMissingMemberID
	}

	rows, err := db.QueryContext(ctx, `
		SELECT o.id, o.name, o.url_name, o.city, o.state, o.description, o.timebank_min_balance, o.timebank_max_balance, o.timebank_starting_balance, o.logo_content_type, (o.logo_data IS NOT NULL), o.enabled, o.created_by, o.created_at, o.updated_at,
		       om.role
		FROM organizations o
		JOIN organization_memberships om ON om.organization_id = o.id
		WHERE om.member_id = $1 AND om.left_at IS NULL AND o.enabled = TRUE
		ORDER BY o.name
	`, memberID)
	if err != nil {
		return nil, fmt.Errorf("list member organizations with roles: %w", err)
	}
	defer rows.Close()

	var orgs []types.MemberOrganization
	for rows.Next() {
		var o types.MemberOrganization
		if err := rows.Scan(&o.ID, &o.Name, &o.URLName, &o.City, &o.State, &o.Description, &o.TimebankMinBalance, &o.TimebankMaxBalance, &o.TimebankStartingBalance, &o.LogoContentType, &o.HasLogo, &o.Enabled, &o.CreatedBy, &o.CreatedAt, &o.UpdatedAt, &o.Role); err != nil {
			return nil, fmt.Errorf("scan member organization: %w", err)
		}
		orgs = append(orgs, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list member organizations with roles: %w", err)
	}

	return orgs, nil
}

// OrganizationForOwner returns an organization if the member is an owner.
func OrganizationForOwner(ctx context.Context, db *sql.DB, memberID, orgID int64) (types.Organization, error) {
	if db == nil {
		return types.Organization{}, ErrNilDB
	}
	if memberID == 0 {
		return types.Organization{}, ErrMissingMemberID
	}
	if orgID == 0 {
		return types.Organization{}, ErrMissingOrgID
	}

	row := db.QueryRowContext(ctx, `
		SELECT o.id, o.name, o.url_name, o.city, o.state, o.description, o.timebank_min_balance, o.timebank_max_balance, o.timebank_starting_balance, o.logo_content_type, (o.logo_data IS NOT NULL), o.enabled, o.created_by, o.created_at, o.updated_at
		FROM organizations o
		JOIN organization_memberships om ON om.organization_id = o.id
		WHERE om.member_id = $1 AND om.organization_id = $2 AND om.role = 'owner' AND om.left_at IS NULL AND o.enabled = TRUE
	`, memberID, orgID)

	var o types.Organization
	if err := row.Scan(&o.ID, &o.Name, &o.URLName, &o.City, &o.State, &o.Description, &o.TimebankMinBalance, &o.TimebankMaxBalance, &o.TimebankStartingBalance, &o.LogoContentType, &o.HasLogo, &o.Enabled, &o.CreatedBy, &o.CreatedAt, &o.UpdatedAt); err != nil {
		return types.Organization{}, err
	}
	return o, nil
}

// MemberOwnsOrganization reports whether the member is an active owner of the organization.
func MemberOwnsOrganization(ctx context.Context, db *sql.DB, memberID, orgID int64) (bool, error) {
	if db == nil {
		return false, ErrNilDB
	}
	if memberID == 0 {
		return false, ErrMissingMemberID
	}
	if orgID == 0 {
		return false, ErrMissingOrgID
	}

	var exists bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM organization_memberships
			WHERE member_id = $1 AND organization_id = $2 AND role = 'owner' AND left_at IS NULL
		)
	`, memberID, orgID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check member organization ownership: %w", err)
	}
	return exists, nil
}

func activeOwnerCountTx(ctx context.Context, tx *sql.Tx, orgID int64) (int, error) {
	var count int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM organization_memberships
		WHERE organization_id = $1
			AND left_at IS NULL
			AND role = 'owner'
	`, orgID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count active owners: %w", err)
	}
	return count, nil
}

// MemberHasActiveMembership reports whether the member belongs to the organization.
func MemberHasActiveMembership(ctx context.Context, db *sql.DB, memberID, orgID int64) (bool, error) {
	if db == nil {
		return false, ErrNilDB
	}
	if memberID == 0 {
		return false, ErrMissingMemberID
	}
	if orgID == 0 {
		return false, ErrMissingOrgID
	}

	var exists bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM organization_memberships om
			JOIN organizations o ON o.id = om.organization_id
			WHERE om.member_id = $1 AND om.organization_id = $2 AND om.left_at IS NULL AND o.enabled = TRUE
		)
	`, memberID, orgID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check member organization membership: %w", err)
	}
	return exists, nil
}

// MemberLeaveBlockedOrganizationIDs returns active organization IDs the member cannot leave
// because they are currently the sole active owner.
func MemberLeaveBlockedOrganizationIDs(ctx context.Context, db *sql.DB, memberID int64) (map[int64]struct{}, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if memberID == 0 {
		return nil, ErrMissingMemberID
	}

	rows, err := db.QueryContext(ctx, `
		SELECT om.organization_id
		FROM organization_memberships om
		WHERE om.member_id = $1
			AND om.left_at IS NULL
			AND om.role = 'owner'
			AND NOT EXISTS (
				SELECT 1
				FROM organization_memberships other
				WHERE other.organization_id = om.organization_id
					AND other.role = 'owner'
					AND other.left_at IS NULL
					AND other.member_id <> $1
			)
	`, memberID)
	if err != nil {
		return nil, fmt.Errorf("list leave-blocked organizations for member: %w", err)
	}
	defer rows.Close()

	blocked := make(map[int64]struct{})
	for rows.Next() {
		var orgID int64
		if err := rows.Scan(&orgID); err != nil {
			return nil, fmt.Errorf("scan leave-blocked organization: %w", err)
		}
		blocked[orgID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list leave-blocked organizations for member: %w", err)
	}
	return blocked, nil
}

// MembersShareOrganization reports whether two members share an active organization.
func MembersShareOrganization(ctx context.Context, db *sql.DB, memberID, otherMemberID int64) (bool, error) {
	if db == nil {
		return false, ErrNilDB
	}
	if memberID == 0 || otherMemberID == 0 {
		return false, ErrMissingMemberID
	}
	if memberID == otherMemberID {
		return true, nil
	}

	var exists bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM organization_memberships om1
			JOIN organization_memberships om2 ON om1.organization_id = om2.organization_id
			JOIN organizations o ON o.id = om1.organization_id
			WHERE om1.member_id = $1
				AND om2.member_id = $2
				AND om1.left_at IS NULL
				AND om2.left_at IS NULL
				AND o.enabled = TRUE
		)
	`, memberID, otherMemberID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check shared organization membership: %w", err)
	}
	return exists, nil
}

// GetOrganizationByID returns an organization by ID.
func GetOrganizationByID(ctx context.Context, db *sql.DB, orgID int64) (types.Organization, error) {
	if db == nil {
		return types.Organization{}, ErrNilDB
	}
	if orgID == 0 {
		return types.Organization{}, ErrMissingOrgID
	}

	row := db.QueryRowContext(ctx, `
		SELECT id, name, url_name, city, state, description, timebank_min_balance, timebank_max_balance, timebank_starting_balance, logo_content_type, (logo_data IS NOT NULL), enabled, created_by, created_at, updated_at
		FROM organizations
		WHERE id = $1
	`, orgID)

	var o types.Organization
	if err := row.Scan(&o.ID, &o.Name, &o.URLName, &o.City, &o.State, &o.Description, &o.TimebankMinBalance, &o.TimebankMaxBalance, &o.TimebankStartingBalance, &o.LogoContentType, &o.HasLogo, &o.Enabled, &o.CreatedBy, &o.CreatedAt, &o.UpdatedAt); err != nil {
		return types.Organization{}, fmt.Errorf("get organization by id: %w", err)
	}
	return o, nil
}

// GetEnabledOrganizationByID returns an enabled organization by ID.
func GetEnabledOrganizationByID(ctx context.Context, db *sql.DB, orgID int64) (types.Organization, error) {
	org, err := GetOrganizationByID(ctx, db, orgID)
	if err != nil {
		return types.Organization{}, err
	}
	if !org.Enabled {
		return types.Organization{}, sql.ErrNoRows
	}
	return org, nil
}

// GetOrganizationByURLName returns an organization by permanent URL name.
func GetOrganizationByURLName(ctx context.Context, db *sql.DB, urlName string) (types.Organization, error) {
	if db == nil {
		return types.Organization{}, ErrNilDB
	}
	urlName = strings.TrimSpace(strings.ToLower(urlName))
	if urlName == "" {
		return types.Organization{}, ErrMissingField
	}

	row := db.QueryRowContext(ctx, `
		SELECT id, name, url_name, city, state, description, timebank_min_balance, timebank_max_balance, timebank_starting_balance, logo_content_type, (logo_data IS NOT NULL), enabled, created_by, created_at, updated_at
		FROM organizations
		WHERE url_name = $1
	`, urlName)

	var o types.Organization
	if err := row.Scan(&o.ID, &o.Name, &o.URLName, &o.City, &o.State, &o.Description, &o.TimebankMinBalance, &o.TimebankMaxBalance, &o.TimebankStartingBalance, &o.LogoContentType, &o.HasLogo, &o.Enabled, &o.CreatedBy, &o.CreatedAt, &o.UpdatedAt); err != nil {
		return types.Organization{}, fmt.Errorf("get organization by url name: %w", err)
	}
	return o, nil
}

// GetEnabledOrganizationByURLName returns an enabled organization by URL name.
func GetEnabledOrganizationByURLName(ctx context.Context, db *sql.DB, urlName string) (types.Organization, error) {
	org, err := GetOrganizationByURLName(ctx, db, urlName)
	if err != nil {
		return types.Organization{}, err
	}
	if !org.Enabled {
		return types.Organization{}, sql.ErrNoRows
	}
	return org, nil
}

// ListOrganizations returns all organizations ordered by name.
func ListOrganizations(ctx context.Context, db *sql.DB) ([]types.Organization, error) {
	if db == nil {
		return nil, ErrNilDB
	}

	rows, err := db.QueryContext(ctx, `
		SELECT id, name, url_name, city, state, description, timebank_min_balance, timebank_max_balance, timebank_starting_balance, logo_content_type, (logo_data IS NOT NULL), enabled, created_by, created_at, updated_at
		FROM organizations
		WHERE enabled = TRUE
		ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("list organizations: %w", err)
	}
	defer rows.Close()

	var orgs []types.Organization
	for rows.Next() {
		var o types.Organization
		if err := rows.Scan(&o.ID, &o.Name, &o.URLName, &o.City, &o.State, &o.Description, &o.TimebankMinBalance, &o.TimebankMaxBalance, &o.TimebankStartingBalance, &o.LogoContentType, &o.HasLogo, &o.Enabled, &o.CreatedBy, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan organization: %w", err)
		}
		orgs = append(orgs, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list organizations: %w", err)
	}

	return orgs, nil
}

// SearchOrganizationsForAdmin returns organizations filtered by case-insensitive name search.
// Disabled organizations are included for admin use.
func SearchOrganizationsForAdmin(ctx context.Context, db *sql.DB, query string, limit int) ([]types.Organization, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if limit <= 0 {
		limit = 20
	}

	query = strings.TrimSpace(query)
	searchPattern := "%"
	if query != "" {
		searchPattern = "%" + strings.ToLower(query) + "%"
	}

	rows, err := db.QueryContext(ctx, `
		SELECT id, name, url_name, city, state, description, timebank_min_balance, timebank_max_balance, timebank_starting_balance, logo_content_type, (logo_data IS NOT NULL), enabled, created_by, created_at, updated_at
		FROM organizations
		WHERE ($1 = '%' OR LOWER(name) LIKE $1)
		ORDER BY name
		LIMIT $2
	`, searchPattern, limit)
	if err != nil {
		return nil, fmt.Errorf("search organizations for admin: %w", err)
	}
	defer rows.Close()

	var orgs []types.Organization
	for rows.Next() {
		var o types.Organization
		if err := rows.Scan(&o.ID, &o.Name, &o.URLName, &o.City, &o.State, &o.Description, &o.TimebankMinBalance, &o.TimebankMaxBalance, &o.TimebankStartingBalance, &o.LogoContentType, &o.HasLogo, &o.Enabled, &o.CreatedBy, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan organization: %w", err)
		}
		orgs = append(orgs, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search organizations for admin: %w", err)
	}

	return orgs, nil
}

// OrganizationEnabled reports whether an organization is enabled.
func OrganizationEnabled(ctx context.Context, db *sql.DB, orgID int64) (bool, error) {
	if db == nil {
		return false, ErrNilDB
	}
	if orgID == 0 {
		return false, ErrMissingOrgID
	}

	var enabled bool
	if err := db.QueryRowContext(ctx, `
		SELECT enabled
		FROM organizations
		WHERE id = $1
	`, orgID).Scan(&enabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrMissingOrgID
		}
		return false, fmt.Errorf("check organization enabled: %w", err)
	}
	return enabled, nil
}

// SetOrganizationEnabled updates an organization's enabled state.
func SetOrganizationEnabled(ctx context.Context, db *sql.DB, orgID int64, enabled bool) error {
	if db == nil {
		return ErrNilDB
	}
	if orgID == 0 {
		return ErrMissingOrgID
	}

	res, err := db.ExecContext(ctx, `
		UPDATE organizations
		SET enabled = $1, updated_at = NOW()
		WHERE id = $2
	`, enabled, orgID)
	if err != nil {
		return fmt.Errorf("set organization enabled: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("set organization enabled rows affected: %w", err)
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func OrganizationLogo(ctx context.Context, db *sql.DB, orgID int64) ([]byte, string, bool, error) {
	if db == nil {
		return nil, "", false, ErrNilDB
	}
	if orgID == 0 {
		return nil, "", false, ErrMissingOrgID
	}

	var contentType sql.NullString
	var data []byte
	if err := db.QueryRowContext(ctx, `
		SELECT logo_content_type, logo_data
		FROM organizations
		WHERE id = $1
	`, orgID).Scan(&contentType, &data); err != nil {
		return nil, "", false, fmt.Errorf("get organization logo: %w", err)
	}

	if len(data) == 0 {
		return nil, "", false, nil
	}

	ct := contentType.String
	if ct == "" {
		ct = "application/octet-stream"
	}

	return data, ct, true, nil
}

func SetOrganizationLogo(ctx context.Context, db *sql.DB, orgID int64, contentType string, data []byte) error {
	if db == nil {
		return ErrNilDB
	}
	if orgID == 0 {
		return ErrMissingOrgID
	}
	if strings.TrimSpace(contentType) == "" || len(data) == 0 {
		return ErrMissingField
	}

	res, err := db.ExecContext(ctx, `
		UPDATE organizations
		SET logo_content_type = $1, logo_data = $2, updated_at = NOW()
		WHERE id = $3
	`, contentType, data, orgID)
	if err != nil {
		return fmt.Errorf("set organization logo: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("set organization logo rows affected: %w", err)
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// RequestMembership inserts a pending membership request for a member/organization pair.
func RequestMembership(ctx context.Context, db *sql.DB, memberID, orgID int64, note *string) error {
	if db == nil {
		return ErrNilDB
	}
	if memberID == 0 {
		return ErrMissingMemberID
	}
	if orgID == 0 {
		return ErrMissingOrgID
	}

	enabled, err := OrganizationEnabled(ctx, db, orgID)
	if err != nil {
		return err
	}
	if !enabled {
		return ErrOrganizationDisabled
	}

	var exists bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM membership_requests
			WHERE organization_id = $1 AND member_id = $2 AND status = 'pending'
		)
	`, orgID, memberID).Scan(&exists); err != nil {
		return fmt.Errorf("check existing membership request: %w", err)
	}
	if exists {
		return ErrRequestAlreadyExists
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO membership_requests (organization_id, member_id, status, decision_note)
		VALUES ($1, $2, 'pending', $3)
	`, orgID, memberID, note); err != nil {
		return fmt.Errorf("insert membership request: %w", err)
	}

	requesterName, _, err := memberNameAndEmailByID(ctx, db, memberID)
	if err != nil {
		return err
	}
	orgName := "your organization"
	if name, _, detailsErr := organizationNameAndURLNameByID(ctx, db, orgID); detailsErr == nil {
		orgName = name
	}

	ownerIDs, err := activeOwnerMemberIDsForOrganization(ctx, db, orgID)
	if err != nil {
		return err
	}
	notificationHref := "/organizations/manage?org_id=" + strconv.FormatInt(orgID, 10)
	notificationText := requesterName + " requested to join " + orgName + "."
	for _, ownerID := range ownerIDs {
		if ownerID == memberID {
			continue
		}
		_ = createMemberNotification(ctx, db, ownerID, notificationText, notificationHref)
	}

	return nil
}

// PendingMembershipRequests returns pending membership requests for an organization.
func PendingMembershipRequests(ctx context.Context, db *sql.DB, orgID int64) ([]types.MembershipRequest, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if orgID == 0 {
		return nil, ErrMissingOrgID
	}

	rows, err := db.QueryContext(ctx, `
			SELECT
				mr.id,
				mr.organization_id,
				mr.member_id,
				COALESCE(NULLIF(TRIM(CONCAT_WS(' ', m.first_name, m.last_name)), ''), m.email),
				m.email,
				mr.requested_at,
				mr.status
			FROM membership_requests mr
			JOIN members m ON m.id = mr.member_id
			WHERE mr.organization_id = $1 AND mr.status = 'pending'
		ORDER BY mr.requested_at ASC
	`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list pending membership requests: %w", err)
	}
	defer rows.Close()

	var reqs []types.MembershipRequest
	for rows.Next() {
		var mr types.MembershipRequest
		if err := rows.Scan(&mr.ID, &mr.OrganizationID, &mr.MemberID, &mr.MemberName, &mr.MemberEmail, &mr.RequestedAt, &mr.Status); err != nil {
			return nil, fmt.Errorf("scan membership request: %w", err)
		}
		reqs = append(reqs, mr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list pending membership requests: %w", err)
	}

	return reqs, nil
}

// ApproveMembershipRequest approves a membership request and ensures membership exists.
func ApproveMembershipRequest(ctx context.Context, db *sql.DB, requestID, decidedBy int64) error {
	if db == nil {
		return ErrNilDB
	}
	if requestID == 0 {
		return ErrRequestNotFound
	}
	if decidedBy == 0 {
		return ErrMissingMemberID
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin approve request: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var orgID, memberID int64
	if err = tx.QueryRowContext(ctx, `
		UPDATE membership_requests
		SET status = 'approved', decided_at = NOW(), decided_by = $1
		WHERE id = $2 AND status = 'pending'
		RETURNING organization_id, member_id
	`, decidedBy, requestID).Scan(&orgID, &memberID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrRequestNotFound
		}
		return fmt.Errorf("approve membership request: %w", err)
	}

	if _, ensureErr := ensureActiveMembershipAndStartingBalance(ctx, tx, orgID, memberID, decidedBy); ensureErr != nil {
		return ensureErr
	}

	orgName := "your organization"
	orgURLName := ""
	if name, urlName, nameErr := organizationNameAndURLNameByID(ctx, tx, orgID); nameErr == nil {
		orgName = name
		orgURLName = urlName
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
		return fmt.Errorf("commit approve request: %w", err)
	}

	return nil
}

// DenyMembershipRequest rejects a membership request.
func DenyMembershipRequest(ctx context.Context, db *sql.DB, requestID, decidedBy int64) error {
	if db == nil {
		return ErrNilDB
	}
	if requestID == 0 {
		return ErrRequestNotFound
	}
	if decidedBy == 0 {
		return ErrMissingMemberID
	}

	var orgID int64
	var memberID int64
	if err := db.QueryRowContext(ctx, `
		UPDATE membership_requests
		SET status = 'rejected', decided_at = NOW(), decided_by = $1
		WHERE id = $2 AND status = 'pending'
		RETURNING organization_id, member_id
	`, decidedBy, requestID).Scan(&orgID, &memberID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrRequestNotFound
		}
		return fmt.Errorf("deny membership request: %w", err)
	}

	orgName := "the organization"
	href := ""
	if name, urlName, nameErr := organizationNameAndURLNameByID(ctx, db, orgID); nameErr == nil {
		orgName = name
		if strings.TrimSpace(urlName) != "" {
			href = "/organization/" + urlName
		}
	}
	_ = CreateMemberNotification(
		ctx,
		db,
		memberID,
		"Your request to join "+orgName+" was denied.",
		href,
	)

	return nil
}

// OrganizationMembers returns active members of an organization, owners first then alphabetical.
func OrganizationMembers(ctx context.Context, db *sql.DB, orgID int64) ([]types.OrganizationMember, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if orgID == 0 {
		return nil, ErrMissingOrgID
	}

	rows, err := db.QueryContext(ctx, `
			SELECT
				m.id,
				COALESCE(NULLIF(TRIM(CONCAT_WS(' ', m.first_name, m.last_name)), ''), m.email),
				m.email,
				om.role,
				om.joined_at
			FROM organization_memberships om
			JOIN members m ON m.id = om.member_id
			WHERE om.organization_id = $1 AND om.left_at IS NULL
			ORDER BY om.role DESC, LOWER(m.last_name) ASC, LOWER(m.first_name) ASC, LOWER(m.email) ASC
		`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list organization members: %w", err)
	}
	defer rows.Close()

	var members []types.OrganizationMember
	for rows.Next() {
		var om types.OrganizationMember
		if err := rows.Scan(&om.MemberID, &om.DisplayName, &om.Email, &om.Role, &om.JoinedAt); err != nil {
			return nil, fmt.Errorf("scan organization member: %w", err)
		}
		members = append(members, om)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list organization members: %w", err)
	}
	return members, nil
}

// LeaveOrganization marks the member's membership as left and reselects current_organization.
// If there are remaining active organizations, the first row returned from the database is used.
// If there are none, current_organization is cleared.
func LeaveOrganization(ctx context.Context, db *sql.DB, orgID, memberID int64) (*int64, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if orgID == 0 {
		return nil, ErrMissingOrgID
	}
	if memberID == 0 {
		return nil, ErrMissingMemberID
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin leave organization: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var role string
	if err := tx.QueryRowContext(ctx, `
		SELECT role
		FROM organization_memberships
		WHERE organization_id = $1 AND member_id = $2 AND left_at IS NULL
		FOR UPDATE
	`, orgID, memberID).Scan(&role); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrMembershipNotFound
		}
		return nil, fmt.Errorf("load membership before leave: %w", err)
	}

	if role == "owner" {
		ownerCount, countErr := activeOwnerCountTx(ctx, tx, orgID)
		if countErr != nil {
			return nil, countErr
		}
		if ownerCount <= 1 {
			return nil, ErrInvalidRoleChange
		}
	}

	res, err := tx.ExecContext(ctx, `
		UPDATE organization_memberships
		SET left_at = NOW()
		WHERE organization_id = $1 AND member_id = $2 AND left_at IS NULL
	`, orgID, memberID)
	if err != nil {
		return nil, fmt.Errorf("leave organization: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("leave organization rows affected: %w", err)
	}
	if affected == 0 {
		return nil, ErrMembershipNotFound
	}

	var nextOrgID *int64
	var nextOrg sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT o.id
		FROM organizations o
		JOIN organization_memberships om ON om.organization_id = o.id
		WHERE om.member_id = $1 AND om.left_at IS NULL AND o.enabled = TRUE
		ORDER BY o.name
		LIMIT 1
	`, memberID).Scan(&nextOrg); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("load first remaining organization after leave: %w", err)
		}
	} else if nextOrg.Valid {
		id := nextOrg.Int64
		nextOrgID = &id
	}

	var currentOrgValue any
	if nextOrgID != nil {
		currentOrgValue = *nextOrgID
	}
	memberRes, err := tx.ExecContext(ctx, `
		UPDATE members
		SET current_organization = $1, updated_at = NOW()
		WHERE id = $2
	`, currentOrgValue, memberID)
	if err != nil {
		return nil, fmt.Errorf("update member current organization after leave: %w", err)
	}
	memberAffected, err := memberRes.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("update member current organization after leave rows affected: %w", err)
	}
	if memberAffected == 0 {
		return nil, sql.ErrNoRows
	}

	orgName := "your organization"
	if name, _, nameErr := organizationNameAndURLNameByID(ctx, tx, orgID); nameErr == nil {
		orgName = name
	}
	leaverName := "User"
	if displayName, _, displayErr := memberNameAndEmailByID(ctx, tx, memberID); displayErr == nil && strings.TrimSpace(displayName) != "" {
		leaverName = displayName
	}
	_ = createMemberNotification(
		ctx,
		tx,
		memberID,
		"You left "+orgName+".",
		"",
	)

	rows, err := tx.QueryContext(ctx, `
		SELECT member_id
		FROM organization_memberships
		WHERE organization_id = $1
			AND role = 'owner'
			AND left_at IS NULL
		ORDER BY member_id
	`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list owner recipients for leave message: %w", err)
	}

	ownerIDs := make([]int64, 0)
	for rows.Next() {
		var ownerID int64
		if err := rows.Scan(&ownerID); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan owner recipient for leave message: %w", err)
		}
		ownerIDs = append(ownerIDs, ownerID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("list owner recipients for leave message: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close owner recipients for leave message: %w", err)
	}

	leaveHref := "/organizations/manage?org_id=" + strconv.FormatInt(orgID, 10)
	leaveBody := "User " + leaverName + " has left the Organization " + orgName + "."
	for _, ownerID := range ownerIDs {
		if ownerID == memberID {
			continue
		}
		if err := createMemberNotification(ctx, tx, ownerID, leaveBody, leaveHref); err != nil {
			return nil, fmt.Errorf("create owner leave notification: %w", err)
		}
	}

	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit leave organization: %w", err)
	}

	return nextOrgID, nil
}

// RemoveOrganizationMember marks a membership as left.
func RemoveOrganizationMember(ctx context.Context, db *sql.DB, orgID, memberID int64, removedBy int64) error {
	if db == nil {
		return ErrNilDB
	}
	if orgID == 0 {
		return ErrMissingOrgID
	}
	if memberID == 0 {
		return ErrMissingMemberID
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin remove organization member: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var role string
	if err = tx.QueryRowContext(ctx, `
		SELECT role
		FROM organization_memberships
		WHERE organization_id = $1 AND member_id = $2 AND left_at IS NULL
		FOR UPDATE
	`, orgID, memberID).Scan(&role); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrMembershipNotFound
		}
		return fmt.Errorf("load membership before remove: %w", err)
	}
	if role == "owner" {
		ownerCount, countErr := activeOwnerCountTx(ctx, tx, orgID)
		if countErr != nil {
			return countErr
		}
		if ownerCount <= 1 {
			return ErrInvalidRoleChange
		}
	}

	res, err := tx.ExecContext(ctx, `
		UPDATE organization_memberships
		SET left_at = NOW()
		WHERE organization_id = $1 AND member_id = $2 AND left_at IS NULL
	`, orgID, memberID)
	if err != nil {
		return fmt.Errorf("remove organization member: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("remove organization member rows affected: %w", err)
	}
	if affected == 0 {
		return ErrMembershipNotFound
	}

	orgName := "your organization"
	if name, _, nameErr := organizationNameAndURLNameByID(ctx, tx, orgID); nameErr == nil {
		orgName = name
	}
	_ = createMemberNotification(
		ctx,
		tx,
		memberID,
		"You were removed from "+orgName+".",
		"",
	)

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit remove organization member: %w", err)
	}

	_ = removedBy
	return nil
}

// UpdateOrganizationMemberRole updates a membership role (member/owner).
func UpdateOrganizationMemberRole(ctx context.Context, db *sql.DB, orgID, memberID int64, makeOwner bool) error {
	if db == nil {
		return ErrNilDB
	}
	if orgID == 0 {
		return ErrMissingOrgID
	}
	if memberID == 0 {
		return ErrMissingMemberID
	}

	role := "member"
	if makeOwner {
		role = "owner"
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin update membership role: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var currentRole string
	if err = tx.QueryRowContext(ctx, `
		SELECT role
		FROM organization_memberships
		WHERE organization_id = $1 AND member_id = $2 AND left_at IS NULL
		FOR UPDATE
	`, orgID, memberID).Scan(&currentRole); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrMembershipNotFound
		}
		return fmt.Errorf("load membership role: %w", err)
	}
	if currentRole == "owner" && role != "owner" {
		ownerCount, countErr := activeOwnerCountTx(ctx, tx, orgID)
		if countErr != nil {
			return countErr
		}
		if ownerCount <= 1 {
			return ErrInvalidRoleChange
		}
	}

	res, err := tx.ExecContext(ctx, `
		UPDATE organization_memberships
		SET role = $1
		WHERE organization_id = $2 AND member_id = $3 AND left_at IS NULL
	`, role, orgID, memberID)
	if err != nil {
		return fmt.Errorf("update membership role: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update membership role rows affected: %w", err)
	}
	if affected == 0 {
		return ErrMembershipNotFound
	}

	orgName := "your organization"
	orgURLName := ""
	if name, urlName, nameErr := organizationNameAndURLNameByID(ctx, tx, orgID); nameErr == nil {
		orgName = name
		orgURLName = urlName
	}
	if currentRole != role {
		var text string
		href := ""
		if role == "owner" {
			text = "You are now an owner in " + orgName + "."
			if strings.TrimSpace(orgURLName) != "" {
				href = "/organization/" + orgURLName
			}
		} else if currentRole == "owner" && role == "member" {
			text = "Your owner role was revoked in " + orgName + "."
		}
		if text != "" {
			_ = createMemberNotification(ctx, tx, memberID, text, href)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit update membership role: %w", err)
	}
	return nil
}

func organizationNameAndURLNameByID(ctx context.Context, db queryer, orgID int64) (string, string, error) {
	var orgName string
	var orgURLName string
	if err := db.QueryRowContext(ctx, `
		SELECT name, url_name
		FROM organizations
		WHERE id = $1
	`, orgID).Scan(&orgName, &orgURLName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", ErrMissingOrgID
		}
		return "", "", fmt.Errorf("load organization details: %w", err)
	}
	return orgName, orgURLName, nil
}

func memberNameAndEmailByID(ctx context.Context, db queryer, memberID int64) (string, string, error) {
	var firstName string
	var lastName string
	var email string
	if err := db.QueryRowContext(ctx, `
		SELECT first_name, last_name, email
		FROM members
		WHERE id = $1
	`, memberID).Scan(&firstName, &lastName, &email); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", ErrMissingMemberID
		}
		return "", "", fmt.Errorf("load member details: %w", err)
	}
	return memberDisplayName(firstName, lastName, email), strings.TrimSpace(email), nil
}

func activeOwnerMemberIDsForOrganization(ctx context.Context, db *sql.DB, orgID int64) ([]int64, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT member_id
		FROM organization_memberships
		WHERE organization_id = $1
			AND role = 'owner'
			AND left_at IS NULL
		ORDER BY member_id
	`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list organization owners: %w", err)
	}
	defer rows.Close()

	var ownerIDs []int64
	for rows.Next() {
		var ownerID int64
		if err := rows.Scan(&ownerID); err != nil {
			return nil, fmt.Errorf("scan organization owner: %w", err)
		}
		ownerIDs = append(ownerIDs, ownerID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list organization owners: %w", err)
	}
	return ownerIDs, nil
}

func myHopshareOrgHref(orgID int64) string {
	if orgID <= 0 {
		return "/my-hopshare"
	}
	return "/my-hopshare?org_id=" + strconv.FormatInt(orgID, 10)
}

// UpdateOrganization updates basic organization fields.
func UpdateOrganization(ctx context.Context, db *sql.DB, org types.Organization) error {
	if db == nil {
		return ErrNilDB
	}
	if org.ID == 0 {
		return ErrMissingOrgID
	}

	name := strings.TrimSpace(org.Name)
	description := strings.TrimSpace(org.Description)
	if name == "" {
		return ErrMissingOrgName
	}
	if description == "" {
		return ErrMissingField
	}
	city := strings.TrimSpace(org.City)
	state := strings.TrimSpace(org.State)

	if _, err := db.ExecContext(ctx, `
		UPDATE organizations
		SET name = $1,
			city = $2,
			state = $3,
			description = $4,
			updated_at = NOW()
		WHERE id = $5
	`, name, city, state, description, org.ID); err != nil {
		return fmt.Errorf("update organization: %w", err)
	}
	return nil
}

// DeleteOrganization permanently removes an organization and all organization-scoped data.
// A message is sent to all active owners and members indicating who removed the organization.
func DeleteOrganization(ctx context.Context, db *sql.DB, orgID, actorMemberID int64) error {
	if db == nil {
		return ErrNilDB
	}
	if orgID == 0 {
		return ErrMissingOrgID
	}
	if actorMemberID == 0 {
		return ErrMissingMemberID
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete organization: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var orgName string
	if err = tx.QueryRowContext(ctx, `
		SELECT name
		FROM organizations
		WHERE id = $1
		FOR UPDATE
	`, orgID).Scan(&orgName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sql.ErrNoRows
		}
		return fmt.Errorf("load organization for delete: %w", err)
	}

	recipientRows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT member_id
		FROM organization_memberships
		WHERE organization_id = $1
			AND left_at IS NULL
		ORDER BY member_id
	`, orgID)
	if err != nil {
		return fmt.Errorf("list organization recipients for delete message: %w", err)
	}

	recipientIDs := make([]int64, 0)
	for recipientRows.Next() {
		var recipientID int64
		if err := recipientRows.Scan(&recipientID); err != nil {
			_ = recipientRows.Close()
			return fmt.Errorf("scan organization recipient for delete message: %w", err)
		}
		recipientIDs = append(recipientIDs, recipientID)
	}
	if err := recipientRows.Err(); err != nil {
		_ = recipientRows.Close()
		return fmt.Errorf("list organization recipients for delete message: %w", err)
	}
	if err := recipientRows.Close(); err != nil {
		return fmt.Errorf("close organization recipients for delete message: %w", err)
	}

	actorName := "A user"
	if displayName, _, nameErr := memberNameAndEmailByID(ctx, tx, actorMemberID); nameErr == nil && strings.TrimSpace(displayName) != "" {
		actorName = displayName
	}

	if _, err = tx.ExecContext(ctx, `
		DELETE FROM organizations
		WHERE id = $1
	`, orgID); err != nil {
		return fmt.Errorf("delete organization: %w", err)
	}

	body := "User " + actorName + " has permanently removed the Organization " + orgName + "."
	for _, recipientID := range recipientIDs {
		if err := createMemberNotification(ctx, tx, recipientID, body, ""); err != nil {
			return fmt.Errorf("create organization delete notification: %w", err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit delete organization: %w", err)
	}
	return nil
}

// UpdateOrganizationTimebankPolicy updates min/max/starting balance settings for an organization.
func UpdateOrganizationTimebankPolicy(ctx context.Context, db *sql.DB, orgID int64, minBalance, maxBalance, startingBalance int) error {
	if db == nil {
		return ErrNilDB
	}
	if orgID == 0 {
		return ErrMissingOrgID
	}

	policy := timebankPolicy{
		MinBalance:      minBalance,
		MaxBalance:      maxBalance,
		StartingBalance: startingBalance,
	}
	if err := validateTimebankPolicy(policy); err != nil {
		return err
	}

	if _, err := db.ExecContext(ctx, `
		UPDATE organizations
		SET timebank_min_balance = $1,
			timebank_max_balance = $2,
			timebank_starting_balance = $3,
			updated_at = NOW()
		WHERE id = $4
	`, policy.MinBalance, policy.MaxBalance, policy.StartingBalance, orgID); err != nil {
		return fmt.Errorf("update organization timebank policy: %w", err)
	}
	return nil
}

// CreateOrganization inserts an organization and adds the creator as an owner.
func CreateOrganization(ctx context.Context, db *sql.DB, name, city, state, description string, creatorMemberID int64) (types.Organization, error) {
	if db == nil {
		return types.Organization{}, ErrNilDB
	}
	name = strings.TrimSpace(name)
	city = strings.TrimSpace(city)
	state = strings.TrimSpace(state)
	description = strings.TrimSpace(description)
	if name == "" {
		return types.Organization{}, ErrMissingOrgName
	}
	if description == "" {
		return types.Organization{}, ErrMissingField
	}
	if creatorMemberID == 0 {
		return types.Organization{}, ErrMissingMemberID
	}
	defaultPolicy := timebankPolicy{
		MinBalance:      DefaultTimebankMinBalance,
		MaxBalance:      DefaultTimebankMaxBalance,
		StartingBalance: DefaultTimebankStartingBalance,
	}
	if err := validateTimebankPolicy(defaultPolicy); err != nil {
		return types.Organization{}, err
	}

	var alreadyCreatedOrganization bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM organizations
			WHERE created_by = $1
		)
	`, creatorMemberID).Scan(&alreadyCreatedOrganization); err != nil {
		return types.Organization{}, fmt.Errorf("check existing organization creator: %w", err)
	}
	if alreadyCreatedOrganization {
		return types.Organization{}, ErrOrganizationAlreadyCreated
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return types.Organization{}, fmt.Errorf("begin create org: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var org types.Organization
	for i := 0; i < organizationURLNameMaxAttempts; i++ {
		urlName, urlErr := ensureUniqueOrganizationURLName(ctx, tx, name)
		if urlErr != nil {
			return types.Organization{}, urlErr
		}

		row := tx.QueryRowContext(ctx, `
				INSERT INTO organizations (
					name, url_name, city, state, description, timebank_min_balance, timebank_max_balance, timebank_starting_balance, created_by
				)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
				RETURNING id, name, url_name, city, state, description, timebank_min_balance, timebank_max_balance, timebank_starting_balance, logo_content_type, (logo_data IS NOT NULL), enabled, created_by, created_at, updated_at
			`, name, urlName, city, state, description, defaultPolicy.MinBalance, defaultPolicy.MaxBalance, defaultPolicy.StartingBalance, creatorMemberID)
		if err = row.Scan(&org.ID, &org.Name, &org.URLName, &org.City, &org.State, &org.Description, &org.TimebankMinBalance, &org.TimebankMaxBalance, &org.TimebankStartingBalance, &org.LogoContentType, &org.HasLogo, &org.Enabled, &org.CreatedBy, &org.CreatedAt, &org.UpdatedAt); err != nil {
			if isUniqueConstraintViolation(err, organizationURLNameUniqueConstraint) {
				continue
			}
			return types.Organization{}, fmt.Errorf("insert organization: %w", err)
		}
		break
	}
	if org.ID == 0 {
		return types.Organization{}, fmt.Errorf("insert organization: could not generate unique url name")
	}

	if _, err = tx.ExecContext(ctx, `
		INSERT INTO organization_memberships (organization_id, member_id, role)
		VALUES ($1, $2, 'owner')
	`, org.ID, creatorMemberID); err != nil {
		return types.Organization{}, fmt.Errorf("add organization owner: %w", err)
	}

	if org.TimebankStartingBalance > 0 {
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO hour_balance_adjustments (
				organization_id,
				member_id,
				admin_member_id,
				hours_delta,
				reason,
				is_starting_balance
			)
			VALUES ($1, $2, $3, $4, $5, TRUE)
		`, org.ID, creatorMemberID, creatorMemberID, org.TimebankStartingBalance, "Organization starting balance"); err != nil {
			return types.Organization{}, fmt.Errorf("insert owner starting balance adjustment: %w", err)
		}
	}

	if err = tx.Commit(); err != nil {
		return types.Organization{}, fmt.Errorf("commit organization: %w", err)
	}

	return org, nil
}

func ensureUniqueOrganizationURLName(ctx context.Context, tx *sql.Tx, name string) (string, error) {
	base := normalizeOrganizationURLName(name)
	for ordinal := 1; ordinal <= organizationURLNameMaxAttempts; ordinal++ {
		candidate := organizationURLNameWithOrdinal(base, ordinal)
		var exists bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM organizations WHERE url_name = $1
			)
		`, candidate).Scan(&exists); err != nil {
			return "", fmt.Errorf("check organization url name: %w", err)
		}
		if !exists {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("check organization url name: exhausted attempts")
}

func normalizeOrganizationURLName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return organizationURLNameDefault
	}

	var b strings.Builder
	b.Grow(len(name))
	lastWasDash := false
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastWasDash = false
			continue
		}
		if b.Len() == 0 || lastWasDash {
			continue
		}
		b.WriteByte('-')
		lastWasDash = true
	}

	normalized := strings.Trim(b.String(), "-")
	if normalized == "" {
		normalized = organizationURLNameDefault
	}
	if len(normalized) > organizationURLNameMaxLen {
		normalized = strings.TrimRight(normalized[:organizationURLNameMaxLen], "-")
	}
	if normalized == "" {
		normalized = organizationURLNameDefault
	}
	return normalized
}

func organizationURLNameWithOrdinal(base string, ordinal int) string {
	if ordinal <= 1 {
		return base
	}

	suffix := fmt.Sprintf("-%d", ordinal)
	maxBaseLen := organizationURLNameMaxLen - len(suffix)
	if maxBaseLen < 1 {
		return suffix[1:]
	}

	candidateBase := strings.Trim(base, "-")
	if len(candidateBase) > maxBaseLen {
		candidateBase = strings.TrimRight(candidateBase[:maxBaseLen], "-")
	}
	if candidateBase == "" {
		candidateBase = organizationURLNameDefault
		if len(candidateBase) > maxBaseLen {
			candidateBase = candidateBase[:maxBaseLen]
		}
		candidateBase = strings.TrimRight(candidateBase, "-")
		if candidateBase == "" {
			candidateBase = "o"
		}
	}
	return candidateBase + suffix
}

func isUniqueConstraintViolation(err error, constraint string) bool {
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		return false
	}
	if pqErr.Code != "23505" {
		return false
	}
	return constraint == "" || pqErr.Constraint == constraint
}
