package service

import (
	"context"
	"database/sql"
	"errors"
)

var (
	ErrNilDB                = errors.New("db is nil")
	ErrInvalidContactMethod = errors.New("invalid preferred contact method")
	ErrMissingField         = errors.New("missing required field")
	ErrMissingMemberID      = errors.New("member id is required")
	ErrMissingOrgName       = errors.New("organization name is required")
	ErrMissingOrgID         = errors.New("organization id is required")
	ErrInvalidCredentials   = errors.New("invalid email or password")
	ErrRequestAlreadyExists = errors.New("membership request already exists")
	ErrAlreadyPrimaryOwner  = errors.New("member already manages an organization")
	ErrRequestNotFound      = errors.New("membership request not found")
	ErrMembershipNotFound   = errors.New("membership not found")
	ErrInvalidRoleChange    = errors.New("invalid role change")
	ErrSkillForbidden       = errors.New("skill forbidden")

	ErrHopNotFound      = errors.New("hop not found")
	ErrHopForbidden     = errors.New("hop forbidden")
	ErrHopInvalidState  = errors.New("hop invalid state")
	ErrHopOfferExists   = errors.New("hop offer already exists")
	ErrHopImageNotFound = errors.New("hop image not found")

	ErrMessageNotFound = errors.New("message not found")
	ErrInvalidMessage  = errors.New("invalid message")
)

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
