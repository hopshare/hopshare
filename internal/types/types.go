package types

import "time"

const (
	ContactMethodEmail = "email"
	ContactMethodPhone = "phone"
	ContactMethodOther = "other"
)

const (
	RequestStatusOpen      = "open"
	RequestStatusAccepted  = "accepted"
	RequestStatusCanceled  = "canceled"
	RequestStatusExpired   = "expired"
	RequestStatusCompleted = "completed"
)

const (
	RequestNeededByAnytime     = "anytime"
	RequestNeededByOn          = "on"
	RequestNeededByAround      = "around"
	RequestNeededByNoLaterThan = "no_later_than"
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
	LastLoginAt            *time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

// Organization represents a Hopshare organization/tenant.
type Organization struct {
	ID        int64
	Name      string
	LogoContentType *string
	HasLogo         bool
	Enabled   bool
	CreatedBy *int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// MembershipRequest represents a pending/judged request to join an organization.
type MembershipRequest struct {
	ID             int64
	OrganizationID int64
	MemberID       int64
	MemberName     string
	MemberEmail    string
	RequestedAt    time.Time
	Status         string
}

// OrganizationMember represents an active membership record.
type OrganizationMember struct {
	MemberID       int64
	Username       string
	Email          string
	Role           string
	IsPrimaryOwner bool
	JoinedAt       time.Time
}

// Request represents a help request within an organization.
type Request struct {
	ID             int64
	OrganizationID int64
	CreatedBy      int64
	CreatedByName  string
	Title          string
	Details        *string
	EstimatedHours int

	NeededByKind string
	NeededByDate *time.Time
	ExpiresAt    *time.Time

	Status string

	AcceptedBy     *int64
	AcceptedByName *string
	AcceptedAt     *time.Time

	CanceledBy *int64
	CanceledAt *time.Time

	CompletedBy       *int64
	CompletedAt       *time.Time
	CompletedHours    *int
	CompletionComment *string

	CreatedAt time.Time
	UpdatedAt time.Time
}

type OrgRequestMetrics struct {
	MemberCount       int
	PendingCount      int
	CompletedCount    int
	CompletedThisWeek int
}

type MemberRequestStats struct {
	BalanceHours      int
	RequestsMade      int
	LastRequestMadeAt *time.Time
	RequestsFulfilled int
	LastFulfilledAt   *time.Time
}
