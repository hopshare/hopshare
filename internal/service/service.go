package service

import (
	"context"
	"database/sql"
	"errors"
)

var (
	ErrNilDB                = errors.New("db is nil")
	ErrMissingField         = errors.New("missing required field")
	ErrMissingMemberID      = errors.New("member id is required")
	ErrMissingOrgName       = errors.New("organization name is required")
	ErrMissingOrgID         = errors.New("organization id is required")
	ErrInvalidCredentials   = errors.New("invalid email or password")
	ErrEmailNotVerified     = errors.New("email not verified")
	ErrRequestAlreadyExists = errors.New("membership request already exists")
	ErrAlreadyPrimaryOwner  = errors.New("member already manages an organization")
	ErrRequestNotFound      = errors.New("membership request not found")
	ErrMembershipNotFound   = errors.New("membership not found")
	ErrInvalidRoleChange    = errors.New("invalid role change")
	ErrSkillForbidden       = errors.New("skill forbidden")
	ErrOrganizationDisabled = errors.New("organization is disabled")
	ErrAuditReasonRequired  = errors.New("audit reason is required")
	ErrInvalidAuditMetadata = errors.New("invalid audit metadata")
	ErrTokenInvalid         = errors.New("token invalid")
	ErrTokenExpired         = errors.New("token expired")
	ErrTokenUsed            = errors.New("token already used")
	ErrTokenPurposeInvalid  = errors.New("token purpose invalid")

	ErrHopNotFound      = errors.New("hop not found")
	ErrHopForbidden     = errors.New("hop forbidden")
	ErrHopInvalidState  = errors.New("hop invalid state")
	ErrHopOfferExists   = errors.New("hop offer already exists")
	ErrHopImageNotFound = errors.New("hop image not found")

	ErrModerationReportNotFound  = errors.New("moderation report not found")
	ErrModerationReportResolved  = errors.New("moderation report already resolved")
	ErrModerationTargetNotFound  = errors.New("moderation target not found")
	ErrModerationTargetMismatch  = errors.New("moderation target type mismatch")
	ErrModerationAlreadyReported = errors.New("moderation target already reported by this member")
	ErrInvalidHoursDelta         = errors.New("invalid hours delta")

	ErrMessageNotFound = errors.New("message not found")
	ErrInvalidMessage  = errors.New("invalid message")
)

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
