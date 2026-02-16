package http_test

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"testing"
	"time"

	"hopshare/internal/auth"
	apphttp "hopshare/internal/http"
	"hopshare/internal/service"
	"hopshare/internal/types"
)

func newTestContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 30*time.Second)
}

func newHTTPServer(t *testing.T, db *sql.DB) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(apphttp.NewRouter(db))
	t.Cleanup(server.Close)
	return server
}

func newHTTPServerWithSessions(t *testing.T, db *sql.DB, sessions *auth.SessionManager) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(apphttp.NewRouterWithSessions(db, sessions))
	t.Cleanup(server.Close)
	return server
}

func createOrganizationWithMembers(t *testing.T, ctx context.Context, db *sql.DB, suffix string, roleNames ...string) (types.Organization, map[string]seededMember) {
	t.Helper()

	members := make(map[string]seededMember, len(roleNames))
	for _, roleName := range roleNames {
		members[roleName] = createSeededMember(t, ctx, db, roleName, suffix)
	}

	owner, ok := members["owner"]
	if !ok {
		t.Fatalf("createOrganizationWithMembers requires role \"owner\"")
	}

	org, err := service.CreateOrganization(
		ctx,
		db,
		"HTTP Test Org "+suffix,
		"Test City",
		"TS",
		"Organization for HTTP integration tests.",
		owner.Member.ID,
	)
	if err != nil {
		t.Fatalf("create organization: %v", err)
	}

	for roleName, member := range members {
		if roleName == "owner" {
			continue
		}
		approveMemberForOrganization(t, ctx, db, org.ID, owner.Member.ID, member.Member.ID)
	}

	return org, members
}

func loginActorsForMembers(t *testing.T, serverURL string, members map[string]seededMember) map[string]*testActor {
	t.Helper()

	actors := make(map[string]*testActor, len(members))
	for roleName, member := range members {
		actor := newTestActor(t, roleName, serverURL, member.Member.Username, member.Password)
		actor.Login()
		actors[roleName] = actor
	}
	return actors
}
