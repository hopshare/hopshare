package http_test

import (
	"net/url"
	"regexp"
	"strings"
	"testing"

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

	t.Run("AUTH-08 GET /logout clears session", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		member := createSeededMember(t, ctx, db, "auth_logout_get", suffix)
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Username, member.Password)
		actor.Login()
		requireRedirectPath(t, actor.Get("/logout"), "/")
		requireRedirectPath(t, actor.Get("/my-hopshare"), "/login")
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

	t.Run("AUTH-14 POST /forgot-password known email includes demo reset link", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		member := createSeededMember(t, ctx, db, "forgot_known", suffix)
		server := newHTTPServer(t, db)
		anon := newTestActor(t, "anon", server.URL, "", "")
		body := requireStatus(t, anon.PostForm("/forgot-password", formKV(
			"email", member.Member.Email,
		)), 200)
		requireBodyContains(t, body, "If an account exists for that email")
		requireBodyContains(t, body, "/reset-password?token=")
	})

	t.Run("AUTH-15 POST /forgot-password unknown email does not include demo link", func(t *testing.T) {
		server := newHTTPServer(t, db)
		anon := newTestActor(t, "anon", server.URL, "", "")
		body := requireStatus(t, anon.PostForm("/forgot-password", formKV(
			"email", "unknown_email_"+uniqueTestSuffix()+"@example.com",
		)), 200)
		requireBodyContains(t, body, "If an account exists for that email")
		requireBodyNotContains(t, body, "Demo: use this link now")
	})

	t.Run("AUTH-16 reset-password token flow updates password and token is single-use", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		member := createSeededMember(t, ctx, db, "reset_flow", suffix)
		server := newHTTPServer(t, db)
		anon := newTestActor(t, "anon", server.URL, "", "")

		forgotBody := requireStatus(t, anon.PostForm("/forgot-password", formKV(
			"email", member.Member.Email,
		)), 200)
		token := extractResetToken(t, forgotBody)
		if token == "" {
			t.Fatalf("expected reset token")
		}

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
}

func formKV(kv ...string) url.Values {
	out := make(url.Values)
	for i := 0; i+1 < len(kv); i += 2 {
		out.Set(kv[i], kv[i+1])
	}
	return out
}

func extractResetToken(t *testing.T, body string) string {
	t.Helper()
	re := regexp.MustCompile(`/reset-password\?token=([a-f0-9]+)`)
	match := re.FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("could not find reset token in body %q", body)
	}
	return strings.TrimSpace(match[1])
}
