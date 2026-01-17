package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

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

	ErrHopNotFound     = errors.New("hop not found")
	ErrHopForbidden    = errors.New("hop forbidden")
	ErrHopInvalidState = errors.New("hop invalid state")
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
		SELECT id, username, email, password_hash, preferred_contact_method, preferred_contact, profile_picture_url, city, state, interests, enabled, verified, last_login_at, created_at, updated_at
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
		&m.LastLoginAt,
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
		SELECT id, username, email, password_hash, preferred_contact_method, preferred_contact, profile_picture_url, city, state, interests, enabled, verified, last_login_at, created_at, updated_at
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
		&m.LastLoginAt,
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
		SELECT o.id, o.name, o.logo_content_type, (o.logo_data IS NOT NULL), o.enabled, o.created_by, o.created_at, o.updated_at
		FROM organizations o
		JOIN organization_memberships om ON om.organization_id = o.id
		WHERE om.member_id = $1 AND om.is_primary_owner = TRUE AND om.left_at IS NULL
	`, memberID)

	var o types.Organization
	if err := row.Scan(&o.ID, &o.Name, &o.LogoContentType, &o.HasLogo, &o.Enabled, &o.CreatedBy, &o.CreatedAt, &o.UpdatedAt); err != nil {
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
		SELECT o.id, o.name, o.logo_content_type, (o.logo_data IS NOT NULL), o.enabled, o.created_by, o.created_at, o.updated_at
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
		if err := rows.Scan(&o.ID, &o.Name, &o.LogoContentType, &o.HasLogo, &o.Enabled, &o.CreatedBy, &o.CreatedAt, &o.UpdatedAt); err != nil {
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
		SELECT id, name, logo_content_type, (logo_data IS NOT NULL), enabled, created_by, created_at, updated_at
		FROM organizations
		WHERE id = $1
	`, orgID)

	var o types.Organization
	if err := row.Scan(&o.ID, &o.Name, &o.LogoContentType, &o.HasLogo, &o.Enabled, &o.CreatedBy, &o.CreatedAt, &o.UpdatedAt); err != nil {
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
		SELECT id, name, logo_content_type, (logo_data IS NOT NULL), enabled, created_by, created_at, updated_at
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
		if err := rows.Scan(&o.ID, &o.Name, &o.LogoContentType, &o.HasLogo, &o.Enabled, &o.CreatedBy, &o.CreatedAt, &o.UpdatedAt); err != nil {
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

func UpdateMemberLastLogin(ctx context.Context, db *sql.DB, memberID int64, at time.Time) error {
	if db == nil {
		return ErrNilDB
	}
	if memberID == 0 {
		return ErrMissingMemberID
	}

	res, err := db.ExecContext(ctx, `
		UPDATE members
		SET last_login_at = $1, updated_at = NOW()
		WHERE id = $2
	`, at, memberID)
	if err != nil {
		return fmt.Errorf("update member last login: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update member last login rows affected: %w", err)
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
		SET name = $1, updated_at = NOW()
		WHERE id = $2
	`, name, org.ID); err != nil {
		return fmt.Errorf("update organization: %w", err)
	}
	return nil
}

// CreateOrganization inserts an organization and sets the given member as the primary owner.
func CreateOrganization(ctx context.Context, db *sql.DB, name string, primaryOwnerMemberID int64) (types.Organization, error) {
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
		INSERT INTO organizations (name, created_by)
		VALUES ($1, $2)
		RETURNING id, name, logo_content_type, (logo_data IS NOT NULL), enabled, created_by, created_at, updated_at
	`, name, primaryOwnerMemberID)
	if err = row.Scan(&org.ID, &org.Name, &org.LogoContentType, &org.HasLogo, &org.Enabled, &org.CreatedBy, &org.CreatedAt, &org.UpdatedAt); err != nil {
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

func requireActiveMembership(ctx context.Context, db *sql.DB, orgID, memberID int64) error {
	var exists bool
	if err := db.QueryRowContext(ctx, `
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
