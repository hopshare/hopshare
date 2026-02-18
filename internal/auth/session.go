package auth

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

type sessionRecord struct {
	memberID       int64
	createdAt      time.Time
	lastActivityAt time.Time
}

type SessionManagerConfig struct {
	AbsoluteTTL time.Duration
	IdleTimeout time.Duration
}

// SessionManager manages simple in-memory sessions keyed by token.
type SessionManager struct {
	mu          sync.RWMutex
	sessions    map[string]sessionRecord
	byMember    map[int64]map[string]struct{}
	absoluteTTL time.Duration
	idleTimeout time.Duration
	now         func() time.Time
	cookieKey   string
}

func NewSessionManager() *SessionManager {
	return NewSessionManagerWithConfig(SessionManagerConfig{})
}

// NewSessionManagerWithTTL creates a session manager where sessions expire
// after ttl. A non-positive ttl disables expiry checks.
func NewSessionManagerWithTTL(ttl time.Duration) *SessionManager {
	return NewSessionManagerWithConfig(SessionManagerConfig{
		AbsoluteTTL: ttl,
	})
}

func NewSessionManagerWithConfig(cfg SessionManagerConfig) *SessionManager {
	absoluteTTL := cfg.AbsoluteTTL
	if absoluteTTL < 0 {
		absoluteTTL = 0
	}
	idleTimeout := cfg.IdleTimeout
	if idleTimeout < 0 {
		idleTimeout = 0
	}
	return &SessionManager{
		sessions:    make(map[string]sessionRecord),
		byMember:    make(map[int64]map[string]struct{}),
		absoluteTTL: absoluteTTL,
		idleTimeout: idleTimeout,
		now:         time.Now,
		cookieKey:   "hopshare_session",
	}
}

func (s *SessionManager) CookieName() string {
	return s.cookieKey
}

// Create issues a new session token for the member ID.
func (s *SessionManager) Create(memberID int64) (string, error) {
	token := randomToken()
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now().UTC()
	record := sessionRecord{
		memberID:       memberID,
		createdAt:      now,
		lastActivityAt: now,
	}
	s.sessions[token] = record

	memberTokens, ok := s.byMember[memberID]
	if !ok {
		memberTokens = make(map[string]struct{})
		s.byMember[memberID] = memberTokens
	}
	memberTokens[token] = struct{}{}

	return token, nil
}

// Get retrieves the member ID for a session token.
func (s *SessionManager) Get(token string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, ok := s.sessions[token]
	if !ok {
		return 0, false
	}
	if s.isExpired(record) {
		s.deleteLocked(token, record.memberID)
		return 0, false
	}
	record.lastActivityAt = s.now().UTC()
	s.sessions[token] = record
	return record.memberID, true
}

// Delete removes a session token.
func (s *SessionManager) Delete(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, ok := s.sessions[token]
	if !ok {
		return
	}
	s.deleteLocked(token, record.memberID)
}

// RevokeAllForMember removes all active sessions for a member and returns the
// number of sessions revoked.
func (s *SessionManager) RevokeAllForMember(memberID int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	tokens, ok := s.byMember[memberID]
	if !ok || len(tokens) == 0 {
		return 0
	}

	revoked := 0
	for token := range tokens {
		if _, exists := s.sessions[token]; exists {
			delete(s.sessions, token)
			revoked++
		}
	}
	delete(s.byMember, memberID)

	return revoked
}

// Rotate replaces an existing active session token with a new token.
// It returns the replacement token and the owning member ID.
func (s *SessionManager) Rotate(token string) (string, int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, ok := s.sessions[token]
	if !ok {
		return "", 0, false
	}
	if s.isExpired(record) {
		s.deleteLocked(token, record.memberID)
		return "", 0, false
	}

	s.deleteLocked(token, record.memberID)
	newToken := randomToken()
	now := s.now().UTC()
	record.createdAt = now
	record.lastActivityAt = now
	s.sessions[newToken] = record

	memberTokens, ok := s.byMember[record.memberID]
	if !ok {
		memberTokens = make(map[string]struct{})
		s.byMember[record.memberID] = memberTokens
	}
	memberTokens[newToken] = struct{}{}

	return newToken, record.memberID, true
}

func (s *SessionManager) isExpired(record sessionRecord) bool {
	now := s.now().UTC()
	if s.absoluteTTL > 0 {
		expiresAt := record.createdAt.Add(s.absoluteTTL)
		if !expiresAt.After(now) {
			return true
		}
	}
	if s.idleTimeout > 0 {
		expiresAt := record.lastActivityAt.Add(s.idleTimeout)
		if !expiresAt.After(now) {
			return true
		}
	}
	return false
}

func (s *SessionManager) deleteLocked(token string, memberID int64) {
	delete(s.sessions, token)

	memberTokens, ok := s.byMember[memberID]
	if !ok {
		return
	}
	delete(memberTokens, token)
	if len(memberTokens) == 0 {
		delete(s.byMember, memberID)
	}
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "session"
	}
	return hex.EncodeToString(b)
}
