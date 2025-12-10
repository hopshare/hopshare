package auth

import (
	"errors"
	"strings"
	"sync"
)

// User is a minimal in-memory user record for demo purposes.
type User struct {
	Email    string
	Password string
}

// UserStore keeps users and reset tokens in memory.
type UserStore struct {
	mu          sync.RWMutex
	users       map[string]*User
	resetTokens map[string]string // token -> email
}

func NewUserStore() *UserStore {
	us := &UserStore{
		users:       make(map[string]*User),
		resetTokens: make(map[string]string),
	}
	us.users["demo@hopshare.org"] = &User{
		Email:    "demo@hopshare.org",
		Password: "password123",
	}
	return us
}

// Authenticate verifies the provided credentials.
func (s *UserStore) Authenticate(email, password string) (*User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[strings.ToLower(email)]
	if !ok {
		return nil, false
	}
	if u.Password != password {
		return nil, false
	}
	return u, true
}

// RequestReset issues a token for the email if it exists. The boolean indicates whether a user was found.
func (s *UserStore) RequestReset(email string) (token string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.users[strings.ToLower(email)]; !exists {
		return "", false
	}
	token = randomToken()
	s.resetTokens[token] = strings.ToLower(email)
	return token, true
}

// ResetPassword updates the password if the token is valid and returns the associated email.
func (s *UserStore) ResetPassword(token, newPassword string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	email, ok := s.resetTokens[token]
	if !ok {
		return "", errors.New("invalid or expired token")
	}
	user, exists := s.users[email]
	if !exists {
		return "", errors.New("user not found")
	}
	user.Password = newPassword
	delete(s.resetTokens, token)
	return email, nil
}

// Get returns a user by email.
func (s *UserStore) Get(email string) (*User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[strings.ToLower(email)]
	return u, ok
}
