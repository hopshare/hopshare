package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

// PrimaryOwnedOrganization returns the organization where the member is the primary owner.
func PrimaryOwnedOrganization(ctx context.Context, db *sql.DB, memberID int64) (types.Organization, error) {
	if db == nil {
		return types.Organization{}, ErrNilDB
	}
	if memberID == 0 {
		return types.Organization{}, ErrMissingMemberID
	}

	row := db.QueryRowContext(ctx, `
		SELECT o.id, o.name, o.url_name, o.city, o.state, o.description, o.logo_content_type, (o.logo_data IS NOT NULL), o.enabled, o.created_by, o.created_at, o.updated_at
		FROM organizations o
		JOIN organization_memberships om ON om.organization_id = o.id
		WHERE om.member_id = $1 AND om.is_primary_owner = TRUE AND om.left_at IS NULL
	`, memberID)

	var o types.Organization
	if err := row.Scan(&o.ID, &o.Name, &o.URLName, &o.City, &o.State, &o.Description, &o.LogoContentType, &o.HasLogo, &o.Enabled, &o.CreatedBy, &o.CreatedAt, &o.UpdatedAt); err != nil {
		return types.Organization{}, err
	}
	return o, nil
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
		SELECT o.id, o.name, o.url_name, o.city, o.state, o.description, o.logo_content_type, (o.logo_data IS NOT NULL), o.enabled, o.created_by, o.created_at, o.updated_at
		FROM organizations o
		JOIN organization_memberships om ON om.organization_id = o.id
		WHERE om.member_id = $1 AND om.left_at IS NULL
		ORDER BY o.name
	`, memberID)
	if err != nil {
		return nil, fmt.Errorf("list member organizations: %w", err)
	}
	defer rows.Close()

	var orgs []types.Organization
	for rows.Next() {
		var o types.Organization
		if err := rows.Scan(&o.ID, &o.Name, &o.URLName, &o.City, &o.State, &o.Description, &o.LogoContentType, &o.HasLogo, &o.Enabled, &o.CreatedBy, &o.CreatedAt, &o.UpdatedAt); err != nil {
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
		SELECT o.id, o.name, o.url_name, o.city, o.state, o.description, o.logo_content_type, (o.logo_data IS NOT NULL), o.enabled, o.created_by, o.created_at, o.updated_at,
		       om.role, om.is_primary_owner
		FROM organizations o
		JOIN organization_memberships om ON om.organization_id = o.id
		WHERE om.member_id = $1 AND om.left_at IS NULL
		ORDER BY o.name
	`, memberID)
	if err != nil {
		return nil, fmt.Errorf("list member organizations with roles: %w", err)
	}
	defer rows.Close()

	var orgs []types.MemberOrganization
	for rows.Next() {
		var o types.MemberOrganization
		if err := rows.Scan(&o.ID, &o.Name, &o.URLName, &o.City, &o.State, &o.Description, &o.LogoContentType, &o.HasLogo, &o.Enabled, &o.CreatedBy, &o.CreatedAt, &o.UpdatedAt, &o.Role, &o.IsPrimaryOwner); err != nil {
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
		SELECT o.id, o.name, o.url_name, o.city, o.state, o.description, o.logo_content_type, (o.logo_data IS NOT NULL), o.enabled, o.created_by, o.created_at, o.updated_at
		FROM organizations o
		JOIN organization_memberships om ON om.organization_id = o.id
		WHERE om.member_id = $1 AND om.organization_id = $2 AND om.role = 'owner' AND om.left_at IS NULL
	`, memberID, orgID)

	var o types.Organization
	if err := row.Scan(&o.ID, &o.Name, &o.URLName, &o.City, &o.State, &o.Description, &o.LogoContentType, &o.HasLogo, &o.Enabled, &o.CreatedBy, &o.CreatedAt, &o.UpdatedAt); err != nil {
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
			FROM organization_memberships
			WHERE member_id = $1 AND organization_id = $2 AND left_at IS NULL
		)
	`, memberID, orgID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check member organization membership: %w", err)
	}
	return exists, nil
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
			WHERE om1.member_id = $1
				AND om2.member_id = $2
				AND om1.left_at IS NULL
				AND om2.left_at IS NULL
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
		SELECT id, name, url_name, city, state, description, logo_content_type, (logo_data IS NOT NULL), enabled, created_by, created_at, updated_at
		FROM organizations
		WHERE id = $1
	`, orgID)

	var o types.Organization
	if err := row.Scan(&o.ID, &o.Name, &o.URLName, &o.City, &o.State, &o.Description, &o.LogoContentType, &o.HasLogo, &o.Enabled, &o.CreatedBy, &o.CreatedAt, &o.UpdatedAt); err != nil {
		return types.Organization{}, fmt.Errorf("get organization by id: %w", err)
	}
	return o, nil
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
		SELECT id, name, url_name, city, state, description, logo_content_type, (logo_data IS NOT NULL), enabled, created_by, created_at, updated_at
		FROM organizations
		WHERE url_name = $1
	`, urlName)

	var o types.Organization
	if err := row.Scan(&o.ID, &o.Name, &o.URLName, &o.City, &o.State, &o.Description, &o.LogoContentType, &o.HasLogo, &o.Enabled, &o.CreatedBy, &o.CreatedAt, &o.UpdatedAt); err != nil {
		return types.Organization{}, fmt.Errorf("get organization by url name: %w", err)
	}
	return o, nil
}

// ListOrganizations returns all organizations ordered by name.
func ListOrganizations(ctx context.Context, db *sql.DB) ([]types.Organization, error) {
	if db == nil {
		return nil, ErrNilDB
	}

	rows, err := db.QueryContext(ctx, `
		SELECT id, name, url_name, city, state, description, logo_content_type, (logo_data IS NOT NULL), enabled, created_by, created_at, updated_at
		FROM organizations
		ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("list organizations: %w", err)
	}
	defer rows.Close()

	var orgs []types.Organization
	for rows.Next() {
		var o types.Organization
		if err := rows.Scan(&o.ID, &o.Name, &o.URLName, &o.City, &o.State, &o.Description, &o.LogoContentType, &o.HasLogo, &o.Enabled, &o.CreatedBy, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan organization: %w", err)
		}
		orgs = append(orgs, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list organizations: %w", err)
	}

	return orgs, nil
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
		SELECT mr.id, mr.organization_id, mr.member_id, m.username, m.email, mr.requested_at, mr.status
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

	var exists bool
	if err = tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM organization_memberships
			WHERE organization_id = $1 AND member_id = $2 AND left_at IS NULL
		)
	`, orgID, memberID).Scan(&exists); err != nil {
		return fmt.Errorf("check existing membership: %w", err)
	}
	if !exists {
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO organization_memberships (organization_id, member_id, role, is_primary_owner)
			VALUES ($1, $2, 'member', FALSE)
		`, orgID, memberID); err != nil {
			return fmt.Errorf("create membership: %w", err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit approve request: %w", err)
	}

	// TODO: notify requester of approval (email/notification).
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

	res, err := db.ExecContext(ctx, `
		UPDATE membership_requests
		SET status = 'rejected', decided_at = NOW(), decided_by = $1
		WHERE id = $2 AND status = 'pending'
	`, decidedBy, requestID)
	if err != nil {
		return fmt.Errorf("deny membership request: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("deny membership request rows affected: %w", err)
	}
	if affected == 0 {
		return ErrRequestNotFound
	}

	// TODO: notify requester of rejection.
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
		SELECT m.id, m.username, m.email, om.role, om.is_primary_owner, om.joined_at
		FROM organization_memberships om
		JOIN members m ON m.id = om.member_id
		WHERE om.organization_id = $1 AND om.left_at IS NULL
		ORDER BY om.is_primary_owner DESC, om.role DESC, m.username ASC
	`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list organization members: %w", err)
	}
	defer rows.Close()

	var members []types.OrganizationMember
	for rows.Next() {
		var om types.OrganizationMember
		if err := rows.Scan(&om.MemberID, &om.Username, &om.Email, &om.Role, &om.IsPrimaryOwner, &om.JoinedAt); err != nil {
			return nil, fmt.Errorf("scan organization member: %w", err)
		}
		members = append(members, om)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list organization members: %w", err)
	}
	return members, nil
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

	res, err := db.ExecContext(ctx, `
		UPDATE organization_memberships
		SET left_at = NOW()
		WHERE organization_id = $1 AND member_id = $2 AND left_at IS NULL AND is_primary_owner = FALSE
	`, orgID, memberID)
	if err != nil {
		return fmt.Errorf("remove organization member: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("remove organization member rows affected: %w", err)
	}
	if affected == 0 {
		var isPrimaryOwner bool
		var exists bool
		if err := db.QueryRowContext(ctx, `
			SELECT
				EXISTS (
					SELECT 1
					FROM organization_memberships
					WHERE organization_id = $1 AND member_id = $2 AND left_at IS NULL
				),
				EXISTS (
					SELECT 1
					FROM organization_memberships
					WHERE organization_id = $1 AND member_id = $2 AND left_at IS NULL AND is_primary_owner = TRUE
				)
		`, orgID, memberID).Scan(&exists, &isPrimaryOwner); err != nil {
			return fmt.Errorf("check organization membership before remove: %w", err)
		}
		if isPrimaryOwner {
			return ErrInvalidRoleChange
		}
		if !exists {
			return ErrMembershipNotFound
		}
		return ErrMembershipNotFound
	}

	// TODO: notify member of removal; audit removedBy if needed.
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

	res, err := db.ExecContext(ctx, `
		UPDATE organization_memberships
		SET role = $1, is_primary_owner = CASE WHEN is_primary_owner THEN TRUE ELSE FALSE END
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
	return nil
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

// CreateOrganization inserts an organization and sets the given member as the primary owner.
func CreateOrganization(ctx context.Context, db *sql.DB, name, city, state, description string, primaryOwnerMemberID int64) (types.Organization, error) {
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
	if primaryOwnerMemberID == 0 {
		return types.Organization{}, ErrMissingMemberID
	}

	if _, err := PrimaryOwnedOrganization(ctx, db, primaryOwnerMemberID); err == nil {
		return types.Organization{}, ErrAlreadyPrimaryOwner
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return types.Organization{}, err
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
			INSERT INTO organizations (name, url_name, city, state, description, created_by)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id, name, url_name, city, state, description, logo_content_type, (logo_data IS NOT NULL), enabled, created_by, created_at, updated_at
		`, name, urlName, city, state, description, primaryOwnerMemberID)
		if err = row.Scan(&org.ID, &org.Name, &org.URLName, &org.City, &org.State, &org.Description, &org.LogoContentType, &org.HasLogo, &org.Enabled, &org.CreatedBy, &org.CreatedAt, &org.UpdatedAt); err != nil {
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
		INSERT INTO organization_memberships (organization_id, member_id, role, is_primary_owner)
		VALUES ($1, $2, 'owner', TRUE)
	`, org.ID, primaryOwnerMemberID); err != nil {
		return types.Organization{}, fmt.Errorf("add primary owner: %w", err)
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
