package http

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/a-h/templ"
	"github.com/lib/pq"

	"hopshare/internal/auth"
	"hopshare/internal/service"
	"hopshare/internal/types"
	"hopshare/web/templates"
)

// Server bundles dependencies for HTTP handlers.
type Server struct {
	db                       *sql.DB
	sessions                 auth.SessionStore
	admins                   *adminSet
	authRateLimiter          *fixedWindowLimiter
	passwordResetEmailSender PasswordResetEmailSender
	featureEmail             bool
	publicBaseURL            string
	cookieSecure             bool
}

type HandlerFunc func(http.ResponseWriter, *http.Request)
type Middleware func(HandlerFunc) HandlerFunc

type contextKey string

const userContextKey contextKey = "currentUser"
const adminContextKey contextKey = "isAdmin"
const csrfContextKey contextKey = "csrfToken"

const postAuthRedirectCookieName = "hopshare_post_auth_redirect"
const csrfCookieName = "hopshare_csrf"
const csrfHeaderName = "X-CSRF-Token"
const csrfMaxRequestBodyBytes = 21 << 20
const authRateLimitWindow = time.Minute
const authRateLimitMaxRequests = 10
const defaultPublicBaseURL = "http://localhost:8080"

const cspPolicy = "default-src 'self'; script-src 'self' 'unsafe-eval' https://cdn.tailwindcss.com; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"

type PasswordResetEmailSender interface {
	SendPasswordReset(ctx context.Context, toEmail, resetURL string) error
	SendEmailVerification(ctx context.Context, toEmail, username, verifyURL string) error
}

type RouterOptions struct {
	Sessions                 auth.SessionStore
	AdminUsernames           []string
	PasswordResetEmailSender PasswordResetEmailSender
	FeatureEmail             *bool
	PublicBaseURL            string
	CookieSecure             *bool
}

// NewRouter wires the base HTTP routes.
func NewRouter(db *sql.DB) http.Handler {
	return NewRouterWithOptions(db, RouterOptions{})
}

// NewRouterWithSessions wires routes with an optional injected session manager.
// Passing nil uses the default in-memory session manager.
func NewRouterWithSessions(db *sql.DB, sessions auth.SessionStore) http.Handler {
	return NewRouterWithOptions(db, RouterOptions{
		Sessions: sessions,
	})
}

// NewRouterWithSessionsAndAdmins wires routes with optional injected session manager
// and admin usernames. Admin usernames are normalized to lowercase and deduplicated.
func NewRouterWithSessionsAndAdmins(db *sql.DB, sessions auth.SessionStore, adminUsernames []string) http.Handler {
	return NewRouterWithOptions(db, RouterOptions{
		Sessions:       sessions,
		AdminUsernames: adminUsernames,
	})
}

// NewRouterWithOptions wires routes with explicit dependencies and config.
func NewRouterWithOptions(db *sql.DB, opts RouterOptions) http.Handler {
	sessions := opts.Sessions
	if sessions == nil {
		sessions = auth.NewSessionManager()
	}
	passwordResetEmailSender := opts.PasswordResetEmailSender
	if passwordResetEmailSender == nil {
		passwordResetEmailSender = nopPasswordResetEmailSender{}
	}
	featureEmail := true
	if opts.FeatureEmail != nil {
		featureEmail = *opts.FeatureEmail
	}
	publicBaseURL := normalizePublicBaseURL(opts.PublicBaseURL)
	cookieSecure := true
	if opts.CookieSecure != nil {
		cookieSecure = *opts.CookieSecure
	}

	srv := &Server{
		db:                       db,
		sessions:                 sessions,
		admins:                   newAdminSet(opts.AdminUsernames),
		authRateLimiter:          newFixedWindowLimiter(authRateLimitMaxRequests, authRateLimitWindow),
		passwordResetEmailSender: passwordResetEmailSender,
		featureEmail:             featureEmail,
		publicBaseURL:            publicBaseURL,
		cookieSecure:             cookieSecure,
	}

	mux := http.NewServeMux()
	staticFS := http.FileServer(http.Dir("web/static"))
	mux.Handle("/static/", http.StripPrefix("/static/", staticFS))
	srv.register(mux, "/", srv.handleLanding, srv.requireMethod(http.MethodGet))
	srv.register(mux, "/login", srv.handleLogin, srv.requireMethod(http.MethodGet, http.MethodPost), srv.rateLimitAuthEndpoint("login"))
	srv.register(mux, "/signup", srv.handleSignup, srv.requireMethod(http.MethodGet, http.MethodPost), srv.rateLimitAuthEndpoint("signup"))
	srv.register(mux, "/signup-success", srv.handleSignupSuccess, srv.requireMethod(http.MethodGet))
	srv.register(mux, "/learn-more", srv.handleLearnMore, srv.requireMethod(http.MethodGet))
	srv.register(mux, "/terms", srv.handleTerms, srv.requireMethod(http.MethodGet))
	srv.register(mux, "/privacy", srv.handlePrivacy, srv.requireMethod(http.MethodGet))
	srv.register(mux, "/help", srv.handleHelp, srv.requireAuth(), srv.requireMethod(http.MethodGet))
	srv.register(mux, "/farewell", srv.handleFarewell, srv.requireMethod(http.MethodGet))
	srv.register(mux, "/admin", srv.handleAdmin, srv.requireAuth(), srv.requireMethod(http.MethodGet), srv.requireAdmin())
	srv.register(mux, "/admin/organizations/action", srv.handleAdminOrganizationAction, srv.requireAuth(), srv.requireMethod(http.MethodPost), srv.requireAdmin())
	srv.register(mux, "/admin/moderation/action", srv.handleAdminModerationAction, srv.requireAuth(), srv.requireMethod(http.MethodPost), srv.requireAdmin())
	srv.register(mux, "/admin/users/action", srv.handleAdminUserAction, srv.requireAuth(), srv.requireMethod(http.MethodPost), srv.requireAdmin())
	srv.register(mux, "/admin/messages/send", srv.handleAdminMessageSend, srv.requireAuth(), srv.requireMethod(http.MethodPost), srv.requireAdmin())
	srv.register(mux, "/admin/audit/export", srv.handleAdminAuditExport, srv.requireAuth(), srv.requireMethod(http.MethodGet), srv.requireAdmin())
	srv.register(mux, "/forgot-password", srv.handleForgotPassword, srv.requireMethod(http.MethodGet, http.MethodPost), srv.rateLimitAuthEndpoint("forgot-password"))
	srv.register(mux, "/reset-password", srv.handleResetPassword, srv.requireMethod(http.MethodGet, http.MethodPost))
	srv.register(mux, "/verify-email", srv.handleVerifyEmail, srv.requireMethod(http.MethodGet))
	srv.register(mux, "/verify-email/resend", srv.handleVerifyEmailResend, srv.requireMethod(http.MethodPost), srv.rateLimitAuthEndpoint("verify-email-resend"))
	srv.register(mux, "/my-hopshare", srv.handleMyHopshare, srv.requireAuth(), srv.requireMethod(http.MethodGet))
	srv.register(mux, "/my-hops", srv.handleMyHops, srv.requireAuth(), srv.requireMethod(http.MethodGet))
	srv.register(mux, "/profile", srv.handleProfile, srv.requireAuth(), srv.requireMethod(http.MethodGet, http.MethodPost))
	srv.register(mux, "/members/avatar", srv.handleMemberAvatar, srv.requireAuth(), srv.requireMethod(http.MethodGet))
	srv.register(mux, "/members/avatar/public", srv.handlePublicMemberAvatar, srv.requireMethod(http.MethodGet))
	srv.register(mux, "/messages", srv.handleMessages, srv.requireAuth(), srv.requireMethod(http.MethodGet))
	srv.register(mux, "/messages/unread-count", srv.handleUnreadMessageCount, srv.requireAuth(), srv.requireMethod(http.MethodGet))
	srv.register(mux, "/messages/delete", srv.handleDeleteMessage, srv.requireAuth(), srv.requireMethod(http.MethodPost))
	srv.register(mux, "/messages/reply", srv.handleReplyMessage, srv.requireAuth(), srv.requireMethod(http.MethodPost))
	srv.register(mux, "/messages/action", srv.handleMessageAction, srv.requireAuth(), srv.requireMethod(http.MethodPost))
	srv.register(mux, "/hops/create", srv.handleCreateHop, srv.requireAuth(), srv.requireMethod(http.MethodPost))
	srv.register(mux, "/hops/view", srv.handleHopDetails, srv.requireAuth(), srv.requireMethod(http.MethodGet))
	srv.register(mux, "/hops/privacy", srv.handleHopPrivacy, srv.requireAuth(), srv.requireMethod(http.MethodPost))
	srv.register(mux, "/hops/comments/create", srv.handleCreateHopComment, srv.requireAuth(), srv.requireMethod(http.MethodPost))
	srv.register(mux, "/hops/comments/report", srv.handleReportHopComment, srv.requireAuth(), srv.requireMethod(http.MethodPost))
	srv.register(mux, "/hops/images/upload", srv.handleUploadHopImage, srv.requireAuth(), srv.requireMethod(http.MethodPost))
	srv.register(mux, "/hops/images/delete", srv.handleDeleteHopImage, srv.requireAuth(), srv.requireMethod(http.MethodPost))
	srv.register(mux, "/hops/images/report", srv.handleReportHopImage, srv.requireAuth(), srv.requireMethod(http.MethodPost))
	srv.register(mux, "/hops/image", srv.handleHopImage, srv.requireAuth(), srv.requireMethod(http.MethodGet))
	srv.register(mux, "/hops/offer", srv.handleOfferHop, srv.requireAuth(), srv.requireMethod(http.MethodPost))
	srv.register(mux, "/hops/cancel", srv.handleCancelHop, srv.requireAuth(), srv.requireMethod(http.MethodPost))
	srv.register(mux, "/hops/complete", srv.handleCompleteHop, srv.requireAuth(), srv.requireMethod(http.MethodPost))
	srv.register(mux, "/organizations", srv.handleOrganizations, srv.requireMethod(http.MethodGet))
	srv.register(mux, "/organization", srv.handleOrganization, srv.requireMethod(http.MethodGet))
	srv.register(mux, "/organization/", srv.handleOrganization, srv.requireMethod(http.MethodGet))
	srv.register(mux, "/organizations/logo", srv.handleOrganizationLogo, srv.requireMethod(http.MethodGet))
	srv.register(mux, "/organizations/create", srv.handleCreateOrganization, srv.requireAuth(), srv.requireMethod(http.MethodGet, http.MethodPost))
	srv.register(mux, "/organizations/manage", srv.handleManageOrganization, srv.requireAuth(), srv.requireMethod(http.MethodGet, http.MethodPost))
	srv.register(mux, "/organizations/manage/request", srv.handleManageMembershipRequest, srv.requireAuth(), srv.requireMethod(http.MethodPost))
	srv.register(mux, "/organizations/manage/member/remove", srv.handleRemoveMember, srv.requireAuth(), srv.requireMethod(http.MethodPost))
	srv.register(mux, "/organizations/manage/member/role", srv.handleChangeMemberRole, srv.requireAuth(), srv.requireMethod(http.MethodPost))
	srv.register(mux, "/organizations/request", srv.handleRequestMembership, srv.requireAuth(), srv.requireMethod(http.MethodPost))
	srv.register(mux, "/logout", srv.handleLogout, srv.requireMethod(http.MethodPost))
	mux.HandleFunc("/healthz", handleHealthz)

	return mux
}

func (s *Server) handleLanding(w http.ResponseWriter, r *http.Request) {
	render(w, r, templates.Landing(s.currentUserEmailPtr(r)))
}

func (s *Server) handleLearnMore(w http.ResponseWriter, r *http.Request) {
	render(w, r, templates.LearnMore(s.currentUserEmailPtr(r)))
}

func (s *Server) handleHelp(w http.ResponseWriter, r *http.Request) {
	render(w, r, templates.Help(s.currentUserEmailPtr(r)))
}

func (s *Server) handleTerms(w http.ResponseWriter, r *http.Request) {
	render(w, r, templates.Terms(s.currentUserEmailPtr(r)))
}

func (s *Server) handlePrivacy(w http.ResponseWriter, r *http.Request) {
	render(w, r, templates.Privacy(s.currentUserEmailPtr(r)))
}

func (s *Server) handleFarewell(w http.ResponseWriter, r *http.Request) {
	render(w, r, templates.Farewell(s.currentUserEmailPtr(r)))
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	next := sanitizePostAuthRedirect(r.URL.Query().Get("next"))
	username := sanitizeLoginUsername(r.URL.Query().Get("username"))
	if next == "" {
		next = s.postAuthRedirectFromCookie(r)
	}
	if next != "" {
		s.setPostAuthRedirectCookie(w, r, next)
	}

	switch r.Method {
	case http.MethodGet:
		if s.currentUser(r) != nil {
			redirectTarget := "/my-hopshare"
			if next != "" {
				redirectTarget = next
			}
			http.Redirect(w, r, redirectTarget, http.StatusSeeOther)
			return
		}
		successMsg := r.URL.Query().Get("success")
		errorMsg := r.URL.Query().Get("error")
		render(w, r, templates.Login(nil, errorMsg, successMsg, next, username))
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		next = sanitizePostAuthRedirect(r.FormValue("next"))
		if next == "" {
			next = s.postAuthRedirectFromCookie(r)
		}
		if next != "" {
			s.setPostAuthRedirectCookie(w, r, next)
		}
		username = strings.TrimSpace(r.FormValue("username"))
		password := r.FormValue("password")

		member, err := service.AuthenticateMemberByUsername(r.Context(), s.db, username, password)
		if err != nil {
			if errors.Is(err, service.ErrInvalidCredentials) {
				render(w, r, templates.Login(nil, "Invalid username or password.", "", next, username))
				return
			}
			if errors.Is(err, service.ErrEmailNotVerified) {
				if s.featureEmail {
					render(w, r, templates.Login(nil, "Please verify your email before logging in. Check your inbox for a verification link.", "", next, username))
					return
				}

				member, err = service.GetMemberByUsername(r.Context(), s.db, username)
				if err != nil {
					if errors.Is(err, sql.ErrNoRows) {
						render(w, r, templates.Login(nil, "Invalid username or password.", "", next, username))
						return
					}
					log.Printf("lookup unverified member: %v", err)
					http.Error(w, "could not log in", http.StatusInternalServerError)
					return
				}
				if !member.Enabled {
					render(w, r, templates.Login(nil, "Invalid username or password.", "", next, username))
					return
				}
				if err := service.UpdateMemberVerified(r.Context(), s.db, member.ID, true); err != nil {
					log.Printf("auto-verify member=%d: %v", member.ID, err)
				}
			} else {
				log.Printf("authenticate member: %v", err)
				http.Error(w, "could not log in", http.StatusInternalServerError)
				return
			}
		}

		token, err := s.sessions.Create(member.ID)
		if err != nil {
			http.Error(w, "could not create session", http.StatusInternalServerError)
			return
		}

		s.setSessionCookie(w, r, token)

		if err := service.UpdateMemberLastLogin(r.Context(), s.db, member.ID, time.Now().UTC()); err != nil {
			log.Printf("update last login member=%d: %v", member.ID, err)
		}

		s.clearPostAuthRedirectCookie(w, r)
		redirectTarget := "/my-hopshare"
		if next != "" {
			redirectTarget = next
		}
		http.Redirect(w, r, redirectTarget, http.StatusSeeOther)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	next := sanitizePostAuthRedirect(r.URL.Query().Get("next"))
	if next == "" {
		next = s.postAuthRedirectFromCookie(r)
	}
	if next != "" {
		s.setPostAuthRedirectCookie(w, r, next)
	}

	switch r.Method {
	case http.MethodGet:
		render(w, r, templates.Signup(s.currentUserEmailPtr(r), templates.SignupForm{}, "", "", next))
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		next = sanitizePostAuthRedirect(r.FormValue("next"))
		if next == "" {
			next = s.postAuthRedirectFromCookie(r)
		}
		if next != "" {
			s.setPostAuthRedirectCookie(w, r, next)
		}

		form := templates.SignupForm{
			FirstName: strings.TrimSpace(r.FormValue("first_name")),
			LastName:  strings.TrimSpace(r.FormValue("last_name")),
			Email:     strings.TrimSpace(r.FormValue("email")),
			Password:  r.FormValue("password"),
			City:      strings.TrimSpace(r.FormValue("city")),
			State:     strings.TrimSpace(r.FormValue("state")),
		}
		firstName := form.FirstName
		lastName := form.LastName
		email := form.Email
		password := form.Password
		city := form.City
		state := form.State

		if firstName == "" || lastName == "" {
			render(w, r, templates.Signup(s.currentUserEmailPtr(r), form, "", "Please enter your first and last name.", next))
			return
		}

		strPtr := func(v string) *string {
			if v == "" {
				return nil
			}
			return &v
		}

		passwordHash, err := service.HashPassword(password)
		if err != nil {
			log.Printf("hash password failed: %v", err)
			render(w, r, templates.Signup(s.currentUserEmailPtr(r), form, "", "We could not process your request right now. Please try again.", next))
			return
		}

		baseUsername := deriveUsername(strings.TrimSpace(firstName+" "+lastName), email)
		var created types.Member
		var username string
		var createErr error
		for attempt := 0; attempt < 3; attempt++ {
			username, err = service.EnsureUniqueUsername(r.Context(), s.db, baseUsername)
			if err != nil {
				log.Printf("generate username failed: %v", err)
				render(w, r, templates.Signup(s.currentUserEmailPtr(r), form, "", "We could not process your request right now. Please try again.", next))
				return
			}

			member := types.Member{
				FirstName:        firstName,
				LastName:         lastName,
				Username:         username,
				Email:            email,
				PasswordHash:     passwordHash,
				PreferredContact: email,
				City:             strPtr(city),
				State:            strPtr(state),
				Enabled:          true,
				Verified:         !s.featureEmail,
			}

			created, createErr = service.CreateMember(r.Context(), s.db, member)
			if createErr == nil {
				break
			}
			if !isUniqueViolation(createErr, "members_username_key") {
				break
			}
		}
		if createErr != nil {
			if isUniqueViolation(createErr, "members_email_key") {
				render(w, r, templates.Signup(s.currentUserEmailPtr(r), form, "", "That email address is already taken, please try another one.", next))
				return
			}
			log.Printf("create member failed: %v", createErr)
			render(w, r, templates.Signup(s.currentUserEmailPtr(r), form, "", "We could not process your request right now. Please try again.", next))
			return
		}

		log.Printf("signup request: first_name=%q last_name=%q username=%q email=%q city=%q state=%q member_id=%d", firstName, lastName, username, email, city, state, created.ID)
		if s.featureEmail {
			token, tokenErr := service.IssueMemberToken(r.Context(), s.db, service.IssueMemberTokenParams{
				MemberID:    created.ID,
				Purpose:     service.MemberTokenPurposeEmailVerification,
				TTL:         service.DefaultMemberTokenTTL,
				RequestedIP: requestIPFromRequest(r),
			})
			if tokenErr != nil {
				log.Printf("email verification token issue failed: member_id=%d: %v", created.ID, tokenErr)
			} else {
				verifyURL := s.verifyEmailURL(token, created.Username)
				if sendErr := s.passwordResetEmailSender.SendEmailVerification(r.Context(), created.Email, created.Username, verifyURL); sendErr != nil {
					log.Printf("email verification send failed: member_id=%d: %v", created.ID, sendErr)
				}
			}
		}

		redirectURL := "/signup-success"
		if next != "" {
			redirectURL += "?next=" + url.QueryEscape(next)
		}
		http.Redirect(w, r, redirectURL, http.StatusSeeOther)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSignupSuccess(w http.ResponseWriter, r *http.Request) {
	next := sanitizePostAuthRedirect(r.URL.Query().Get("next"))
	if next == "" {
		next = s.postAuthRedirectFromCookie(r)
	}
	if next != "" {
		s.setPostAuthRedirectCookie(w, r, next)
	}
	loginHref := "/login"
	if next != "" {
		loginHref += "?next=" + url.QueryEscape(next)
	}
	render(w, r, templates.SignupSuccess(s.currentUserEmailPtr(r), loginHref, s.featureEmail))
}

func (s *Server) handleVerifyEmail(w http.ResponseWriter, r *http.Request) {
	username := sanitizeLoginUsername(r.URL.Query().Get("username"))
	redirectToLogin := func(kind, message string) {
		query := url.Values{}
		query.Set(kind, message)
		if username != "" {
			query.Set("username", username)
		}
		http.Redirect(w, r, "/login?"+query.Encode(), http.StatusSeeOther)
	}

	if !s.featureEmail {
		redirectToLogin("success", "Email verification is disabled. You can log in.")
		return
	}

	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		redirectToLogin("error", "Invalid or expired verification link.")
		return
	}

	memberID, err := service.ConsumeMemberToken(r.Context(), s.db, service.MemberTokenPurposeEmailVerification, token)
	if err != nil {
		redirectToLogin("error", "Invalid or expired verification link.")
		return
	}
	if err := service.UpdateMemberVerified(r.Context(), s.db, memberID, true); err != nil {
		log.Printf("verify email update failed: member_id=%d: %v", memberID, err)
		redirectToLogin("error", "Could not verify your email right now.")
		return
	}

	redirectToLogin("success", "Email verified. You can now log in.")
}

func (s *Server) handleVerifyEmailResend(w http.ResponseWriter, r *http.Request) {
	if !s.featureEmail {
		render(w, r, templates.Login(nil, "", "Email verification is currently disabled.", "", ""))
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))

	member, err := service.GetMemberByEmail(r.Context(), s.db, email)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		log.Printf("verify email resend lookup failed: %v", err)
		http.Error(w, "could not process request", http.StatusInternalServerError)
		return
	}

	if err == nil && !member.Verified {
		token, tokenErr := service.IssueMemberToken(r.Context(), s.db, service.IssueMemberTokenParams{
			MemberID:    member.ID,
			Purpose:     service.MemberTokenPurposeEmailVerification,
			TTL:         service.DefaultMemberTokenTTL,
			RequestedIP: requestIPFromRequest(r),
		})
		if tokenErr != nil {
			log.Printf("verify email resend token issue failed: member_id=%d: %v", member.ID, tokenErr)
		} else {
			verifyURL := s.verifyEmailURL(token, member.Username)
			if sendErr := s.passwordResetEmailSender.SendEmailVerification(r.Context(), member.Email, member.Username, verifyURL); sendErr != nil {
				log.Printf("verify email resend send failed: member_id=%d: %v", member.ID, sendErr)
			}
		}
	}

	render(w, r, templates.Login(nil, "", "If an account exists and still needs verification, we sent a verification email.", "", ""))
}

func (s *Server) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		render(w, r, templates.ForgotPassword(s.currentUserEmailPtr(r), false))
	case http.MethodPost:
		if !s.featureEmail {
			render(w, r, templates.ForgotPassword(s.currentUserEmailPtr(r), true))
			return
		}

		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		email := strings.TrimSpace(r.FormValue("email"))

		member, err := service.GetMemberByEmail(r.Context(), s.db, email)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			log.Printf("forgot password lookup failed: %v", err)
			http.Error(w, "could not process request", http.StatusInternalServerError)
			return
		}

		if err == nil {
			token, tokenErr := service.IssueMemberToken(r.Context(), s.db, service.IssueMemberTokenParams{
				MemberID:    member.ID,
				Purpose:     service.MemberTokenPurposePasswordReset,
				TTL:         service.DefaultMemberTokenTTL,
				RequestedIP: requestIPFromRequest(r),
			})
			if tokenErr != nil {
				log.Printf("password reset token issue failed: member_id=%d: %v", member.ID, tokenErr)
				render(w, r, templates.ForgotPassword(s.currentUserEmailPtr(r), true))
				return
			}
			resetURL := s.resetPasswordURL(token)
			if sendErr := s.passwordResetEmailSender.SendPasswordReset(r.Context(), member.Email, resetURL); sendErr != nil {
				log.Printf("password reset email send failed: member_id=%d: %v", member.ID, sendErr)
			}
		}

		render(w, r, templates.ForgotPassword(s.currentUserEmailPtr(r), true))
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		token := r.URL.Query().Get("token")
		if token == "" {
			render(w, r, templates.ResetPassword(s.currentUserEmailPtr(r), "", "Missing token.", ""))
			return
		}
		if _, err := service.ValidateMemberToken(r.Context(), s.db, service.MemberTokenPurposePasswordReset, token); err != nil {
			render(w, r, templates.ResetPassword(s.currentUserEmailPtr(r), "", "Invalid or expired token.", ""))
			return
		}
		render(w, r, templates.ResetPassword(s.currentUserEmailPtr(r), token, "", ""))
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		token := strings.TrimSpace(r.FormValue("token"))
		newPassword := r.FormValue("new_password")
		confirmPassword := r.FormValue("confirm_password")

		if token == "" {
			render(w, r, templates.ResetPassword(s.currentUserEmailPtr(r), token, "Missing token.", ""))
			return
		}
		if newPassword == "" || confirmPassword == "" {
			render(w, r, templates.ResetPassword(s.currentUserEmailPtr(r), token, "Please enter and confirm your new password.", ""))
			return
		}
		if newPassword != confirmPassword {
			render(w, r, templates.ResetPassword(s.currentUserEmailPtr(r), token, "Passwords do not match.", ""))
			return
		}

		newHash, err := service.HashPassword(newPassword)
		if err != nil {
			log.Printf("hash password failed: %v", err)
			render(w, r, templates.ResetPassword(s.currentUserEmailPtr(r), token, "Could not reset password right now.", ""))
			return
		}

		memberID, err := service.ConsumeMemberToken(r.Context(), s.db, service.MemberTokenPurposePasswordReset, token)
		if err != nil {
			render(w, r, templates.ResetPassword(s.currentUserEmailPtr(r), token, "Invalid or expired token.", ""))
			return
		}

		if err := service.UpdateMemberPassword(r.Context(), s.db, memberID, newHash); err != nil {
			log.Printf("reset password update failed: %v", err)
			render(w, r, templates.ResetPassword(s.currentUserEmailPtr(r), token, "Could not reset password right now.", ""))
			return
		}
		s.sessions.RevokeAllForMember(memberID)

		http.Redirect(w, r, "/login?success=Password+reset+successful%2C+please+log+in.", http.StatusSeeOther)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleMyHopshare(w http.ResponseWriter, r *http.Request) {
	successMsg := r.URL.Query().Get("success")
	errorMsg := r.URL.Query().Get("error")
	s.renderMyHopshare(w, r, successMsg, errorMsg)
}
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(s.sessions.CookieName())
	if err == nil {
		s.sessions.Delete(c.Value)
		s.clearSessionCookie(w, r)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) renderMyHopshare(w http.ResponseWriter, r *http.Request, successMsg, errorMsg string) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	displayName := memberDisplayName(user)
	lastLoginLabel := "First login"
	if user.LastLoginAt != nil {
		lastLoginLabel = user.LastLoginAt.Format("January 2, 2006")
	}

	primaryOrg, err := service.PrimaryOwnedOrganization(r.Context(), s.db, user.ID)
	hasPrimary := err == nil
	var primaryOrgID int64
	if err == nil {
		primaryOrgID = primaryOrg.ID
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		log.Printf("load primary organization for member %d: %v", user.ID, err)
	}

	orgs, err := service.ActiveOrganizationsForMember(r.Context(), s.db, user.ID)
	if err != nil {
		log.Printf("load organizations for member %d: %v", user.ID, err)
		http.Error(w, "could not load organizations", http.StatusInternalServerError)
		return
	}

	var currentOrgID int64
	var selectedFromQuery bool
	if orgIDStr := strings.TrimSpace(r.URL.Query().Get("org_id")); orgIDStr != "" {
		if parsed, err := strconv.ParseInt(orgIDStr, 10, 64); err == nil && parsed > 0 {
			currentOrgID = parsed
			selectedFromQuery = true
		}
	}
	if currentOrgID == 0 && user.CurrentOrganization != nil {
		currentOrgID = *user.CurrentOrganization
	}
	if len(orgs) > 0 && currentOrgID == 0 {
		currentOrgID = orgs[0].ID
	}
	// WHAT SORT OF HACK IS THIS??
	if currentOrgID != 0 && !orgIDInList(orgs, currentOrgID) && len(orgs) > 0 {
		currentOrgID = orgs[0].ID
	}

	if currentOrgID != 0 && (selectedFromQuery || user.CurrentOrganization == nil || *user.CurrentOrganization != currentOrgID) {
		if err := service.UpdateMemberCurrentOrganization(r.Context(), s.db, user.ID, currentOrgID); err != nil {
			log.Printf("update current organization member=%d org=%d: %v", user.ID, currentOrgID, err)
		}
	}

	isPrimaryOwnerCurrent := currentOrgID != 0 && primaryOrgID == currentOrgID

	var metrics types.OrgHopMetrics
	var memberStats types.MemberHopStats
	var myHops []types.Hop
	var activeAcceptedHop *types.Hop
	activeAcceptedViewKey := "requested"
	var activityCount int

	if currentOrgID != 0 {
		// TODO: We should expire old hops asynchronously through a delay-configurable goroutine, not whenever we render the myhopshare page
		if _, err := service.ExpireHops(r.Context(), s.db, currentOrgID, time.Now().UTC()); err != nil {
			log.Printf("expire hops org=%d: %v", currentOrgID, err)
		}

		metrics, err = service.OrgMetrics(r.Context(), s.db, currentOrgID)
		if err != nil {
			log.Printf("load org metrics org=%d: %v", currentOrgID, err)
			http.Error(w, "could not load metrics", http.StatusInternalServerError)
			return
		}
		memberStats, err = service.MemberStats(r.Context(), s.db, currentOrgID, user.ID)
		if err != nil {
			log.Printf("load member stats org=%d member=%d: %v", currentOrgID, user.ID, err)
			http.Error(w, "could not load stats", http.StatusInternalServerError)
			return
		}
		activityCount = memberStats.HopsMade + memberStats.HopsFulfilled
		myHops, err = service.ListMemberHops(r.Context(), s.db, currentOrgID, user.ID)
		if err != nil {
			log.Printf("load my hops org=%d member=%d: %v", currentOrgID, user.ID, err)
			http.Error(w, "could not load hops", http.StatusInternalServerError)
			return
		}
		for i := range myHops {
			hop := myHops[i]
			if hop.Status != types.HopStatusAccepted {
				continue
			}
			isRequester := hop.CreatedBy == user.ID
			isHelper := hop.AcceptedBy != nil && *hop.AcceptedBy == user.ID
			if !isRequester && !isHelper {
				continue
			}
			hopCopy := hop
			activeAcceptedHop = &hopCopy
			if isHelper && !isRequester {
				activeAcceptedViewKey = "helped"
			}
			break
		}
		pendingOffers, err := service.PendingHopOfferIDs(r.Context(), s.db, user.ID)
		if err != nil {
			log.Printf("load pending hop offers member=%d: %v", user.ID, err)
		} else if len(pendingOffers) > 0 {
			for i := range myHops {
				if _, ok := pendingOffers[myHops[i].ID]; ok {
					myHops[i].HasPendingOffer = true
				}
			}
		}
	}

	render(w, r, templates.MyhopShare(
		user.Email,
		displayName,
		lastLoginLabel,
		orgs,
		currentOrgID,
		isPrimaryOwnerCurrent,
		metrics,
		memberStats,
		myHops,
		activeAcceptedHop,
		activeAcceptedViewKey,
		user.ID,
		activityCount,
		hasPrimary,
		successMsg,
		errorMsg,
	))
}

func orgIDInList(orgs []types.Organization, orgID int64) bool {
	for _, o := range orgs {
		if o.ID == orgID {
			return true
		}
	}
	return false
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) currentUser(r *http.Request) *types.Member {
	return currentUserFromContext(r.Context())
}

func (s *Server) currentUserEmailPtr(r *http.Request) *string {
	if user := s.currentUser(r); user != nil {
		return &user.Email
	}
	return nil
}

func render(w http.ResponseWriter, r *http.Request, component templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	ctx := templates.WithCSRFToken(r.Context(), csrfTokenFromContext(r.Context()))
	ctx = templates.WithAdmin(ctx, isAdminFromContext(r.Context()))
	if err := component.Render(ctx, w); err != nil {
		log.Printf("templ render error: %v", err)
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func (s *Server) renderUnauthorized(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusForbidden)
	render(w, r, templates.Unauthorized(s.currentUserEmailPtr(r)))
}

func (s *Server) register(mux *http.ServeMux, path string, h HandlerFunc, middlewares ...Middleware) {
	// Headers+user run first. CSRF runs after route guards (e.g. method/auth).
	all := append([]Middleware{s.withSecurityHeaders(), s.withUser()}, middlewares...)
	all = append(all, s.withCSRF())
	mux.HandleFunc(path, chain(h, all...))
}

func chain(h HandlerFunc, m ...Middleware) http.HandlerFunc {
	for i := len(m) - 1; i >= 0; i-- {
		h = m[i](h)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		h(w, r)
	}
}

func (s *Server) withUser() Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if c, err := r.Cookie(s.sessions.CookieName()); err == nil {
				if memberID, ok := s.sessions.Get(c.Value); ok {
					if member, err := service.GetMemberByID(r.Context(), s.db, memberID); err == nil && member.Enabled {
						ctx := context.WithValue(r.Context(), userContextKey, &member)
						ctx = context.WithValue(ctx, adminContextKey, s.admins.IsAdmin(member.Username))
						r = r.WithContext(ctx)
					} else {
						s.clearSessionCookie(w, r)
					}
				} else {
					s.clearSessionCookie(w, r)
				}
			}
			next(w, r)
		}
	}
}

func (s *Server) withSecurityHeaders() Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Security-Policy", cspPolicy)
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
			w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			next(w, r)
		}
	}
}

func (s *Server) requireAuth() Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if currentUserFromContext(r.Context()) == nil {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			next(w, r)
		}
	}
}

func (s *Server) requireAdmin() Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !isAdminFromContext(r.Context()) {
				s.renderUnauthorized(w, r)
				return
			}
			next(w, r)
		}
	}
}

func (s *Server) requireMethod(methods ...string) Middleware {
	allowed := make(map[string]struct{}, len(methods))
	for _, m := range methods {
		allowed[m] = struct{}{}
	}
	allowHeader := strings.Join(methods, ", ")

	return func(next HandlerFunc) HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if _, ok := allowed[r.Method]; !ok {
				w.Header().Set("Allow", allowHeader)
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			next(w, r)
		}
	}
}

func (s *Server) rateLimitAuthEndpoint(endpoint string) Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				next(w, r)
				return
			}
			clientKey := endpoint + "|" + clientIPKey(r)
			if !s.authRateLimiter.Allow(clientKey) {
				w.Header().Set("Retry-After", strconv.Itoa(int(authRateLimitWindow/time.Second)))
				http.Error(w, "too many requests", http.StatusTooManyRequests)
				return
			}
			next(w, r)
		}
	}
}

func (s *Server) withCSRF() Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			token := s.ensureCSRFCookie(w, r)
			r = r.WithContext(context.WithValue(r.Context(), csrfContextKey, token))

			if r.Method == http.MethodPost {
				r.Body = http.MaxBytesReader(w, r.Body, csrfMaxRequestBodyBytes)
				if isMultipartRequest(r) {
					if err := r.ParseMultipartForm(csrfMaxRequestBodyBytes); err != nil {
						http.Error(w, "invalid form", http.StatusBadRequest)
						return
					}
				} else {
					if err := r.ParseForm(); err != nil {
						http.Error(w, "invalid form", http.StatusBadRequest)
						return
					}
				}

				submitted := strings.TrimSpace(r.FormValue(templates.CSRFFieldName))
				if submitted == "" {
					submitted = strings.TrimSpace(r.Header.Get(csrfHeaderName))
				}
				if !csrfTokensEqual(token, submitted) {
					http.Error(w, "forbidden", http.StatusForbidden)
					return
				}
			}

			next(w, r)
		}
	}
}

func isMultipartRequest(r *http.Request) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type"))), "multipart/form-data")
}

func clientIPKey(r *http.Request) string {
	if r == nil {
		return "unknown"
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	remote := strings.TrimSpace(r.RemoteAddr)
	if remote != "" {
		return remote
	}
	return "unknown"
}

func requestIPFromRequest(r *http.Request) *string {
	if r == nil {
		return nil
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		if parsed := net.ParseIP(host); parsed != nil {
			ip := parsed.String()
			return &ip
		}
	}
	raw := strings.TrimSpace(r.RemoteAddr)
	if parsed := net.ParseIP(raw); parsed != nil {
		ip := parsed.String()
		return &ip
	}
	return nil
}

type adminSet struct {
	usernames map[string]struct{}
}

func newAdminSet(adminUsernames []string) *adminSet {
	set := &adminSet{
		usernames: make(map[string]struct{}, len(adminUsernames)),
	}
	for _, username := range adminUsernames {
		normalized := strings.ToLower(strings.TrimSpace(username))
		if normalized == "" {
			continue
		}
		set.usernames[normalized] = struct{}{}
	}
	return set
}

func (a *adminSet) IsAdmin(username string) bool {
	if a == nil {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(username))
	if normalized == "" {
		return false
	}
	_, ok := a.usernames[normalized]
	return ok
}

func (s *Server) ensureCSRFCookie(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(csrfCookieName); err == nil {
		token := strings.TrimSpace(c.Value)
		if isValidCSRFToken(token) {
			return token
		}
	}

	token := randomToken()
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cookieSecureForRequest(r),
	})
	return token
}

func csrfTokenFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	token, _ := ctx.Value(csrfContextKey).(string)
	return token
}

func isValidCSRFToken(token string) bool {
	if len(token) != 64 {
		return false
	}
	for i := 0; i < len(token); i++ {
		b := token[i]
		isDigit := b >= '0' && b <= '9'
		isLowerHex := b >= 'a' && b <= 'f'
		if !isDigit && !isLowerHex {
			return false
		}
	}
	return true
}

func csrfTokensEqual(expected, submitted string) bool {
	if !isValidCSRFToken(expected) || !isValidCSRFToken(submitted) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(submitted)) == 1
}

func isAdminFromContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	isAdmin, _ := ctx.Value(adminContextKey).(bool)
	return isAdmin
}

type fixedWindowLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	now    func() time.Time
	hits   map[string]fixedWindowCounter
}

type fixedWindowCounter struct {
	windowStart time.Time
	count       int
}

func newFixedWindowLimiter(limit int, window time.Duration) *fixedWindowLimiter {
	if limit <= 0 {
		limit = 1
	}
	if window <= 0 {
		window = time.Minute
	}
	return &fixedWindowLimiter{
		limit:  limit,
		window: window,
		now:    time.Now,
		hits:   make(map[string]fixedWindowCounter),
	}
}

func (l *fixedWindowLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now().UTC()
	entry, ok := l.hits[key]
	if !ok || now.Sub(entry.windowStart) >= l.window {
		l.hits[key] = fixedWindowCounter{
			windowStart: now,
			count:       1,
		}
		l.cleanupStale(now)
		return true
	}

	if entry.count >= l.limit {
		return false
	}

	entry.count++
	l.hits[key] = entry
	return true
}

func (l *fixedWindowLimiter) cleanupStale(now time.Time) {
	if len(l.hits) < 1024 {
		return
	}
	for key, entry := range l.hits {
		if now.Sub(entry.windowStart) >= 2*l.window {
			delete(l.hits, key)
		}
	}
}

func currentUserFromContext(ctx context.Context) *types.Member {
	if ctx == nil {
		return nil
	}
	if u, ok := ctx.Value(userContextKey).(*types.Member); ok {
		return u
	}
	return nil
}

func memberDisplayName(member *types.Member) string {
	if member == nil {
		return ""
	}
	full := strings.TrimSpace(strings.TrimSpace(member.FirstName) + " " + strings.TrimSpace(member.LastName))
	if full != "" {
		return full
	}
	if member.Username != "" {
		return member.Username
	}
	return member.Email
}

func isUniqueViolation(err error, constraint string) bool {
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		return false
	}
	if pqErr.Code != "23505" {
		return false
	}
	if constraint == "" {
		return true
	}
	return pqErr.Constraint == constraint
}

func deriveUsername(name, email string) string {
	if name != "" {
		n := strings.ToLower(strings.TrimSpace(name))
		n = strings.ReplaceAll(n, " ", "_")
		return n
	}
	parts := strings.Split(email, "@")
	if len(parts) > 0 && parts[0] != "" {
		return parts[0]
	}
	return "user"
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "token"
	}
	return hex.EncodeToString(b)
}

func normalizePublicBaseURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = defaultPublicBaseURL
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return defaultPublicBaseURL
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return strings.TrimRight(parsed.String(), "/")
}

func (s *Server) resetPasswordURL(token string) string {
	base, err := url.Parse(s.publicBaseURL)
	if err != nil {
		base, _ = url.Parse(defaultPublicBaseURL)
	}
	ref := &url.URL{Path: "/reset-password"}
	query := ref.Query()
	query.Set("token", token)
	ref.RawQuery = query.Encode()
	return base.ResolveReference(ref).String()
}

func (s *Server) verifyEmailURL(token, username string) string {
	base, err := url.Parse(s.publicBaseURL)
	if err != nil {
		base, _ = url.Parse(defaultPublicBaseURL)
	}
	ref := &url.URL{Path: "/verify-email"}
	query := ref.Query()
	query.Set("token", token)
	if username = sanitizeLoginUsername(username); username != "" {
		query.Set("username", username)
	}
	ref.RawQuery = query.Encode()
	return base.ResolveReference(ref).String()
}

type nopPasswordResetEmailSender struct{}

func (nopPasswordResetEmailSender) SendPasswordReset(_ context.Context, _ string, _ string) error {
	return nil
}

func (nopPasswordResetEmailSender) SendEmailVerification(_ context.Context, _ string, _ string, _ string) error {
	return nil
}

func sanitizePostAuthRedirect(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 256 {
		return ""
	}
	if strings.Contains(raw, "://") || strings.Contains(raw, "\\") {
		return ""
	}
	if strings.ContainsAny(raw, "\r\n") {
		return ""
	}
	if !strings.HasPrefix(raw, "/organization/") {
		return ""
	}

	urlName := strings.TrimPrefix(raw, "/organization/")
	if urlName == "" || strings.Contains(urlName, "/") || strings.Contains(urlName, "?") || strings.Contains(urlName, "#") {
		return ""
	}

	var b strings.Builder
	b.Grow(len(urlName))
	for _, r := range strings.ToLower(urlName) {
		isAlpha := r >= 'a' && r <= 'z'
		isDigit := r >= '0' && r <= '9'
		if isAlpha || isDigit || r == '-' {
			b.WriteRune(r)
			continue
		}
		return ""
	}
	clean := strings.Trim(b.String(), "-")
	if clean == "" {
		return ""
	}
	return "/organization/" + clean
}

func sanitizeLoginUsername(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 64 {
		return ""
	}
	if strings.ContainsAny(raw, "\r\n") {
		return ""
	}
	return raw
}

func (s *Server) postAuthRedirectFromCookie(r *http.Request) string {
	c, err := r.Cookie(postAuthRedirectCookieName)
	if err != nil || c.Value == "" {
		return ""
	}
	v, err := url.QueryUnescape(c.Value)
	if err != nil {
		return ""
	}
	return sanitizePostAuthRedirect(v)
}

func (s *Server) setPostAuthRedirectCookie(w http.ResponseWriter, r *http.Request, next string) {
	next = sanitizePostAuthRedirect(next)
	if next == "" {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     postAuthRedirectCookieName,
		Value:    url.QueryEscape(next),
		Path:     "/",
		MaxAge:   7 * 24 * 60 * 60,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cookieSecureForRequest(r),
	})
}

func (s *Server) clearPostAuthRedirectCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     postAuthRedirectCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cookieSecureForRequest(r),
	})
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.sessions.CookieName(),
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cookieSecureForRequest(r),
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.sessions.CookieName(),
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cookieSecureForRequest(r),
	})
}

func (s *Server) cookieSecureForRequest(r *http.Request) bool {
	if !s.cookieSecure {
		return false
	}
	if r == nil {
		return true
	}
	if r.TLS != nil {
		return true
	}
	forwardedProto := strings.TrimSpace(strings.ToLower(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]))
	return forwardedProto == "https"
}
