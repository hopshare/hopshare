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
	AdminAuditActionOrganizationDisable     = "admin.organization.disable"
	AdminAuditActionOrganizationEnable      = "admin.organization.enable"
	AdminAuditActionHopExpire               = "admin.hop.expire"
	AdminAuditActionHopDelete               = "admin.hop.delete"
	AdminAuditActionModerationDismiss       = "admin.moderation.dismiss"
	AdminAuditActionModerationCommentDelete = "admin.moderation.comment.delete"
	AdminAuditActionModerationImageDelete   = "admin.moderation.image.delete"
	AdminAuditActionUserDisable             = "admin.user.disable"
	AdminAuditActionUserEnable              = "admin.user.enable"
	AdminAuditActionUserForcePasswordReset  = "admin.user.password.force_reset"
	AdminAuditActionUserRevokeSessions      = "admin.user.sessions.revoke"
	AdminAuditActionUserBalanceAdjust       = "admin.user.balance.adjust"
	AdminAuditActionMessageSend             = "admin.message.send"
	AdminAuditActionExportCSV               = "admin.audit.export.csv"
	AdminAuditActionExportJSON              = "admin.audit.export.json"
	AdminAuditTargetApplication             = "application"
	defaultAdminAuditEventListLimit         = 200
	maxAdminAuditEventListLimit             = 2000
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

type ListAdminAuditEventsParams struct {
	ActorQuery        string
	ActionQuery       string
	OrganizationQuery string
	UserQuery         string
	TargetQuery       string
	StartAt           *time.Time
	EndBefore         *time.Time
	Limit             int
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

func ListAdminAuditEvents(ctx context.Context, db *sql.DB, p ListAdminAuditEventsParams) ([]types.AdminAuditEventView, error) {
	if db == nil {
		return nil, ErrNilDB
	}

	limit := p.Limit
	if limit <= 0 {
		limit = defaultAdminAuditEventListLimit
	}
	if limit > maxAdminAuditEventListLimit {
		limit = maxAdminAuditEventListLimit
	}

	actorRaw, actorLike := adminAuditLikeValue(p.ActorQuery)
	actionRaw, actionLike := adminAuditLikeValue(p.ActionQuery)
	orgRaw, orgLike := adminAuditLikeValue(p.OrganizationQuery)
	userRaw, userLike := adminAuditLikeValue(p.UserQuery)
	targetRaw, targetLike := adminAuditLikeValue(p.TargetQuery)

	var startAt any
	if p.StartAt != nil && !p.StartAt.IsZero() {
		startAt = p.StartAt.UTC()
	}
	var endBefore any
	if p.EndBefore != nil && !p.EndBefore.IsZero() {
		endBefore = p.EndBefore.UTC()
	}

	rows, err := db.QueryContext(ctx, `
		SELECT
			a.id,
			a.actor_member_id,
			actor.username,
			actor.first_name,
			actor.last_name,
			a.action,
			a.target,
			a.reason,
			a.metadata,
			a.created_at,
			derived.org_id,
			org.name,
			derived.user_member_id,
			member_user.username,
			member_user.first_name,
			member_user.last_name
		FROM admin_audit_events a
		JOIN members actor ON actor.id = a.actor_member_id
		LEFT JOIN LATERAL (
			SELECT
				CASE
					WHEN (a.metadata ? 'org_id') AND (a.metadata->>'org_id') ~ '^[0-9]+$' THEN (a.metadata->>'org_id')::bigint
					WHEN a.target ~ '^organization:[0-9]+$' THEN split_part(a.target, ':', 2)::bigint
					ELSE NULL
				END AS org_id,
				CASE
					WHEN (a.metadata ? 'member_id') AND (a.metadata->>'member_id') ~ '^[0-9]+$' THEN (a.metadata->>'member_id')::bigint
					WHEN (a.metadata ? 'recipient_member_id') AND (a.metadata->>'recipient_member_id') ~ '^[0-9]+$' THEN (a.metadata->>'recipient_member_id')::bigint
					WHEN (a.metadata ? 'reported_member_id') AND (a.metadata->>'reported_member_id') ~ '^[0-9]+$' THEN (a.metadata->>'reported_member_id')::bigint
					WHEN a.target ~ '^member:[0-9]+$' THEN split_part(a.target, ':', 2)::bigint
					ELSE NULL
				END AS user_member_id
		) AS derived ON TRUE
		LEFT JOIN organizations org ON org.id = derived.org_id
		LEFT JOIN members member_user ON member_user.id = derived.user_member_id
		WHERE
			($1 = '' OR LOWER(actor.username) LIKE $2 OR LOWER(actor.first_name) LIKE $2 OR LOWER(actor.last_name) LIKE $2)
			AND ($3 = '' OR LOWER(a.action) LIKE $4)
			AND ($5 = '' OR LOWER(a.target) LIKE $6)
			AND (
				$7 = ''
				OR CAST(COALESCE(derived.org_id, 0) AS TEXT) = $7
				OR LOWER(COALESCE(org.name, '')) LIKE $8
				OR LOWER(COALESCE(org.url_name, '')) LIKE $8
			)
			AND (
				$9 = ''
				OR CAST(COALESCE(derived.user_member_id, 0) AS TEXT) = $9
				OR LOWER(COALESCE(member_user.username, '')) LIKE $10
				OR LOWER(COALESCE(member_user.first_name, '')) LIKE $10
				OR LOWER(COALESCE(member_user.last_name, '')) LIKE $10
			)
			AND ($11::timestamptz IS NULL OR a.created_at >= $11)
			AND ($12::timestamptz IS NULL OR a.created_at < $12)
		ORDER BY a.created_at DESC, a.id DESC
		LIMIT $13
	`, actorRaw, actorLike, actionRaw, actionLike, targetRaw, targetLike, orgRaw, orgLike, userRaw, userLike, startAt, endBefore, limit)
	if err != nil {
		return nil, fmt.Errorf("list admin audit events: %w", err)
	}
	defer rows.Close()

	events := make([]types.AdminAuditEventView, 0)
	for rows.Next() {
		var row types.AdminAuditEventView
		var actorFirstName string
		var actorLastName string
		var reason sql.NullString
		var metadata []byte
		var orgID sql.NullInt64
		var orgName sql.NullString
		var userID sql.NullInt64
		var userUsername sql.NullString
		var userFirstName sql.NullString
		var userLastName sql.NullString
		if err := rows.Scan(
			&row.ID,
			&row.ActorMemberID,
			&row.ActorUsername,
			&actorFirstName,
			&actorLastName,
			&row.Action,
			&row.Target,
			&reason,
			&metadata,
			&row.CreatedAt,
			&orgID,
			&orgName,
			&userID,
			&userUsername,
			&userFirstName,
			&userLastName,
		); err != nil {
			return nil, fmt.Errorf("scan admin audit event: %w", err)
		}

		row.ActorName = adminAuditDisplayName(actorFirstName, actorLastName, row.ActorUsername)
		if reason.Valid {
			row.Reason = &reason.String
		}
		row.Metadata = json.RawMessage(append([]byte(nil), metadata...))
		if orgID.Valid {
			v := orgID.Int64
			row.OrganizationID = &v
		}
		if orgName.Valid {
			v := orgName.String
			row.OrganizationName = &v
		}
		if userID.Valid {
			v := userID.Int64
			row.UserMemberID = &v
		}
		if userUsername.Valid {
			v := userUsername.String
			row.UserUsername = &v
		}
		if userID.Valid {
			name := adminAuditDisplayName(userFirstName.String, userLastName.String, userUsername.String)
			if strings.TrimSpace(name) != "" {
				row.UserName = &name
			}
		}

		events = append(events, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list admin audit events: %w", err)
	}

	return events, nil
}

func adminAuditLikeValue(raw string) (string, string) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return "", ""
	}
	return trimmed, "%" + trimmed + "%"
}

func adminAuditDisplayName(firstName, lastName, fallback string) string {
	full := strings.TrimSpace(strings.TrimSpace(firstName) + " " + strings.TrimSpace(lastName))
	if full != "" {
		return full
	}
	return strings.TrimSpace(fallback)
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
