package http_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"hopshare/internal/auth"
	"hopshare/internal/service"
)

func TestAuthHTTPMatrix(t *testing.T) {
	db := requireHTTPTestDB(t)

	t.Run("AUTH-01 GET /login unauthenticated renders login page", func(t *testing.T) {
		server := newHTTPServer(t, db)
		anon := newTestActor(t, "anon", server.URL, "", "")

		body := requireStatus(t, anon.Get("/login"), 200)
		requireBodyContains(t, body, "Log in")
	})

	t.Run("AUTH-02 POST /login valid credentials redirects to /my-hopshare", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		member := createSeededMember(t, ctx, db, "auth_login", suffix)
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Username, member.Password)
		actor.Login()
	})

	t.Run("AUTH-03 POST /login invalid credentials renders error", func(t *testing.T) {
		server := newHTTPServer(t, db)
		anon := newTestActor(t, "anon", server.URL, "", "")
		body := requireStatus(t, anon.PostForm("/login", formKV(
			"username", "does_not_exist",
			"password", "wrong_password",
		)), 200)
		requireBodyContains(t, body, "Invalid username or password.")
	})

	t.Run("AUTH-04 GET /login when already authenticated redirects to /my-hopshare", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		member := createSeededMember(t, ctx, db, "auth_login_redirect", suffix)
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Username, member.Password)
		actor.Login()
		requireRedirectPath(t, actor.Get("/login"), "/my-hopshare")
	})

	t.Run("AUTH-05 POST /login with valid next organization path redirects there", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		owner := createSeededMember(t, ctx, db, "auth_next_owner", suffix)
		org, err := service.CreateOrganization(ctx, db, "Auth Next Org "+suffix, "Test City", "TS", "Auth next redirect org.", owner.Member.ID)
		if err != nil {
			t.Fatalf("create org: %v", err)
		}
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "owner", server.URL, owner.Member.Username, owner.Password)
		requireRedirectPath(t, actor.PostForm("/login", formKV(
			"username", owner.Member.Username,
			"password", owner.Password,
			"next", "/organization/"+org.URLName,
		)), "/organization/"+org.URLName)
	})

	t.Run("AUTH-06 POST /login with invalid next ignores it", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		member := createSeededMember(t, ctx, db, "auth_next_invalid", suffix)
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Username, member.Password)
		requireRedirectPath(t, actor.PostForm("/login", formKV(
			"username", member.Member.Username,
			"password", member.Password,
			"next", "https://evil.example.com",
		)), "/my-hopshare")
	})

	t.Run("AUTH-07 GET protected route unauthenticated redirects to /login", func(t *testing.T) {
		server := newHTTPServer(t, db)
		anon := newTestActor(t, "anon", server.URL, "", "")
		requireRedirectPath(t, anon.Get("/my-hopshare"), "/login")
	})

	t.Run("AUTH-08 GET /logout is not allowed", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		member := createSeededMember(t, ctx, db, "auth_logout_method_guard", suffix)
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Username, member.Password)
		actor.Login()
		resp := actor.Get("/logout")
		requireStatus(t, resp, http.StatusMethodNotAllowed)
		if got := resp.Header.Get("Allow"); got != "POST" {
			t.Fatalf("expected Allow header POST for GET /logout, got %q", got)
		}
		requireStatus(t, actor.Get("/my-hopshare"), http.StatusOK)
	})

	t.Run("AUTH-09 POST /logout clears session", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		member := createSeededMember(t, ctx, db, "auth_logout_post", suffix)
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Username, member.Password)
		actor.Login()
		requireRedirectPath(t, actor.PostForm("/logout", formKV()), "/")
		requireRedirectPath(t, actor.Get("/my-hopshare"), "/login")
	})

	t.Run("AUTH-CSRF-01 POST /logout missing csrf token returns forbidden", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		member := createSeededMember(t, ctx, db, "auth_logout_missing_csrf", suffix)
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Username, member.Password)
		actor.Login()

		resp := actor.Request(http.MethodPost, "/logout", strings.NewReader(""), map[string]string{
			"Content-Type": "application/x-www-form-urlencoded",
		})
		requireStatus(t, resp, http.StatusForbidden)
		requireStatus(t, actor.Get("/my-hopshare"), http.StatusOK)
	})

	t.Run("AUTH-CSRF-02 POST /logout invalid csrf token returns forbidden", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		member := createSeededMember(t, ctx, db, "auth_logout_invalid_csrf", suffix)
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Username, member.Password)
		actor.Login()

		resp := actor.Request(http.MethodPost, "/logout", strings.NewReader("csrf_token=invalid"), map[string]string{
			"Content-Type": "application/x-www-form-urlencoded",
		})
		requireStatus(t, resp, http.StatusForbidden)
		requireStatus(t, actor.Get("/my-hopshare"), http.StatusOK)
	})

	t.Run("AUTH-10 POST /signup success creates member and redirects /signup-success", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		server := newHTTPServer(t, db)
		anon := newTestActor(t, "anon", server.URL, "", "")
		email := "signup_success_" + suffix + "@example.com"
		loc := requireRedirectPath(t, anon.PostForm("/signup", formKV(
			"first_name", "Signup",
			"last_name", "Success",
			"email", email,
			"password", "Password123!",
			"preferred_contact_method", "email",
			"city", "Austin",
			"state", "TX",
			"interests", "reading",
		)), "/signup-success")

		if next := loc.Query().Get("next"); next != "" {
			t.Fatalf("did not expect next query on signup success, got %q", next)
		}

		member, err := service.GetMemberByEmail(ctx, db, email)
		if err != nil {
			t.Fatalf("load signed up member: %v", err)
		}
		if member.ID == 0 {
			t.Fatalf("expected signed-up member id")
		}
	})

	t.Run("AUTH-11 POST /signup missing names shows validation error", func(t *testing.T) {
		server := newHTTPServer(t, db)
		anon := newTestActor(t, "anon", server.URL, "", "")
		body := requireStatus(t, anon.PostForm("/signup", formKV(
			"first_name", "",
			"last_name", "",
			"email", "missing_names@example.com",
			"password", "Password123!",
			"preferred_contact_method", "email",
		)), 200)
		requireBodyContains(t, body, "Please enter your first and last name.")
	})

	t.Run("AUTH-12 POST /signup invalid contact method shows validation error", func(t *testing.T) {
		server := newHTTPServer(t, db)
		anon := newTestActor(t, "anon", server.URL, "", "")
		body := requireStatus(t, anon.PostForm("/signup", formKV(
			"first_name", "Invalid",
			"last_name", "Contact",
			"email", "invalid_contact@example.com",
			"password", "Password123!",
			"preferred_contact_method", "pager",
		)), 200)
		requireBodyContains(t, body, "Please choose a preferred contact method.")
	})

	t.Run("AUTH-13 repeated signup base username gets unique username", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		server := newHTTPServer(t, db)
		anon := newTestActor(t, "anon", server.URL, "", "")
		emailA := "same_name_a_" + suffix + "@example.com"
		emailB := "same_name_b_" + suffix + "@example.com"

		requireRedirectPath(t, anon.PostForm("/signup", formKV(
			"first_name", "Same",
			"last_name", "Name",
			"email", emailA,
			"password", "Password123!",
			"preferred_contact_method", "email",
		)), "/signup-success")

		requireRedirectPath(t, anon.PostForm("/signup", formKV(
			"first_name", "Same",
			"last_name", "Name",
			"email", emailB,
			"password", "Password123!",
			"preferred_contact_method", "email",
		)), "/signup-success")

		memberA, err := service.GetMemberByEmail(ctx, db, emailA)
		if err != nil {
			t.Fatalf("load memberA: %v", err)
		}
		memberB, err := service.GetMemberByEmail(ctx, db, emailB)
		if err != nil {
			t.Fatalf("load memberB: %v", err)
		}
		if memberA.Username == memberB.Username {
			t.Fatalf("expected unique usernames, both were %q", memberA.Username)
		}
	})

	t.Run("AUTH-14 POST /forgot-password known email returns generic success and sends reset email", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		member := createSeededMember(t, ctx, db, "forgot_known", suffix)
		resetSender := &recordingPasswordResetEmailSender{}
		server := newHTTPServerWithPasswordResetEmailSender(t, db, resetSender)
		anon := newTestActor(t, "anon", server.URL, "", "")
		body := requireStatus(t, anon.PostForm("/forgot-password", formKV(
			"email", member.Member.Email,
		)), 200)
		requireBodyContains(t, body, "If an account exists for that email")
		requireBodyNotContains(t, body, "/reset-password?token=")
		if resetSender.Count() != 1 {
			t.Fatalf("expected one password reset email, got %d", resetSender.Count())
		}
		email, ok := resetSender.Last()
		if !ok {
			t.Fatalf("expected password reset email")
		}
		if email.ToEmail != member.Member.Email {
			t.Fatalf("password reset email recipient mismatch: got=%q want=%q", email.ToEmail, member.Member.Email)
		}
		token := extractResetTokenFromURL(t, email.ResetURL)
		if token == "" {
			t.Fatalf("expected reset token in sent email URL")
		}
	})

	t.Run("AUTH-15 POST /forgot-password unknown email returns same generic success and does not send email", func(t *testing.T) {
		resetSender := &recordingPasswordResetEmailSender{}
		server := newHTTPServerWithPasswordResetEmailSender(t, db, resetSender)
		anon := newTestActor(t, "anon", server.URL, "", "")
		body := requireStatus(t, anon.PostForm("/forgot-password", formKV(
			"email", "unknown_email_"+uniqueTestSuffix()+"@example.com",
		)), 200)
		requireBodyContains(t, body, "If an account exists for that email")
		requireBodyNotContains(t, body, "/reset-password?token=")
		requireBodyNotContains(t, body, "Demo: use this link now")
		if resetSender.Count() != 0 {
			t.Fatalf("expected no password reset email for unknown account, got %d", resetSender.Count())
		}
	})

	t.Run("AUTH-16 reset-password token flow updates password and token is single-use", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		member := createSeededMember(t, ctx, db, "reset_flow", suffix)
		resetSender := &recordingPasswordResetEmailSender{}
		server := newHTTPServerWithPasswordResetEmailSender(t, db, resetSender)
		anon := newTestActor(t, "anon", server.URL, "", "")

		forgotBody := requireStatus(t, anon.PostForm("/forgot-password", formKV(
			"email", member.Member.Email,
		)), 200)
		requireBodyContains(t, forgotBody, "If an account exists for that email")
		requireBodyNotContains(t, forgotBody, "/reset-password?token=")
		email, ok := resetSender.Last()
		if !ok {
			t.Fatalf("expected password reset email")
		}
		token := extractResetTokenFromURL(t, email.ResetURL)

		requireRedirectPath(t, anon.PostForm("/reset-password", formKV(
			"token", token,
			"new_password", "NewPassword123!",
			"confirm_password", "NewPassword123!",
		)), "/login")

		loginActor := newTestActor(t, "member", server.URL, member.Member.Username, member.Password)
		invalidBody := requireStatus(t, loginActor.PostForm("/login", formKV(
			"username", member.Member.Username,
			"password", member.Password,
		)), 200)
		requireBodyContains(t, invalidBody, "Invalid username or password.")

		newLoginActor := newTestActor(t, "member_new", server.URL, member.Member.Username, "NewPassword123!")
		newLoginActor.Login()

		reuseBody := requireStatus(t, anon.PostForm("/reset-password", formKV(
			"token", token,
			"new_password", "AnotherPassword123!",
			"confirm_password", "AnotherPassword123!",
		)), 200)
		requireBodyContains(t, reuseBody, "Invalid or expired token.")
	})

	t.Run("AUTH-16B reset-password works across server instances with db-backed token storage", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		member := createSeededMember(t, ctx, db, "reset_cross_instance", suffix)
		resetSender := &recordingPasswordResetEmailSender{}
		forgotServer := newHTTPServerWithPasswordResetEmailSender(t, db, resetSender)
		forgotActor := newTestActor(t, "forgot", forgotServer.URL, "", "")
		body := requireStatus(t, forgotActor.PostForm("/forgot-password", formKV(
			"email", member.Member.Email,
		)), 200)
		requireBodyContains(t, body, "If an account exists for that email")
		resetEmail, ok := resetSender.Last()
		if !ok {
			t.Fatalf("expected password reset email")
		}
		token := extractResetTokenFromURL(t, resetEmail.ResetURL)

		resetServer := newHTTPServerWithPasswordResetEmailSender(t, db, &recordingPasswordResetEmailSender{})
		resetActor := newTestActor(t, "reset", resetServer.URL, "", "")
		requireRedirectPath(t, resetActor.PostForm("/reset-password", formKV(
			"token", token,
			"new_password", "CrossInstancePassword123!",
			"confirm_password", "CrossInstancePassword123!",
		)), "/login")

		newLoginActor := newTestActor(t, "member_cross_instance", resetServer.URL, member.Member.Username, "CrossInstancePassword123!")
		newLoginActor.Login()
	})

	t.Run("AUTH-17 revoke-all sessions invalidates active member sessions", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		member := createSeededMember(t, ctx, db, "auth_revoke_all", suffix)

		sessions := auth.NewSessionManager()
		server := newHTTPServerWithSessions(t, db, sessions)
		actor := newTestActor(t, "member", server.URL, member.Member.Username, member.Password)
		actor.Login()

		requireStatus(t, actor.Get("/my-hopshare"), http.StatusOK)
		if revoked := sessions.RevokeAllForMember(member.Member.ID); revoked < 1 {
			t.Fatalf("expected at least one revoked session, got %d", revoked)
		}
		requireRedirectPath(t, actor.Get("/my-hopshare"), "/login")
	})

	t.Run("AUTH-18 GET /admin non-admin is forbidden and nav omits Admin link", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		member := createSeededMember(t, ctx, db, "auth_non_admin", suffix)
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Username, member.Password)
		actor.Login()

		requireStatus(t, actor.Get("/admin"), http.StatusForbidden)
		body := requireStatus(t, actor.Get("/my-hopshare"), http.StatusOK)
		requireBodyNotContains(t, body, `href="/admin"`)
	})

	t.Run("AUTH-19 GET /admin admin succeeds and nav includes Admin link", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		member := createSeededMember(t, ctx, db, "auth_admin", suffix)
		server := newHTTPServerWithAdmins(t, db, []string{"  " + strings.ToUpper(member.Member.Username) + "  "})
		actor := newTestActor(t, "member", server.URL, member.Member.Username, member.Password)
		actor.Login()

		adminBody := requireStatus(t, actor.Get("/admin"), http.StatusOK)
		requireBodyContains(t, adminBody, "App-Wide Metrics")
		homeBody := requireStatus(t, actor.Get("/my-hopshare"), http.StatusOK)
		requireBodyContains(t, homeBody, `href="/admin"`)
	})
}

func TestAuthRateLimitingHTTP(t *testing.T) {
	db := requireHTTPTestDB(t)
	const maxRequests = 10

	t.Run("RL-01 login endpoint throttles repeated POST attempts", func(t *testing.T) {
		server := newHTTPServer(t, db)
		anon := newTestActor(t, "anon", server.URL, "", "")

		for i := 0; i < maxRequests; i++ {
			resp := anon.PostForm("/login", formKV(
				"username", "unknown_user",
				"password", "wrong_password",
			))
			if resp.StatusCode == http.StatusTooManyRequests {
				t.Fatalf("request %d unexpectedly rate-limited", i+1)
			}
			_ = requireStatus(t, resp, http.StatusOK)
		}

		limited := anon.PostForm("/login", formKV(
			"username", "unknown_user",
			"password", "wrong_password",
		))
		requireStatus(t, limited, http.StatusTooManyRequests)
		if retry := limited.Header.Get("Retry-After"); retry == "" {
			t.Fatalf("expected Retry-After header on rate-limited response")
		}
	})

	t.Run("RL-02 signup endpoint throttles repeated POST attempts", func(t *testing.T) {
		server := newHTTPServer(t, db)
		anon := newTestActor(t, "anon", server.URL, "", "")

		for i := 0; i < maxRequests; i++ {
			resp := anon.PostForm("/signup", formKV(
				"first_name", "",
				"last_name", "",
				"email", "ratelimit_signup_"+uniqueTestSuffix()+"@example.com",
				"password", "Password123!",
				"preferred_contact_method", "email",
			))
			if resp.StatusCode == http.StatusTooManyRequests {
				t.Fatalf("request %d unexpectedly rate-limited", i+1)
			}
			_ = requireStatus(t, resp, http.StatusOK)
		}

		limited := anon.PostForm("/signup", formKV(
			"first_name", "",
			"last_name", "",
			"email", "ratelimit_signup_"+uniqueTestSuffix()+"@example.com",
			"password", "Password123!",
			"preferred_contact_method", "email",
		))
		requireStatus(t, limited, http.StatusTooManyRequests)
	})

	t.Run("RL-03 forgot-password endpoint throttles repeated POST attempts", func(t *testing.T) {
		server := newHTTPServer(t, db)
		anon := newTestActor(t, "anon", server.URL, "", "")

		for i := 0; i < maxRequests; i++ {
			resp := anon.PostForm("/forgot-password", formKV(
				"email", "ratelimit_forgot_"+uniqueTestSuffix()+"@example.com",
			))
			if resp.StatusCode == http.StatusTooManyRequests {
				t.Fatalf("request %d unexpectedly rate-limited", i+1)
			}
			_ = requireStatus(t, resp, http.StatusOK)
		}

		limited := anon.PostForm("/forgot-password", formKV(
			"email", "ratelimit_forgot_"+uniqueTestSuffix()+"@example.com",
		))
		requireStatus(t, limited, http.StatusTooManyRequests)
	})
}

func formKV(kv ...string) url.Values {
	out := make(url.Values)
	for i := 0; i+1 < len(kv); i += 2 {
		out.Set(kv[i], kv[i+1])
	}
	return out
}

func extractResetTokenFromURL(t *testing.T, resetURL string) string {
	t.Helper()
	parsed, err := url.Parse(strings.TrimSpace(resetURL))
	if err != nil {
		t.Fatalf("parse reset url %q: %v", resetURL, err)
	}
	token := strings.TrimSpace(parsed.Query().Get("token"))
	if token == "" {
		t.Fatalf("could not find reset token in url %q", resetURL)
	}
	return token
}
