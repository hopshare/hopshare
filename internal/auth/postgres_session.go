package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

const (
	sessionTokenSeparator = "."
	sessionTokenIDBytes   = 16
	sessionTokenSecretLen = 32
)

// PostgresSessionManager persists sessions in Postgres so they survive restarts
// and are shared across all app instances.
type PostgresSessionManager struct {
	db          *sql.DB
	absoluteTTL time.Duration
	idleTimeout time.Duration
	now         func() time.Time
	cookieKey   string
}

func NewPostgresSessionManager(db *sql.DB, cfg SessionManagerConfig) (*PostgresSessionManager, error) {
	if db == nil {
		return nil, errors.New("nil database")
	}

	absoluteTTL := cfg.AbsoluteTTL
	if absoluteTTL < 0 {
		absoluteTTL = 0
	}
	idleTimeout := cfg.IdleTimeout
	if idleTimeout < 0 {
		idleTimeout = 0
	}

	return &PostgresSessionManager{
		db:          db,
		absoluteTTL: absoluteTTL,
		idleTimeout: idleTimeout,
		now:         time.Now,
		cookieKey:   "hopshare_session",
	}, nil
}

func (s *PostgresSessionManager) CookieName() string {
	return s.cookieKey
}

func (s *PostgresSessionManager) Create(memberID int64) (string, error) {
	if memberID <= 0 {
		return "", errors.New("invalid member id")
	}

	ctx := context.Background()
	now := s.now().UTC()
	absoluteExpiresAt, idleExpiresAt := s.expiryBounds(now)

	for attempt := 0; attempt < 5; attempt++ {
		tokenID, tokenSecret, err := randomSessionTokenParts()
		if err != nil {
			return "", fmt.Errorf("generate session token: %w", err)
		}

		_, err = s.db.ExecContext(ctx, `
			INSERT INTO member_sessions (
				token_id,
				token_hash,
				member_id,
				created_at,
				last_activity_at,
				absolute_expires_at,
				idle_expires_at
			)
			VALUES ($1, $2, $3, $4, $4, $5, $6)
		`, tokenID, hashSessionSecret(tokenSecret), memberID, now, absoluteExpiresAt, idleExpiresAt)
		if err == nil {
			return buildSessionToken(tokenID, tokenSecret), nil
		}
		if isUniqueViolation(err) {
			continue
		}
		return "", fmt.Errorf("insert session: %w", err)
	}

	return "", errors.New("could not create unique session token")
}

func (s *PostgresSessionManager) Get(rawToken string) (int64, bool) {
	tokenID, tokenSecret, ok := splitSessionToken(rawToken)
	if !ok {
		return 0, false
	}

	now := s.now().UTC()
	nextIdleExpiresAt := any(nil)
	if s.idleTimeout > 0 {
		nextIdleExpiresAt = now.Add(s.idleTimeout)
	}

	var memberID int64
	err := s.db.QueryRowContext(context.Background(), `
		UPDATE member_sessions
		SET last_activity_at = $3,
			idle_expires_at = $4
		WHERE token_id = $1
			AND token_hash = $2
			AND (absolute_expires_at IS NULL OR absolute_expires_at > $3)
			AND (idle_expires_at IS NULL OR idle_expires_at > $3)
		RETURNING member_id
	`, tokenID, hashSessionSecret(tokenSecret), now, nextIdleExpiresAt).Scan(&memberID)
	if err == nil {
		return memberID, true
	}
	if errors.Is(err, sql.ErrNoRows) {
		// Best-effort cleanup when the token exists but has just expired.
		_, _ = s.db.ExecContext(context.Background(), `
			DELETE FROM member_sessions
			WHERE token_id = $1
				AND token_hash = $2
				AND (
					(absolute_expires_at IS NOT NULL AND absolute_expires_at <= $3)
					OR (idle_expires_at IS NOT NULL AND idle_expires_at <= $3)
				)
		`, tokenID, hashSessionSecret(tokenSecret), now)
	}
	return 0, false
}

func (s *PostgresSessionManager) Delete(rawToken string) {
	tokenID, tokenSecret, ok := splitSessionToken(rawToken)
	if !ok {
		return
	}
	_, _ = s.db.ExecContext(context.Background(), `
		DELETE FROM member_sessions
		WHERE token_id = $1
			AND token_hash = $2
	`, tokenID, hashSessionSecret(tokenSecret))
}

func (s *PostgresSessionManager) RevokeAllForMember(memberID int64) int {
	if memberID <= 0 {
		return 0
	}

	res, err := s.db.ExecContext(context.Background(), `
		DELETE FROM member_sessions
		WHERE member_id = $1
	`, memberID)
	if err != nil {
		return 0
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil || rowsAffected <= 0 {
		return 0
	}
	if rowsAffected > int64(^uint(0)>>1) {
		return int(^uint(0) >> 1)
	}
	return int(rowsAffected)
}

func (s *PostgresSessionManager) Rotate(rawToken string) (string, int64, bool) {
	tokenID, tokenSecret, ok := splitSessionToken(rawToken)
	if !ok {
		return "", 0, false
	}

	now := s.now().UTC()
	absoluteExpiresAt, idleExpiresAt := s.expiryBounds(now)

	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", 0, false
	}
	defer tx.Rollback()

	var memberID int64
	err = tx.QueryRowContext(ctx, `
		SELECT member_id
		FROM member_sessions
		WHERE token_id = $1
			AND token_hash = $2
			AND (absolute_expires_at IS NULL OR absolute_expires_at > $3)
			AND (idle_expires_at IS NULL OR idle_expires_at > $3)
		FOR UPDATE
	`, tokenID, hashSessionSecret(tokenSecret), now).Scan(&memberID)
	if errors.Is(err, sql.ErrNoRows) {
		_, _ = tx.ExecContext(ctx, `
			DELETE FROM member_sessions
			WHERE token_id = $1
				AND token_hash = $2
				AND (
					(absolute_expires_at IS NOT NULL AND absolute_expires_at <= $3)
					OR (idle_expires_at IS NOT NULL AND idle_expires_at <= $3)
				)
		`, tokenID, hashSessionSecret(tokenSecret), now)
		return "", 0, false
	}
	if err != nil {
		return "", 0, false
	}

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM member_sessions
		WHERE token_id = $1
			AND token_hash = $2
	`, tokenID, hashSessionSecret(tokenSecret)); err != nil {
		return "", 0, false
	}

	var newToken string
	for attempt := 0; attempt < 5; attempt++ {
		newTokenID, newTokenSecret, err := randomSessionTokenParts()
		if err != nil {
			return "", 0, false
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO member_sessions (
				token_id,
				token_hash,
				member_id,
				created_at,
				last_activity_at,
				absolute_expires_at,
				idle_expires_at
			)
			VALUES ($1, $2, $3, $4, $4, $5, $6)
		`, newTokenID, hashSessionSecret(newTokenSecret), memberID, now, absoluteExpiresAt, idleExpiresAt)
		if err == nil {
			newToken = buildSessionToken(newTokenID, newTokenSecret)
			break
		}
		if !isUniqueViolation(err) {
			return "", 0, false
		}
	}
	if newToken == "" {
		return "", 0, false
	}

	if err := tx.Commit(); err != nil {
		return "", 0, false
	}
	return newToken, memberID, true
}

func (s *PostgresSessionManager) expiryBounds(now time.Time) (any, any) {
	absoluteExpiresAt := any(nil)
	if s.absoluteTTL > 0 {
		absoluteExpiresAt = now.Add(s.absoluteTTL)
	}
	idleExpiresAt := any(nil)
	if s.idleTimeout > 0 {
		idleExpiresAt = now.Add(s.idleTimeout)
	}
	return absoluteExpiresAt, idleExpiresAt
}

func randomSessionTokenParts() (string, string, error) {
	tokenID, err := randomSessionHex(sessionTokenIDBytes)
	if err != nil {
		return "", "", err
	}
	tokenSecret, err := randomSessionHex(sessionTokenSecretLen)
	if err != nil {
		return "", "", err
	}
	return tokenID, tokenSecret, nil
}

func randomSessionHex(bytesLen int) (string, error) {
	if bytesLen <= 0 {
		return "", errors.New("bytes length must be positive")
	}
	b := make([]byte, bytesLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func hashSessionSecret(tokenSecret string) string {
	sum := sha256.Sum256([]byte(tokenSecret))
	return hex.EncodeToString(sum[:])
}

func buildSessionToken(tokenID, tokenSecret string) string {
	return tokenID + sessionTokenSeparator + tokenSecret
}

func splitSessionToken(rawToken string) (tokenID, tokenSecret string, ok bool) {
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" {
		return "", "", false
	}
	tokenID, tokenSecret, ok = strings.Cut(rawToken, sessionTokenSeparator)
	if !ok || tokenID == "" || tokenSecret == "" {
		return "", "", false
	}
	if len(tokenID) != 32 || len(tokenSecret) != 64 {
		return "", "", false
	}
	if !isLowerHexTokenPart(tokenID) || !isLowerHexTokenPart(tokenSecret) {
		return "", "", false
	}
	return tokenID, tokenSecret, true
}

func isLowerHexTokenPart(v string) bool {
	for i := 0; i < len(v); i++ {
		b := v[i]
		isDigit := b >= '0' && b <= '9'
		isLowerHex := b >= 'a' && b <= 'f'
		if !isDigit && !isLowerHex {
			return false
		}
	}
	return true
}

func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return pqErr.Code == "23505"
	}
	return false
}
