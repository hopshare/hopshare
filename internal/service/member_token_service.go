package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	MemberTokenPurposePasswordReset     = "password_reset"
	MemberTokenPurposeEmailVerification = "email_verification"
	memberTokenSeparator                = "."
	DefaultMemberTokenTTL               = 2 * time.Hour
)

type IssueMemberTokenParams struct {
	MemberID    int64
	Purpose     string
	TTL         time.Duration
	RequestedIP *string
}

// IssueMemberToken creates and stores a new one-time token, returning its raw value.
func IssueMemberToken(ctx context.Context, db *sql.DB, params IssueMemberTokenParams) (string, error) {
	if db == nil {
		return "", ErrNilDB
	}
	if params.MemberID <= 0 {
		return "", ErrMissingMemberID
	}
	if !isValidMemberTokenPurpose(params.Purpose) {
		return "", ErrTokenPurposeInvalid
	}

	ttl := params.TTL
	if ttl == 0 {
		ttl = DefaultMemberTokenTTL
	}

	tokenID, err := randomHexToken(16)
	if err != nil {
		return "", fmt.Errorf("generate token id: %w", err)
	}
	tokenSecret, err := randomHexToken(32)
	if err != nil {
		return "", fmt.Errorf("generate token secret: %w", err)
	}
	tokenHash := hashTokenSecret(tokenSecret)
	expiresAt := time.Now().UTC().Add(ttl)

	requestedIP := normalizeRequestedIP(params.RequestedIP)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO member_tokens (token_id, member_id, purpose, token_hash, expires_at, requested_ip)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, tokenID, params.MemberID, params.Purpose, tokenHash, expiresAt, requestedIP); err != nil {
		return "", fmt.Errorf("issue member token: %w", err)
	}

	return buildIssuedMemberToken(tokenID, tokenSecret), nil
}

// ValidateMemberToken verifies token authenticity and state without consuming it.
func ValidateMemberToken(ctx context.Context, db *sql.DB, purpose, rawToken string) (int64, error) {
	if db == nil {
		return 0, ErrNilDB
	}
	if !isValidMemberTokenPurpose(purpose) {
		return 0, ErrTokenPurposeInvalid
	}

	tokenID, tokenSecret, ok := splitIssuedMemberToken(rawToken)
	if !ok {
		return 0, ErrTokenInvalid
	}

	var memberID int64
	var storedHash string
	var expiresAt time.Time
	var usedAt sql.NullTime
	err := db.QueryRowContext(ctx, `
		SELECT member_id, token_hash, expires_at, used_at
		FROM member_tokens
		WHERE token_id = $1
		  AND purpose = $2
	`, tokenID, purpose).Scan(&memberID, &storedHash, &expiresAt, &usedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrTokenInvalid
	}
	if err != nil {
		return 0, fmt.Errorf("validate member token: %w", err)
	}

	if !tokenHashesEqual(storedHash, hashTokenSecret(tokenSecret)) {
		return 0, ErrTokenInvalid
	}
	if usedAt.Valid {
		return 0, ErrTokenUsed
	}
	if !expiresAt.After(time.Now().UTC()) {
		return 0, ErrTokenExpired
	}

	return memberID, nil
}

// ConsumeMemberToken verifies and marks a token used in a single transaction.
func ConsumeMemberToken(ctx context.Context, db *sql.DB, purpose, rawToken string) (int64, error) {
	if db == nil {
		return 0, ErrNilDB
	}
	if !isValidMemberTokenPurpose(purpose) {
		return 0, ErrTokenPurposeInvalid
	}

	tokenID, tokenSecret, ok := splitIssuedMemberToken(rawToken)
	if !ok {
		return 0, ErrTokenInvalid
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin token consume transaction: %w", err)
	}
	defer tx.Rollback()

	var rowID int64
	var memberID int64
	var storedHash string
	var expiresAt time.Time
	var usedAt sql.NullTime
	err = tx.QueryRowContext(ctx, `
		SELECT id, member_id, token_hash, expires_at, used_at
		FROM member_tokens
		WHERE token_id = $1
		  AND purpose = $2
		FOR UPDATE
	`, tokenID, purpose).Scan(&rowID, &memberID, &storedHash, &expiresAt, &usedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrTokenInvalid
	}
	if err != nil {
		return 0, fmt.Errorf("load member token for consume: %w", err)
	}

	if !tokenHashesEqual(storedHash, hashTokenSecret(tokenSecret)) {
		return 0, ErrTokenInvalid
	}
	if usedAt.Valid {
		return 0, ErrTokenUsed
	}
	if !expiresAt.After(time.Now().UTC()) {
		return 0, ErrTokenExpired
	}

	res, err := tx.ExecContext(ctx, `
		UPDATE member_tokens
		SET used_at = NOW()
		WHERE id = $1
		  AND used_at IS NULL
	`, rowID)
	if err != nil {
		return 0, fmt.Errorf("consume member token: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("consume member token rows affected: %w", err)
	}
	if rowsAffected != 1 {
		return 0, ErrTokenUsed
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit token consume transaction: %w", err)
	}

	return memberID, nil
}

func isValidMemberTokenPurpose(purpose string) bool {
	switch strings.TrimSpace(purpose) {
	case MemberTokenPurposePasswordReset, MemberTokenPurposeEmailVerification:
		return true
	default:
		return false
	}
}

func normalizeRequestedIP(requestedIP *string) *string {
	if requestedIP == nil {
		return nil
	}
	ip := strings.TrimSpace(*requestedIP)
	if ip == "" {
		return nil
	}
	return &ip
}

func buildIssuedMemberToken(tokenID, tokenSecret string) string {
	return tokenID + memberTokenSeparator + tokenSecret
}

func splitIssuedMemberToken(rawToken string) (tokenID, tokenSecret string, ok bool) {
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

func randomHexToken(bytesLen int) (string, error) {
	if bytesLen <= 0 {
		return "", fmt.Errorf("bytes length must be positive")
	}
	b := make([]byte, bytesLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func hashTokenSecret(tokenSecret string) string {
	sum := sha256.Sum256([]byte(tokenSecret))
	return hex.EncodeToString(sum[:])
}

func tokenHashesEqual(storedHash, candidateHash string) bool {
	storedHash = strings.TrimSpace(storedHash)
	candidateHash = strings.TrimSpace(candidateHash)
	if len(storedHash) != len(candidateHash) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(storedHash), []byte(candidateHash)) == 1
}

func isLowerHex(value string) bool {
	for i := 0; i < len(value); i++ {
		b := value[i]
		isDigit := b >= '0' && b <= '9'
		isLowerHex := b >= 'a' && b <= 'f'
		if !isDigit && !isLowerHex {
			return false
		}
	}
	return true
}
