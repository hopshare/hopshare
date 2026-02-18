package http_test

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"sync"
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
	cookieSecure := false
	server := httptest.NewServer(apphttp.NewRouterWithOptions(db, apphttp.RouterOptions{
		CookieSecure: &cookieSecure,
	}))
	t.Cleanup(server.Close)
	return server
}

func newHTTPServerWithSessions(t *testing.T, db *sql.DB, sessions *auth.SessionManager) *httptest.Server {
	t.Helper()
	cookieSecure := false
	server := httptest.NewServer(apphttp.NewRouterWithOptions(db, apphttp.RouterOptions{
		Sessions:     sessions,
		CookieSecure: &cookieSecure,
	}))
	t.Cleanup(server.Close)
	return server
}

func newHTTPServerWithAdmins(t *testing.T, db *sql.DB, adminUsernames []string) *httptest.Server {
	t.Helper()
	cookieSecure := false
	server := httptest.NewServer(apphttp.NewRouterWithOptions(db, apphttp.RouterOptions{
		AdminUsernames: adminUsernames,
		CookieSecure:   &cookieSecure,
	}))
	t.Cleanup(server.Close)
	return server
}

func newHTTPServerWithPasswordResetEmailSender(t *testing.T, db *sql.DB, sender apphttp.PasswordResetEmailSender) *httptest.Server {
	t.Helper()
	cookieSecure := false
	server := httptest.NewServer(apphttp.NewRouterWithOptions(db, apphttp.RouterOptions{
		PasswordResetEmailSender: sender,
		PublicBaseURL:            "https://hopshare.test",
		CookieSecure:             &cookieSecure,
	}))
	t.Cleanup(server.Close)
	return server
}

func newHTTPServerWithAdminsAndPasswordResetEmailSender(t *testing.T, db *sql.DB, adminUsernames []string, sender apphttp.PasswordResetEmailSender) *httptest.Server {
	t.Helper()
	cookieSecure := false
	server := httptest.NewServer(apphttp.NewRouterWithOptions(db, apphttp.RouterOptions{
		AdminUsernames:           adminUsernames,
		PasswordResetEmailSender: sender,
		PublicBaseURL:            "https://hopshare.test",
		CookieSecure:             &cookieSecure,
	}))
	t.Cleanup(server.Close)
	return server
}

type sentPasswordResetEmail struct {
	ToEmail  string
	ResetURL string
}

type sentVerificationEmail struct {
	ToEmail   string
	VerifyURL string
}

type recordingPasswordResetEmailSender struct {
	mu                 sync.Mutex
	resetEmails        []sentPasswordResetEmail
	verificationEmails []sentVerificationEmail
}

func (s *recordingPasswordResetEmailSender) SendPasswordReset(_ context.Context, toEmail, resetURL string) error {
	s.mu.Lock()
	s.resetEmails = append(s.resetEmails, sentPasswordResetEmail{
		ToEmail:  toEmail,
		ResetURL: resetURL,
	})
	s.mu.Unlock()
	return nil
}

func (s *recordingPasswordResetEmailSender) SendEmailVerification(_ context.Context, toEmail, verifyURL string) error {
	s.mu.Lock()
	s.verificationEmails = append(s.verificationEmails, sentVerificationEmail{
		ToEmail:   toEmail,
		VerifyURL: verifyURL,
	})
	s.mu.Unlock()
	return nil
}

func (s *recordingPasswordResetEmailSender) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.resetEmails)
}

func (s *recordingPasswordResetEmailSender) Last() (sentPasswordResetEmail, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.resetEmails) == 0 {
		return sentPasswordResetEmail{}, false
	}
	return s.resetEmails[len(s.resetEmails)-1], true
}

func (s *recordingPasswordResetEmailSender) VerificationCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.verificationEmails)
}

func (s *recordingPasswordResetEmailSender) LastVerification() (sentVerificationEmail, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.verificationEmails) == 0 {
		return sentVerificationEmail{}, false
	}
	return s.verificationEmails[len(s.verificationEmails)-1], true
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
