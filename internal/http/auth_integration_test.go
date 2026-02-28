package http_test

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"hopshare/internal/auth"
	apphttp "hopshare/internal/http"
	"hopshare/internal/service"
)

func TestAuthHTTPMatrix(t *testing.T) {
	db := requireHTTPTestDB(t)

	t.Run("AUTH-01 GET /login unauthenticated renders login page", func(t *testing.T) {
		server := newHTTPServer(t, db)
		anon := newTestActor(t, "anon", server.URL, "", "")

		body := requireStatus(t, anon.Get("/login"), 200)
		requireBodyContains(t, body, "Log in")
		requireBodyNotContains(t, body, "Need a new verification email?")
		requireBodyNotContains(t, body, "/verify-email/resend")
	})

	t.Run("AUTH-01C GET /login with username query pre-fills username field", func(t *testing.T) {
		server := newHTTPServer(t, db)
		anon := newTestActor(t, "anon", server.URL, "", "")
		prefill := "login_prefill_user"
		body := requireStatus(t, anon.Get("/login?username="+url.QueryEscape(prefill)), http.StatusOK)
		requireBodyContains(t, body, `name="username" value="`+prefill+`"`)
	})

	t.Run("AUTH-01A secure-cookie mode sets Secure/HttpOnly/SameSite flags", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		member := createSeededMember(t, ctx, db, "auth_cookie_secure", suffix)
		cookieSecure := true
		server := httptest.NewTLSServer(apphttp.NewRouterWithOptions(db, apphttp.RouterOptions{
			CookieSecure: &cookieSecure,
		}))
		t.Cleanup(server.Close)

		client := server.Client()
		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatalf("create cookie jar: %v", err)
		}
		client.Jar = jar
		client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		}

		loginWithNext := server.URL + "/login?next=" + url.QueryEscape("/organization/test-org")
		getResp, err := client.Get(loginWithNext)
		if err != nil {
			t.Fatalf("get login page: %v", err)
		}
		requireStatus(t, getResp, http.StatusOK)
		requireSetCookieAttributes(t, getResp, "hopshare_csrf", "Secure", "HttpOnly", "SameSite=Lax")
		requireSetCookieAttributes(t, getResp, "hopshare_post_auth_redirect", "Secure", "HttpOnly", "SameSite=Lax")

		baseURL, err := url.Parse(server.URL)
		if err != nil {
			t.Fatalf("parse server url: %v", err)
		}
		csrfToken := ""
		for _, c := range jar.Cookies(baseURL) {
			if c.Name == testCSRFCookieName {
				csrfToken = c.Value
				break
			}
		}
		if csrfToken == "" {
			t.Fatalf("expected csrf token in cookie jar")
		}

		loginResp, err := client.PostForm(server.URL+"/login", formKV(
			"username", member.Member.Username,
			"password", member.Password,
			testCSRFFieldName, csrfToken,
		))
		if err != nil {
			t.Fatalf("post login: %v", err)
		}
		requireSetCookieAttributes(t, loginResp, "hopshare_session", "Secure", "HttpOnly", "SameSite=Lax")
		requireRedirectPath(t, loginResp, "/organization/test-org")
	})

	t.Run("AUTH-01B secure-cookie mode still allows login over plain HTTP for local dev", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		member := createSeededMember(t, ctx, db, "auth_cookie_secure_http", suffix)
		cookieSecure := true
		server := httptest.NewServer(apphttp.NewRouterWithOptions(db, apphttp.RouterOptions{
			CookieSecure: &cookieSecure,
		}))
		t.Cleanup(server.Close)

		actor := newTestActor(t, "secure-http-actor", server.URL, member.Member.Username, member.Password)
		resp := actor.PostForm("/login", formKV(
			"username", member.Member.Username,
			"password", member.Password,
		))
		requireSetCookieNotContains(t, resp, "hopshare_session", "Secure")
		requireRedirectPath(t, resp, "/my-hopshare")
		requireStatus(t, actor.Get("/my-hopshare"), http.StatusOK)
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

	t.Run("AUTH-10A signup sends verification email and blocks login until verified", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		emailSender := &recordingPasswordResetEmailSender{}
		server := newHTTPServerWithPasswordResetEmailSender(t, db, emailSender)
		anon := newTestActor(t, "anon", server.URL, "", "")

		email := "verify_signup_" + suffix + "@example.com"
		requireRedirectPath(t, anon.PostForm("/signup", formKV(
			"first_name", "Verify",
			"last_name", "Signup",
			"email", email,
			"password", "Password123!",
			"preferred_contact_method", "email",
		)), "/signup-success")

		member, err := service.GetMemberByEmail(ctx, db, email)
		if err != nil {
			t.Fatalf("load signed up member: %v", err)
		}
		if member.Verified {
			t.Fatalf("expected newly signed up member to be unverified")
		}
		if emailSender.VerificationCount() != 1 {
			t.Fatalf("expected one verification email, got %d", emailSender.VerificationCount())
		}
		verifyEmail, ok := emailSender.LastVerification()
		if !ok {
			t.Fatalf("expected verification email")
		}
		if verifyEmail.ToEmail != email {
			t.Fatalf("verification email recipient mismatch: got=%q want=%q", verifyEmail.ToEmail, email)
		}
		if verifyEmail.Username != member.Username {
			t.Fatalf("verification email username mismatch: got=%q want=%q", verifyEmail.Username, member.Username)
		}
		verifyToken := extractVerifyTokenFromURL(t, verifyEmail.VerifyURL)
		if verifyToken == "" {
			t.Fatalf("expected non-empty verification token")
		}

		loginBody := requireStatus(t, anon.PostForm("/login", formKV(
			"username", member.Username,
			"password", "Password123!",
		)), http.StatusOK)
		requireBodyContains(t, loginBody, "Please verify your email before logging in.")

		verifyURL, err := url.Parse(verifyEmail.VerifyURL)
		if err != nil {
			t.Fatalf("parse verification url: %v", err)
		}
		verifyLoc := requireRedirectPath(t, anon.Get(verifyURL.RequestURI()), "/login")
		requireQueryValue(t, verifyLoc, "success", "Email verified. You can now log in.")
		requireQueryValue(t, verifyLoc, "username", member.Username)
		prefilledLoginBody := requireStatus(t, anon.Get(verifyLoc.RequestURI()), http.StatusOK)
		requireBodyContains(t, prefilledLoginBody, `name="username" value="`+member.Username+`"`)

		verifiedMember, err := service.GetMemberByID(ctx, db, member.ID)
		if err != nil {
			t.Fatalf("reload verified member: %v", err)
		}
		if !verifiedMember.Verified {
			t.Fatalf("expected verified member after verification link")
		}

		loginActor := newTestActor(t, "verified_member", server.URL, member.Username, "Password123!")
		loginActor.Login()
	})

	t.Run("AUTH-10A1 signup with feature email disabled logs in without verification", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		emailSender := &recordingPasswordResetEmailSender{}
		server := newHTTPServerWithFeatureEmailAndPasswordResetEmailSender(t, db, false, emailSender)
		anon := newTestActor(t, "anon", server.URL, "", "")

		email := "feature_email_off_" + suffix + "@example.com"
		requireRedirectPath(t, anon.PostForm("/signup", formKV(
			"first_name", "Feature",
			"last_name", "Disabled",
			"email", email,
			"password", "Password123!",
			"preferred_contact_method", "email",
		)), "/signup-success")

		member, err := service.GetMemberByEmail(ctx, db, email)
		if err != nil {
			t.Fatalf("load signed up member: %v", err)
		}
		if !member.Verified {
			t.Fatalf("expected newly signed up member to be verified when feature email is disabled")
		}
		if emailSender.VerificationCount() != 0 {
			t.Fatalf("expected no verification email when feature email is disabled, got %d", emailSender.VerificationCount())
		}

		loginActor := newTestActor(t, "feature_disabled_member", server.URL, member.Username, "Password123!")
		loginActor.Login()
	})

	t.Run("AUTH-10B verification token is one-time and expired tokens fail", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		emailSender := &recordingPasswordResetEmailSender{}
		server := newHTTPServerWithPasswordResetEmailSender(t, db, emailSender)
		anon := newTestActor(t, "anon", server.URL, "", "")

		email := "verify_reuse_" + suffix + "@example.com"
		requireRedirectPath(t, anon.PostForm("/signup", formKV(
			"first_name", "Verify",
			"last_name", "Reuse",
			"email", email,
			"password", "Password123!",
			"preferred_contact_method", "email",
		)), "/signup-success")

		verifyEmail, ok := emailSender.LastVerification()
		if !ok {
			t.Fatalf("expected verification email")
		}
		verifyToken := extractVerifyTokenFromURL(t, verifyEmail.VerifyURL)

		requireRedirectPath(t, anon.Get("/verify-email?token="+url.QueryEscape(verifyToken)), "/login")
		reuseLoc := requireRedirectPath(t, anon.Get("/verify-email?token="+url.QueryEscape(verifyToken)), "/login")
		requireQueryValue(t, reuseLoc, "error", "Invalid or expired verification link.")

		member, err := service.GetMemberByEmail(ctx, db, email)
		if err != nil {
			t.Fatalf("load signed up member: %v", err)
		}
		if err := service.UpdateMemberVerified(ctx, db, member.ID, false); err != nil {
			t.Fatalf("set member unverified: %v", err)
		}
		expiredToken, err := service.IssueMemberToken(ctx, db, service.IssueMemberTokenParams{
			MemberID: member.ID,
			Purpose:  service.MemberTokenPurposeEmailVerification,
			TTL:      time.Hour,
		})
		if err != nil {
			t.Fatalf("issue expired token: %v", err)
		}
		expiredTokenID, _, ok := strings.Cut(expiredToken, ".")
		if !ok || strings.TrimSpace(expiredTokenID) == "" {
			t.Fatalf("parse expired token id: %q", expiredToken)
		}
		if _, err := db.ExecContext(ctx, `
			UPDATE member_tokens
			SET created_at = NOW() - INTERVAL '2 minute',
			    expires_at = NOW() - INTERVAL '1 minute'
			WHERE token_id = $1
			  AND purpose = $2
		`, expiredTokenID, service.MemberTokenPurposeEmailVerification); err != nil {
			t.Fatalf("expire verification token: %v", err)
		}
		expiredLoc := requireRedirectPath(t, anon.Get("/verify-email?token="+url.QueryEscape(expiredToken)), "/login")
		requireQueryValue(t, expiredLoc, "error", "Invalid or expired verification link.")
	})

	t.Run("AUTH-10C POST /verify-email/resend returns generic success and only sends for unverified members", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		emailSender := &recordingPasswordResetEmailSender{}
		server := newHTTPServerWithPasswordResetEmailSender(t, db, emailSender)
		anon := newTestActor(t, "anon", server.URL, "", "")

		email := "verify_resend_" + suffix + "@example.com"
		requireRedirectPath(t, anon.PostForm("/signup", formKV(
			"first_name", "Verify",
			"last_name", "Resend",
			"email", email,
			"password", "Password123!",
			"preferred_contact_method", "email",
		)), "/signup-success")
		initialCount := emailSender.VerificationCount()

		body := requireStatus(t, anon.PostForm("/verify-email/resend", formKV(
			"email", email,
		)), http.StatusOK)
		requireBodyContains(t, body, "If an account exists and still needs verification")
		if emailSender.VerificationCount() != initialCount+1 {
			t.Fatalf("expected verification email resend for unverified member, before=%d after=%d", initialCount, emailSender.VerificationCount())
		}

		unknownBody := requireStatus(t, anon.PostForm("/verify-email/resend", formKV(
			"email", "missing_verify_resend_"+suffix+"@example.com",
		)), http.StatusOK)
		requireBodyContains(t, unknownBody, "If an account exists and still needs verification")
		if emailSender.VerificationCount() != initialCount+1 {
			t.Fatalf("expected verification email count to remain unchanged for unknown member, got %d", emailSender.VerificationCount())
		}

		member, err := service.GetMemberByEmail(ctx, db, email)
		if err != nil {
			t.Fatalf("load member: %v", err)
		}
		if err := service.UpdateMemberVerified(ctx, db, member.ID, true); err != nil {
			t.Fatalf("mark member verified: %v", err)
		}
		verifiedBody := requireStatus(t, anon.PostForm("/verify-email/resend", formKV(
			"email", email,
		)), http.StatusOK)
		requireBodyContains(t, verifiedBody, "If an account exists and still needs verification")
		if emailSender.VerificationCount() != initialCount+1 {
			t.Fatalf("expected no resend for already verified member, got %d", emailSender.VerificationCount())
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

	t.Run("AUTH-16C reset-password revokes active sessions for the member", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		member := createSeededMember(t, ctx, db, "reset_revokes_sessions", suffix)
		resetSender := &recordingPasswordResetEmailSender{}
		server := newHTTPServerWithPasswordResetEmailSender(t, db, resetSender)

		memberActor := newTestActor(t, "member_active_session", server.URL, member.Member.Username, member.Password)
		memberActor.Login()
		requireStatus(t, memberActor.Get("/my-hopshare"), http.StatusOK)

		anon := newTestActor(t, "anon_reset_revoke", server.URL, "", "")
		requireStatus(t, anon.PostForm("/forgot-password", formKV(
			"email", member.Member.Email,
		)), http.StatusOK)
		resetEmail, ok := resetSender.Last()
		if !ok {
			t.Fatalf("expected password reset email")
		}
		token := extractResetTokenFromURL(t, resetEmail.ResetURL)

		requireRedirectPath(t, anon.PostForm("/reset-password", formKV(
			"token", token,
			"new_password", "RevokeSessions123!",
			"confirm_password", "RevokeSessions123!",
		)), "/login")

		requireRedirectPath(t, memberActor.Get("/my-hopshare"), "/login")

		newLoginActor := newTestActor(t, "member_after_revoke", server.URL, member.Member.Username, "RevokeSessions123!")
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

func extractVerifyTokenFromURL(t *testing.T, verifyURL string) string {
	t.Helper()
	parsed, err := url.Parse(strings.TrimSpace(verifyURL))
	if err != nil {
		t.Fatalf("parse verify url %q: %v", verifyURL, err)
	}
	token := strings.TrimSpace(parsed.Query().Get("token"))
	if token == "" {
		t.Fatalf("could not find verify token in url %q", verifyURL)
	}
	return token
}

func requireSetCookieAttributes(t *testing.T, resp *http.Response, cookieName string, attrs ...string) {
	t.Helper()
	setCookies := resp.Header.Values("Set-Cookie")
	if len(setCookies) == 0 {
		t.Fatalf("expected Set-Cookie headers for %q", cookieName)
	}
	prefix := cookieName + "="
	for _, header := range setCookies {
		if !strings.HasPrefix(header, prefix) {
			continue
		}
		for _, attr := range attrs {
			if !strings.Contains(header, attr) {
				t.Fatalf("cookie %q missing attr %q in %q", cookieName, attr, header)
			}
		}
		return
	}
	t.Fatalf("cookie %q not found in Set-Cookie headers: %v", cookieName, setCookies)
}

func requireSetCookieNotContains(t *testing.T, resp *http.Response, cookieName string, attr string) {
	t.Helper()
	setCookies := resp.Header.Values("Set-Cookie")
	if len(setCookies) == 0 {
		t.Fatalf("expected Set-Cookie headers for %q", cookieName)
	}
	prefix := cookieName + "="
	for _, header := range setCookies {
		if !strings.HasPrefix(header, prefix) {
			continue
		}
		if strings.Contains(header, attr) {
			t.Fatalf("cookie %q unexpectedly contained attr %q in %q", cookieName, attr, header)
		}
		return
	}
	t.Fatalf("cookie %q not found in Set-Cookie headers: %v", cookieName, setCookies)
}
