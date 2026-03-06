package types

import (
	"encoding/json"
	"time"
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
	HopOfferStatusAccepted = "accepted"
	HopOfferStatusDenied   = "denied"
)

const (
	MessageTypeInformation = "information"
	MessageTypeAction      = "action"
)

const (
	MessageActionAccepted = "accepted"
	MessageActionDeclined = "declined"
)

const (
	ModerationReportTypeHopComment = "hop_comment"
	ModerationReportTypeHopImage   = "hop_image"
)

const (
	ModerationReportStatusOpen      = "open"
	ModerationReportStatusDismissed = "dismissed"
	ModerationReportStatusActioned  = "actioned"
)

const (
	ModerationResolutionDismiss       = "dismiss_report"
	ModerationResolutionDeleteComment = "delete_comment"
	ModerationResolutionDeleteImage   = "delete_image"
)

// Member represents a row in the members table.
type Member struct {
	ID                  int64
	FirstName           string
	LastName            string
	Email               string
	PasswordHash        string
	PreferredContact    string
	ProfilePictureURL   *string
	AvatarContentType   *string
	HasAvatar           bool
	City                *string
	State               *string
	CurrentOrganization *int64
	Enabled             bool
	Verified            bool
	LastLoginAt         *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// Organization represents a Hopshare organization/tenant.
type Organization struct {
	ID                      int64
	Name                    string
	URLName                 string
	City                    string
	State                   string
	Description             string
	TimebankMinBalance      int
	TimebankMaxBalance      int
	TimebankStartingBalance int
	LogoContentType         *string
	HasLogo                 bool
	Enabled                 bool
	CreatedBy               *int64
	CreatedAt               time.Time
	UpdatedAt               time.Time
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

// OrganizationInvitation represents an invitation to join an organization.
type OrganizationInvitation struct {
	ID               int64
	OrganizationID   int64
	InvitedEmail     string
	Role             string
	Status           string
	InvitedBy        *int64
	InvitedByName    string
	InvitedAt        time.Time
	SentAt           *time.Time
	ExpiresAt        *time.Time
	RespondedAt      *time.Time
	AcceptedAt       *time.Time
	AcceptedMemberID *int64
}

// OrganizationMember represents an active membership record.
type OrganizationMember struct {
	MemberID    int64
	DisplayName string
	Email       string
	Role        string
	JoinedAt    time.Time
}

// MemberOrganization represents an organization plus the member's role.
type MemberOrganization struct {
	Organization
	Role string
}

// Skill represents a selectable skill, either system default or organization-specific.
type Skill struct {
	ID             int64
	OrganizationID *int64
	Name           string
	SourceLabel    string
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
	IsPrivate      bool

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

	HasPendingOffer bool

	CreatedByLeftOrganization  bool
	AcceptedByLeftOrganization bool
}

// HopComment represents a comment on a hop.
type HopComment struct {
	ID         int64
	HopID      int64
	MemberID   int64
	MemberName string
	Body       string
	CreatedAt  time.Time
}

// HopImage represents an uploaded hop image.
type HopImage struct {
	ID        int64
	HopID     int64
	MemberID  int64
	CreatedAt time.Time
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

// MemberNotification represents a dashboard notification for a member.
type MemberNotification struct {
	ID        int64
	MemberID  int64
	Text      string
	Href      *string
	CreatedAt time.Time
}

type OrgHopMetrics struct {
	MemberCount         int
	PendingCount        int
	CompletedCount      int
	CompletedThisWeek   int
	TotalHoursExchanged int
}

type MemberHopStats struct {
	BalanceHours       int
	HopsMade           int
	LastHopMadeAt      *time.Time
	HopsFulfilled      int
	LastHopFulfilledAt *time.Time
}

type AdminEnabledDisabledCounts struct {
	Enabled  int
	Disabled int
}

func (c AdminEnabledDisabledCounts) Total() int {
	return c.Enabled + c.Disabled
}

type AdminVerifiedNotVerifiedCounts struct {
	Verified    int
	NotVerified int
}

func (c AdminVerifiedNotVerifiedCounts) Total() int {
	return c.Verified + c.NotVerified
}

type AdminHopStatusCount struct {
	Status string
	Count  int
}

type AdminOrganizationLeaderboardEntry struct {
	OrganizationID      int64
	OrganizationName    string
	OrganizationURLName string
	OrganizationEnabled bool
	Value               int
	EnabledUsers        int
	DisabledUsers       int
}

func (e AdminOrganizationLeaderboardEntry) TotalUsers() int {
	return e.EnabledUsers + e.DisabledUsers
}

type AdminHourOverrideCounts struct {
	Count        int
	HoursGiven   int
	HoursRemoved int
}

type AdminAppOverview struct {
	OrganizationCounts      AdminEnabledDisabledCounts
	UserCounts              AdminEnabledDisabledCounts
	UserVerificationCounts  AdminVerifiedNotVerifiedCounts
	HourOverrideCounts      AdminHourOverrideCounts
	HopsByStatus            []AdminHopStatusCount
	TotalHoursExchanged     int
	TopOrgsByHopsCreated    []AdminOrganizationLeaderboardEntry
	TopOrgsByHoursExchanged []AdminOrganizationLeaderboardEntry
	TopOrgsByUsers          []AdminOrganizationLeaderboardEntry
	LeaderboardLimit        int
}

type AdminAuditEvent struct {
	ID            int64
	ActorMemberID int64
	Action        string
	Target        string
	Reason        *string
	Metadata      json.RawMessage
	CreatedAt     time.Time
}

type AdminAuditFilter struct {
	Actor        string
	StartDate    string
	EndDate      string
	Action       string
	Organization string
	User         string
	Target       string
}

type AdminAuditEventView struct {
	ID               int64
	ActorMemberID    int64
	ActorEmail       string
	ActorName        string
	Action           string
	Target           string
	Reason           *string
	Metadata         json.RawMessage
	CreatedAt        time.Time
	OrganizationID   *int64
	OrganizationName *string
	UserMemberID     *int64
	UserEmail        *string
	UserName         *string
}

type AdminAuditTabData struct {
	Filter     AdminAuditFilter
	Events     []AdminAuditEventView
	SuccessMsg string
	ErrorMsg   string
}

type AdminOrganizationHop struct {
	ID             int64
	OrganizationID int64
	Title          string
	Status         string
	CreatedByName  string
	CreatedAt      time.Time
}

type AdminOrganizationDetail struct {
	Organization        Organization
	MemberCount         int
	EnabledMemberCount  int
	DisabledMemberCount int
	HourOverrideCounts  AdminHourOverrideCounts
	HopCounts           []AdminHopStatusCount
	Hops                []AdminOrganizationHop
}

type AdminOrganizationTabData struct {
	Query         string
	Results       []Organization
	SelectedOrgID int64
	Selected      *AdminOrganizationDetail
	SuccessMsg    string
	ErrorMsg      string
}

type AdminUserSearchResult struct {
	MemberID    int64
	FirstName   string
	LastName    string
	Email       string
	Enabled     bool
	LastLoginAt *time.Time
}

type AdminUserMembershipTimelineEntry struct {
	OrganizationID      int64
	OrganizationName    string
	OrganizationURLName string
	Role                string
	JoinedAt            time.Time
	LeftAt              *time.Time
}

type AdminUserBalanceEntry struct {
	OrganizationID      int64
	OrganizationName    string
	OrganizationURLName string
	BalanceHours        int
}

type AdminUserDetail struct {
	MemberID       int64
	FirstName      string
	LastName       string
	Email          string
	Enabled        bool
	Verified       bool
	LastLoginAt    *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Memberships    []AdminUserMembershipTimelineEntry
	ActiveBalances []AdminUserBalanceEntry
}

type AdminUsersTabData struct {
	Query            string
	Results          []AdminUserSearchResult
	SelectedMemberID int64
	Selected         *AdminUserDetail
	SuccessMsg       string
	ErrorMsg         string
}

type AdminMessagesTabData struct {
	Query               string
	Results             []AdminUserSearchResult
	SelectedRecipientID int64
	SelectedRecipient   *AdminUserSearchResult
	Conversation        []Message
	SuccessMsg          string
	ErrorMsg            string
}

type ModerationReport struct {
	ID                  int64
	OrganizationID      int64
	OrganizationName    string
	OrganizationURLName string
	HopID               int64
	ReportType          string
	HopCommentID        *int64
	HopImageID          *int64
	ReportedMemberID    int64
	ReportedMemberName  string
	ContentMemberID     int64
	ContentMemberName   string
	ContentSummary      string
	ReporterDetails     *string
	Status              string
	ResolutionAction    *string
	ResolvedByMemberID  *int64
	ResolvedAt          *time.Time
	CreatedAt           time.Time
}

type ListModerationReportsParams struct {
	Status     string
	ReportType string
	Query      string
	Limit      int
}

type AdminModerationTabData struct {
	StatusFilter string
	TypeFilter   string
	Query        string
	Reports      []ModerationReport
	SuccessMsg   string
	ErrorMsg     string
}
