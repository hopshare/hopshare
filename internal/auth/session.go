package auth

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
)

// SessionManager manages simple in-memory sessions keyed by token.
type SessionManager struct {
	mu        sync.RWMutex
	sessions  map[string]string
	cookieKey string
}

func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions:  make(map[string]string),
		cookieKey: "hopshare_session",
	}
}

func (s *SessionManager) CookieName() string {
	return s.cookieKey
}

// Create issues a new session token for the email.
func (s *SessionManager) Create(email string) (string, error) {
	token := randomToken()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[token] = email
	return token, nil
}

// Get retrieves the email for a session token.
func (s *SessionManager) Get(token string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	email, ok := s.sessions[token]
	return email, ok
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
