package auth

// SessionStore defines session persistence operations used by the HTTP layer.
type SessionStore interface {
	CookieName() string
	Create(memberID int64) (string, error)
	Get(token string) (int64, bool)
	Delete(token string)
	RevokeAllForMember(memberID int64) int
	Rotate(token string) (string, int64, bool)
}
