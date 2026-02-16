package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"hopshare/internal/types"
)

const (
	AdminAuditActionAppOverviewViewed = "admin.app_overview.view"
	AdminAuditTargetApplication       = "application"
)

type WriteAdminAuditEventParams struct {
	ActorMemberID int64
	Action        string
	Target        string
	Reason        string
	Metadata      json.RawMessage
	OccurredAt    time.Time
	Sensitive     bool
}

func WriteAdminAuditEvent(ctx context.Context, db *sql.DB, p WriteAdminAuditEventParams) (types.AdminAuditEvent, error) {
	if db == nil {
		return types.AdminAuditEvent{}, ErrNilDB
	}
	if p.ActorMemberID == 0 {
		return types.AdminAuditEvent{}, ErrMissingMemberID
	}

	action := strings.TrimSpace(p.Action)
	target := strings.TrimSpace(p.Target)
	reason := strings.TrimSpace(p.Reason)

	if action == "" || target == "" {
		return types.AdminAuditEvent{}, ErrMissingField
	}
	if p.Sensitive || adminAuditActionRequiresReason(action) {
		if reason == "" {
			return types.AdminAuditEvent{}, ErrAuditReasonRequired
		}
	}

	metadata, err := normalizeAdminAuditMetadata(p.Metadata)
	if err != nil {
		return types.AdminAuditEvent{}, err
	}

	occurredAt := p.OccurredAt.UTC()
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}

	var reasonCol sql.NullString
	if reason != "" {
		reasonCol = sql.NullString{String: reason, Valid: true}
	}

	var event types.AdminAuditEvent
	var storedReason sql.NullString
	var storedMetadata []byte
	if err := db.QueryRowContext(ctx, `
		INSERT INTO admin_audit_events (
			actor_member_id,
			action,
			target,
			reason,
			metadata,
			created_at
		) VALUES ($1, $2, $3, $4, $5::jsonb, $6)
		RETURNING id, actor_member_id, action, target, reason, metadata, created_at
	`, p.ActorMemberID, action, target, reasonCol, string(metadata), occurredAt).Scan(
		&event.ID,
		&event.ActorMemberID,
		&event.Action,
		&event.Target,
		&storedReason,
		&storedMetadata,
		&event.CreatedAt,
	); err != nil {
		return types.AdminAuditEvent{}, fmt.Errorf("insert admin audit event: %w", err)
	}
	if storedReason.Valid {
		event.Reason = &storedReason.String
	}
	event.Metadata = json.RawMessage(append([]byte(nil), storedMetadata...))

	return event, nil
}

func normalizeAdminAuditMetadata(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid(trimmed) {
		return nil, ErrInvalidAuditMetadata
	}
	return json.RawMessage(append([]byte(nil), trimmed...)), nil
}

func adminAuditActionRequiresReason(action string) bool {
	normalized := strings.ToLower(strings.TrimSpace(action))
	if normalized == "" {
		return false
	}

	reasonTokens := []string{
		".delete",
		".remove",
		".disable",
		".revoke",
		".purge",
		".destroy",
		".suspend",
	}
	for _, token := range reasonTokens {
		if strings.Contains(normalized, token) {
			return true
		}
	}
	return false
}
