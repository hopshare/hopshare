package http_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"hopshare/internal/service"
	"hopshare/internal/types"
)

func listMemberNotificationsForTest(t *testing.T, ctx context.Context, db *sql.DB, memberID int64) []types.MemberNotification {
	t.Helper()

	notifications, err := service.ListMemberNotifications(ctx, db, memberID, 200)
	if err != nil {
		t.Fatalf("list notifications member=%d: %v", memberID, err)
	}
	return notifications
}

func countMemberNotificationsContaining(t *testing.T, ctx context.Context, db *sql.DB, memberID int64, text string) int {
	t.Helper()

	count := 0
	for _, notification := range listMemberNotificationsForTest(t, ctx, db, memberID) {
		if strings.Contains(notification.Text, text) {
			count++
		}
	}
	return count
}

func hasMemberNotification(t *testing.T, ctx context.Context, db *sql.DB, memberID int64, text string, href string) bool {
	t.Helper()

	for _, notification := range listMemberNotificationsForTest(t, ctx, db, memberID) {
		if !strings.Contains(notification.Text, text) {
			continue
		}
		actualHref := ""
		if notification.Href != nil {
			actualHref = *notification.Href
		}
		if actualHref == href {
			return true
		}
	}
	return false
}
