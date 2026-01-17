package types

import "time"

const (
	ContactMethodEmail = "email"
	ContactMethodPhone = "phone"
	ContactMethodOther = "other"
)

const (
	HopStatusOpen      = "open"
	HopStatusAccepted  = "accepted"
	HopStatusCanceled  = "canceled"
	HopStatusExpired   = "expired"
	HopStatusCompleted = "completed"
)

const (
	HopNeededByAnytime     = "anytime"
	HopNeededByOn          = "on"
	HopNeededByAround      = "around"
	HopNeededByNoLaterThan = "no_later_than"
)

const (
	MessageTypeInformation = "information"
	MessageTypeAction      = "action"
)

const (
	MessageActionAccepted = "accepted"
	MessageActionDeclined = "declined"
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
	CurrentOrganization    *int64
	Enabled                bool
	Verified               bool
	LastLoginAt            *time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

// Organization represents a Hopshare organization/tenant.
type Organization struct {
	ID              int64
	Name            string
	LogoContentType *string
	HasLogo         bool
	Enabled         bool
	CreatedBy       *int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
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

// Hop represents a help hop within an organization.
type Hop struct {
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

// Message represents a single inbox message.
type Message struct {
	ID            int64
	RecipientID   int64
	SenderID      *int64
	SenderName    string
	MessageType   string
	HopID         *int64
	ActionStatus  *string
	ActionTakenAt *time.Time
	Subject       string
	Body          string
	ReadAt        *time.Time
	CreatedAt     time.Time
}

type OrgHopMetrics struct {
	MemberCount       int
	PendingCount      int
	CompletedCount    int
	CompletedThisWeek int
}

type MemberHopStats struct {
	BalanceHours       int
	HopsMade           int
	LastHopMadeAt      *time.Time
	HopsFulfilled      int
	LastHopFulfilledAt *time.Time
}
