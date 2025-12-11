package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"hopshare/internal/types"
)

var (
	ErrNilDB                = errors.New("db is nil")
	ErrInvalidContactMethod = errors.New("invalid preferred contact method")
	ErrMissingField         = errors.New("missing required field")
	ErrMissingMemberID      = errors.New("member id is required")
	ErrMissingOrgName       = errors.New("organization name is required")
	ErrMissingOrgID         = errors.New("organization id is required")
	ErrInvalidCredentials   = errors.New("invalid email or password")
	ErrRequestAlreadyExists = errors.New("membership request already exists")
	ErrAlreadyPrimaryOwner  = errors.New("member already manages an organization")
	ErrRequestNotFound      = errors.New("membership request not found")
	ErrMembershipNotFound   = errors.New("membership not found")
	ErrInvalidRoleChange    = errors.New("invalid role change")
)

// CreateMember inserts a member row and returns the stored record with ID/timestamps.
func CreateMember(ctx context.Context, db *sql.DB, m types.Member) (types.Member, error) {
	if db == nil {
		return types.Member{}, ErrNilDB
	}
	if err := validateMemberInput(m); err != nil {
		return types.Member{}, err
	}

	stmt := `
		INSERT INTO members (
			username,
			email,
			password_hash,
			preferred_contact_method,
			preferred_contact,
			profile_picture_url,
			city,
			state,
			interests,
			enabled,
			verified
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, created_at, updated_at`

	row := db.QueryRowContext(
		ctx,
		stmt,
		m.Username,
		m.Email,
		m.PasswordHash,
		m.PreferredContactMethod,
		m.PreferredContact,
		m.ProfilePictureURL,
		m.City,
		m.State,
		m.Interests,
		m.Enabled,
		m.Verified,
	)

	if err := row.Scan(&m.ID, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return types.Member{}, fmt.Errorf("create member: %w", err)
	}

	return m, nil
}

func validateMemberInput(m types.Member) error {
	if m.Username == "" || m.Email == "" || m.PasswordHash == "" || m.PreferredContactMethod == "" || m.PreferredContact == "" {
		return ErrMissingField
	}

	switch m.PreferredContactMethod {
	case types.ContactMethodEmail, types.ContactMethodPhone, types.ContactMethodOther:
	default:
		return ErrInvalidContactMethod
	}

	return nil
}

// HashPassword returns a bcrypt hash for the provided password string.
func HashPassword(pw string) (string, error) {
	hashed, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hashed), nil
}

// AuthenticateMember validates credentials and returns the member record.
func AuthenticateMember(ctx context.Context, db *sql.DB, email, password string) (types.Member, error) {
	member, err := GetMemberByEmail(ctx, db, email)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return types.Member{}, ErrInvalidCredentials
		}
		return types.Member{}, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(member.PasswordHash), []byte(password)); err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) || errors.Is(err, bcrypt.ErrHashTooShort) {
			return types.Member{}, ErrInvalidCredentials
		}
		return types.Member{}, fmt.Errorf("verify password: %w", err)
	}
	return member, nil
}

// GetMemberByID fetches a member by ID.
func GetMemberByID(ctx context.Context, db *sql.DB, memberID int64) (types.Member, error) {
	if db == nil {
		return types.Member{}, ErrNilDB
	}
	if memberID == 0 {
		return types.Member{}, ErrMissingMemberID
	}

	row := db.QueryRowContext(ctx, `
		SELECT id, username, email, password_hash, preferred_contact_method, preferred_contact, profile_picture_url, city, state, interests, enabled, verified, created_at, updated_at
		FROM members
		WHERE id = $1
	`, memberID)

	var m types.Member
	if err := row.Scan(
		&m.ID,
		&m.Username,
		&m.Email,
		&m.PasswordHash,
		&m.PreferredContactMethod,
		&m.PreferredContact,
		&m.ProfilePictureURL,
		&m.City,
		&m.State,
		&m.Interests,
		&m.Enabled,
		&m.Verified,
		&m.CreatedAt,
		&m.UpdatedAt,
	); err != nil {
		return types.Member{}, fmt.Errorf("get member by id: %w", err)
	}

	return m, nil
}

// GetMemberByEmail fetches a member by email (case-insensitive).
func GetMemberByEmail(ctx context.Context, db *sql.DB, email string) (types.Member, error) {
	if db == nil {
		return types.Member{}, ErrNilDB
	}
	email = strings.TrimSpace(email)
	if email == "" {
		return types.Member{}, ErrMissingField
	}

	row := db.QueryRowContext(ctx, `
		SELECT id, username, email, password_hash, preferred_contact_method, preferred_contact, profile_picture_url, city, state, interests, enabled, verified, created_at, updated_at
		FROM members
		WHERE LOWER(email) = LOWER($1)
	`, email)

	var m types.Member
	if err := row.Scan(
		&m.ID,
		&m.Username,
		&m.Email,
		&m.PasswordHash,
		&m.PreferredContactMethod,
		&m.PreferredContact,
		&m.ProfilePictureURL,
		&m.City,
		&m.State,
		&m.Interests,
		&m.Enabled,
		&m.Verified,
		&m.CreatedAt,
		&m.UpdatedAt,
	); err != nil {
		return types.Member{}, fmt.Errorf("get member by email: %w", err)
	}

	return m, nil
}

// PrimaryOwnedOrganization returns the organization where the member is the primary owner.
func PrimaryOwnedOrganization(ctx context.Context, db *sql.DB, memberID int64) (types.Organization, error) {
	if db == nil {
		return types.Organization{}, ErrNilDB
	}
	if memberID == 0 {
		return types.Organization{}, ErrMissingMemberID
	}

	row := db.QueryRowContext(ctx, `
		SELECT o.id, o.name, o.logo_url, o.enabled, o.created_by, o.created_at, o.updated_at
		FROM organizations o
		JOIN organization_memberships om ON om.organization_id = o.id
		WHERE om.member_id = $1 AND om.is_primary_owner = TRUE AND om.left_at IS NULL
	`, memberID)

	var o types.Organization
	if err := row.Scan(&o.ID, &o.Name, &o.LogoURL, &o.Enabled, &o.CreatedBy, &o.CreatedAt, &o.UpdatedAt); err != nil {
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
		SELECT o.id, o.name, o.logo_url, o.enabled, o.created_by, o.created_at, o.updated_at
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
		if err := rows.Scan(&o.ID, &o.Name, &o.LogoURL, &o.Enabled, &o.CreatedBy, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan organization: %w", err)
		}
		orgs = append(orgs, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list member organizations: %w", err)
	}

	return orgs, nil
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
		SELECT id, name, logo_url, enabled, created_by, created_at, updated_at
		FROM organizations
		WHERE id = $1
	`, orgID)

	var o types.Organization
	if err := row.Scan(&o.ID, &o.Name, &o.LogoURL, &o.Enabled, &o.CreatedBy, &o.CreatedAt, &o.UpdatedAt); err != nil {
		return types.Organization{}, fmt.Errorf("get organization by id: %w", err)
	}
	return o, nil
}

// ListOrganizations returns all organizations ordered by name.
func ListOrganizations(ctx context.Context, db *sql.DB) ([]types.Organization, error) {
	if db == nil {
		return nil, ErrNilDB
	}

	rows, err := db.QueryContext(ctx, `
		SELECT id, name, logo_url, enabled, created_by, created_at, updated_at
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
		if err := rows.Scan(&o.ID, &o.Name, &o.LogoURL, &o.Enabled, &o.CreatedBy, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan organization: %w", err)
		}
		orgs = append(orgs, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list organizations: %w", err)
	}

	return orgs, nil
}

// UpdateMemberPassword updates a member's password hash.
func UpdateMemberPassword(ctx context.Context, db *sql.DB, memberID int64, passwordHash string) error {
	if db == nil {
		return ErrNilDB
	}
	if memberID == 0 {
		return ErrMissingMemberID
	}
	if passwordHash == "" {
		return ErrMissingField
	}

	res, err := db.ExecContext(ctx, `
		UPDATE members
		SET password_hash = $1, updated_at = NOW()
		WHERE id = $2
	`, passwordHash, memberID)
	if err != nil {
		return fmt.Errorf("update member password: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update member password rows affected: %w", err)
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
	if name == "" {
		return ErrMissingOrgName
	}

	if _, err := db.ExecContext(ctx, `
		UPDATE organizations
		SET name = $1, logo_url = $2, updated_at = NOW()
		WHERE id = $3
	`, name, org.LogoURL, org.ID); err != nil {
		return fmt.Errorf("update organization: %w", err)
	}
	return nil
}

// CreateOrganization inserts an organization and sets the given member as the primary owner.
func CreateOrganization(ctx context.Context, db *sql.DB, name string, primaryOwnerMemberID int64, logoURL *string) (types.Organization, error) {
	if db == nil {
		return types.Organization{}, ErrNilDB
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return types.Organization{}, ErrMissingOrgName
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
	row := tx.QueryRowContext(ctx, `
		INSERT INTO organizations (name, logo_url, created_by)
		VALUES ($1, $2, $3)
		RETURNING id, name, logo_url, enabled, created_by, created_at, updated_at
	`, name, logoURL, primaryOwnerMemberID)
	if err = row.Scan(&org.ID, &org.Name, &org.LogoURL, &org.Enabled, &org.CreatedBy, &org.CreatedAt, &org.UpdatedAt); err != nil {
		return types.Organization{}, fmt.Errorf("insert organization: %w", err)
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
