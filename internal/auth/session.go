package auth

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
)

// SessionManager manages simple in-memory sessions keyed by token.
type SessionManager struct {
	mu        sync.RWMutex
	sessions  map[string]int64
	cookieKey string
}

func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions:  make(map[string]int64),
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
	s.sessions[token] = memberID
	return token, nil
}

// Get retrieves the member ID for a session token.
func (s *SessionManager) Get(token string) (int64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	memberID, ok := s.sessions[token]
	return memberID, ok
}

// Delete removes a session token.
func (s *SessionManager) Delete(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, token)
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "session"
	}
	return hex.EncodeToString(b)
}
