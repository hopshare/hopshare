package types

import "time"

const (
	ContactMethodEmail = "email"
	ContactMethodPhone = "phone"
	ContactMethodOther = "other"
)

// Member represents a row in the members table.
type Member struct {
	ID                     int64
	Username               string
	Email                  string
	PasswordHash           string
	PreferredContactMethod string
	PreferredContact       string
	ProfilePictureURL      *string
	City                   *string
	State                  *string
	Interests              *string
	Enabled                bool
	Verified               bool
	CreatedAt              time.Time
	UpdatedAt              time.Time
}
