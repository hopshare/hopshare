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
		SELECT id,
			username,
			email,
			password_hash,
			preferred_contact_method,
			preferred_contact,
			profile_picture_url,
			avatar_content_type,
			(avatar_data IS NOT NULL),
			city,
			state,
			interests,
			current_organization,
			enabled,
			verified,
			last_login_at,
			created_at,
			updated_at
		FROM members
		WHERE id = $1
	`, memberID)

	var m types.Member
	var currentOrg sql.NullInt64
	var avatarContentType sql.NullString
	var hasAvatar bool
	if err := row.Scan(
		&m.ID,
		&m.Username,
		&m.Email,
		&m.PasswordHash,
		&m.PreferredContactMethod,
		&m.PreferredContact,
		&m.ProfilePictureURL,
		&avatarContentType,
		&hasAvatar,
		&m.City,
		&m.State,
		&m.Interests,
		&currentOrg,
		&m.Enabled,
		&m.Verified,
		&m.LastLoginAt,
		&m.CreatedAt,
		&m.UpdatedAt,
	); err != nil {
		return types.Member{}, fmt.Errorf("get member by id: %w", err)
	}
	if currentOrg.Valid {
		m.CurrentOrganization = &currentOrg.Int64
	}
	if avatarContentType.Valid {
		m.AvatarContentType = &avatarContentType.String
	}
	m.HasAvatar = hasAvatar

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
		SELECT id,
			username,
			email,
			password_hash,
			preferred_contact_method,
			preferred_contact,
			profile_picture_url,
			avatar_content_type,
			(avatar_data IS NOT NULL),
			city,
			state,
			interests,
			current_organization,
			enabled,
			verified,
			last_login_at,
			created_at,
			updated_at
		FROM members
		WHERE LOWER(email) = LOWER($1)
	`, email)

	var m types.Member
	var currentOrg sql.NullInt64
	var avatarContentType sql.NullString
	var hasAvatar bool
	if err := row.Scan(
		&m.ID,
		&m.Username,
		&m.Email,
		&m.PasswordHash,
		&m.PreferredContactMethod,
		&m.PreferredContact,
		&m.ProfilePictureURL,
		&avatarContentType,
		&hasAvatar,
		&m.City,
		&m.State,
		&m.Interests,
		&currentOrg,
		&m.Enabled,
		&m.Verified,
		&m.LastLoginAt,
		&m.CreatedAt,
		&m.UpdatedAt,
	); err != nil {
		return types.Member{}, fmt.Errorf("get member by email: %w", err)
	}
	if currentOrg.Valid {
		m.CurrentOrganization = &currentOrg.Int64
	}
	if avatarContentType.Valid {
		m.AvatarContentType = &avatarContentType.String
	}
	m.HasAvatar = hasAvatar

	return m, nil
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

func UpdateMemberCurrentOrganization(ctx context.Context, db *sql.DB, memberID, orgID int64) error {
	if db == nil {
		return ErrNilDB
	}
	if memberID == 0 {
		return ErrMissingMemberID
	}
	if orgID == 0 {
		return ErrMissingOrgID
	}

	res, err := db.ExecContext(ctx, `
		UPDATE members
		SET current_organization = $1, updated_at = NOW()
		WHERE id = $2
	`, orgID, memberID)
	if err != nil {
		return fmt.Errorf("update member current organization: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update member current organization rows affected: %w", err)
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// UpdateMemberProfile updates a member's profile details.
func UpdateMemberProfile(ctx context.Context, db *sql.DB, memberID int64, email, preferredContactMethod, preferredContact, city, state string) error {
	if db == nil {
		return ErrNilDB
	}
	if memberID == 0 {
		return ErrMissingMemberID
	}
	email = strings.TrimSpace(email)
	preferredContactMethod = strings.TrimSpace(preferredContactMethod)
	preferredContact = strings.TrimSpace(preferredContact)
	city = strings.TrimSpace(city)
	state = strings.TrimSpace(state)

	if email == "" || preferredContactMethod == "" || preferredContact == "" {
		return ErrMissingField
	}
	switch preferredContactMethod {
	case types.ContactMethodEmail, types.ContactMethodPhone, types.ContactMethodOther:
	default:
		return ErrInvalidContactMethod
	}

	res, err := db.ExecContext(ctx, `
		UPDATE members
		SET email = $1,
			preferred_contact_method = $2,
			preferred_contact = $3,
			city = $4,
			state = $5,
			updated_at = NOW()
		WHERE id = $6
	`, email, preferredContactMethod, preferredContact, nullableMemberString(city), nullableMemberString(state), memberID)
	if err != nil {
		return fmt.Errorf("update member profile: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update member profile rows affected: %w", err)
	}
	if affected == 0 {
		return sql.ErrNoRows
	}

	return nil
}

// MemberAvatar returns avatar data for a member.
func MemberAvatar(ctx context.Context, db *sql.DB, memberID int64) ([]byte, string, bool, error) {
	if db == nil {
		return nil, "", false, ErrNilDB
	}
	if memberID == 0 {
		return nil, "", false, ErrMissingMemberID
	}

	row := db.QueryRowContext(ctx, `
		SELECT avatar_content_type, avatar_data
		FROM members
		WHERE id = $1
	`, memberID)

	var contentType sql.NullString
	var data []byte
	if err := row.Scan(&contentType, &data); err != nil {
		return nil, "", false, fmt.Errorf("get member avatar: %w", err)
	}
	if !contentType.Valid || len(data) == 0 {
		return nil, "", false, nil
	}
	return data, contentType.String, true, nil
}

// SetMemberAvatar updates a member's avatar.
func SetMemberAvatar(ctx context.Context, db *sql.DB, memberID int64, contentType string, data []byte) error {
	if db == nil {
		return ErrNilDB
	}
	if memberID == 0 {
		return ErrMissingMemberID
	}
	if contentType == "" || len(data) == 0 {
		return ErrMissingField
	}

	res, err := db.ExecContext(ctx, `
		UPDATE members
		SET avatar_content_type = $1,
			avatar_data = $2,
			updated_at = NOW()
		WHERE id = $3
	`, contentType, data, memberID)
	if err != nil {
		return fmt.Errorf("update member avatar: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update member avatar rows affected: %w", err)
	}
	if affected == 0 {
		return sql.ErrNoRows
	}

	return nil
}

func nullableMemberString(v string) sql.NullString {
	if v == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: v, Valid: true}
}
