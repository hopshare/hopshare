package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"hopshare/internal/types"
)

func TestWriteAdminAuditEvent(t *testing.T) {
	db := require_db(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	member := createAuditTestMember(t, ctx, db, "write")
	occurredAt := time.Date(2026, time.January, 8, 15, 4, 5, 0, time.UTC)
	metadata := json.RawMessage(`{"tab":"app","source":"test"}`)

	event, err := WriteAdminAuditEvent(ctx, db, WriteAdminAuditEventParams{
		ActorMemberID: member.ID,
		Action:        "admin.organization.update",
		Target:        "organization:42",
		Metadata:      metadata,
		OccurredAt:    occurredAt,
	})
	if err != nil {
		t.Fatalf("WriteAdminAuditEvent returned error: %v", err)
	}

	if event.ID == 0 {
		t.Fatalf("expected event ID to be set")
	}
	if event.ActorMemberID != member.ID {
		t.Fatalf("unexpected actor: got=%d want=%d", event.ActorMemberID, member.ID)
	}
	if event.Action != "admin.organization.update" {
		t.Fatalf("unexpected action: %q", event.Action)
	}
	if event.Target != "organization:42" {
		t.Fatalf("unexpected target: %q", event.Target)
	}
	if event.Reason != nil {
		t.Fatalf("expected nil reason for non-sensitive action")
	}
	if !event.CreatedAt.Equal(occurredAt) {
		t.Fatalf("unexpected created_at: got=%s want=%s", event.CreatedAt.UTC(), occurredAt.UTC())
	}

	var stored struct {
		ID            int64
		ActorMemberID int64
		Action        string
		Target        string
		Reason        sql.NullString
		Metadata      []byte
		CreatedAt     time.Time
	}
	if err := db.QueryRowContext(ctx, `
		SELECT id, actor_member_id, action, target, reason, metadata, created_at
		FROM admin_audit_events
		WHERE id = $1
	`, event.ID).Scan(
		&stored.ID,
		&stored.ActorMemberID,
		&stored.Action,
		&stored.Target,
		&stored.Reason,
		&stored.Metadata,
		&stored.CreatedAt,
	); err != nil {
		t.Fatalf("query stored audit event: %v", err)
	}

	if stored.ActorMemberID != member.ID {
		t.Fatalf("unexpected stored actor: got=%d want=%d", stored.ActorMemberID, member.ID)
	}
	if stored.Action != "admin.organization.update" || stored.Target != "organization:42" {
		t.Fatalf("unexpected stored action/target: action=%q target=%q", stored.Action, stored.Target)
	}
	if stored.Reason.Valid {
		t.Fatalf("expected stored reason to be NULL")
	}
	if !stored.CreatedAt.Equal(occurredAt) {
		t.Fatalf("unexpected stored created_at: got=%s want=%s", stored.CreatedAt.UTC(), occurredAt.UTC())
	}

	var metadataMap map[string]string
	if err := json.Unmarshal(stored.Metadata, &metadataMap); err != nil {
		t.Fatalf("unmarshal stored metadata: %v", err)
	}
	if metadataMap["tab"] != "app" || metadataMap["source"] != "test" {
		t.Fatalf("unexpected stored metadata: %#v", metadataMap)
	}
}

func TestWriteAdminAuditEventReasonRequired(t *testing.T) {
	db := require_db(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	member := createAuditTestMember(t, ctx, db, "reason_required")

	_, err := WriteAdminAuditEvent(ctx, db, WriteAdminAuditEventParams{
		ActorMemberID: member.ID,
		Action:        "admin.organization.disable",
		Target:        "organization:101",
		Metadata:      json.RawMessage(`{"attempt":"destructive-without-reason"}`),
	})
	if !errors.Is(err, ErrAuditReasonRequired) {
		t.Fatalf("expected ErrAuditReasonRequired for destructive action, got %v", err)
	}

	_, err = WriteAdminAuditEvent(ctx, db, WriteAdminAuditEventParams{
		ActorMemberID: member.ID,
		Action:        "admin.feature_flag.toggle",
		Target:        "feature:beta",
		Sensitive:     true,
		Metadata:      json.RawMessage(`{"attempt":"sensitive-without-reason"}`),
	})
	if !errors.Is(err, ErrAuditReasonRequired) {
		t.Fatalf("expected ErrAuditReasonRequired for sensitive action, got %v", err)
	}

	var count int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM admin_audit_events
		WHERE actor_member_id = $1
		  AND action IN ('admin.organization.disable', 'admin.feature_flag.toggle')
	`, member.ID).Scan(&count); err != nil {
		t.Fatalf("count audit events: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no audit rows for rejected writes, got %d", count)
	}
}

func createAuditTestMember(t *testing.T, ctx context.Context, db *sql.DB, prefix string) types.Member {
	t.Helper()

	username := fmt.Sprintf("audit_%s_%d", prefix, time.Now().UnixNano())
	email := username + "@example.com"

	member, err := CreateMember(ctx, db, types.Member{
		FirstName:        "Audit",
		LastName:         "Tester",
		Username:         username,
		Email:            email,
		PasswordHash:     "hashed_password",
		PreferredContact: email,
		Enabled:          true,
		Verified:         true,
	})
	if err != nil {
		t.Fatalf("create audit test member: %v", err)
	}

	return member
}
