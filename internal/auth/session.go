package auth

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

type sessionRecord struct {
	memberID  int64
	expiresAt time.Time
}

// SessionManager manages simple in-memory sessions keyed by token.
type SessionManager struct {
	mu        sync.RWMutex
	sessions  map[string]sessionRecord
	byMember  map[int64]map[string]struct{}
	ttl       time.Duration
	now       func() time.Time
	cookieKey string
}

func NewSessionManager() *SessionManager {
	return NewSessionManagerWithTTL(0)
}

// NewSessionManagerWithTTL creates a session manager where sessions expire
// after ttl. A non-positive ttl disables expiry checks.
func NewSessionManagerWithTTL(ttl time.Duration) *SessionManager {
	if ttl < 0 {
		ttl = 0
	}
	return &SessionManager{
		sessions:  make(map[string]sessionRecord),
		byMember:  make(map[int64]map[string]struct{}),
		ttl:       ttl,
		now:       time.Now,
		cookieKey: "hopshare_session",
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

	record := sessionRecord{memberID: memberID}
	if s.ttl > 0 {
		record.expiresAt = s.now().UTC().Add(s.ttl)
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

func (s *SessionManager) isExpired(record sessionRecord) bool {
	if s.ttl <= 0 || record.expiresAt.IsZero() {
		return false
	}
	return s.now().UTC().After(record.expiresAt)
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
