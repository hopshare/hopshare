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
	db              *sql.DB
	sessions        *auth.SessionManager
	admins          *adminSet
	resetTokens     map[string]int64
	resetMu         sync.RWMutex
	authRateLimiter *fixedWindowLimiter
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

const cspPolicy = "default-src 'self'; script-src 'self' 'unsafe-eval' https://cdn.tailwindcss.com; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"

// NewRouter wires the base HTTP routes.
func NewRouter(db *sql.DB) http.Handler {
	return NewRouterWithSessionsAndAdmins(db, nil, nil)
}

// NewRouterWithSessions wires routes with an optional injected session manager.
// Passing nil uses the default in-memory session manager.
func NewRouterWithSessions(db *sql.DB, sessions *auth.SessionManager) http.Handler {
	return NewRouterWithSessionsAndAdmins(db, sessions, nil)
}

// NewRouterWithSessionsAndAdmins wires routes with optional injected session manager
// and admin usernames. Admin usernames are normalized to lowercase and deduplicated.
func NewRouterWithSessionsAndAdmins(db *sql.DB, sessions *auth.SessionManager, adminUsernames []string) http.Handler {
	if sessions == nil {
		sessions = auth.NewSessionManager()
	}
	srv := &Server{
		db:              db,
		sessions:        sessions,
		admins:          newAdminSet(adminUsernames),
		resetTokens:     make(map[string]int64),
		authRateLimiter: newFixedWindowLimiter(authRateLimitMaxRequests, authRateLimitWindow),
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
	srv.register(mux, "/admin", srv.handleAdmin, srv.requireAuth(), srv.requireMethod(http.MethodGet), srv.requireAdmin())
	srv.register(mux, "/admin/organizations/action", srv.handleAdminOrganizationAction, srv.requireAuth(), srv.requireMethod(http.MethodPost), srv.requireAdmin())
	srv.register(mux, "/admin/moderation/action", srv.handleAdminModerationAction, srv.requireAuth(), srv.requireMethod(http.MethodPost), srv.requireAdmin())
	srv.register(mux, "/forgot-password", srv.handleForgotPassword, srv.requireMethod(http.MethodGet, http.MethodPost), srv.rateLimitAuthEndpoint("forgot-password"))
	srv.register(mux, "/reset-password", srv.handleResetPassword, srv.requireMethod(http.MethodGet, http.MethodPost))
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

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	next := sanitizePostAuthRedirect(r.URL.Query().Get("next"))
	if next == "" {
		next = s.postAuthRedirectFromCookie(r)
	}
	if next != "" {
		s.setPostAuthRedirectCookie(w, next)
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
		render(w, r, templates.Login(nil, "", successMsg, next))
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
			s.setPostAuthRedirectCookie(w, next)
		}
		username := strings.TrimSpace(r.FormValue("username"))
		password := r.FormValue("password")

		member, err := service.AuthenticateMemberByUsername(r.Context(), s.db, username, password)
		if err != nil {
			if errors.Is(err, service.ErrInvalidCredentials) {
				render(w, r, templates.Login(nil, "Invalid username or password.", "", next))
				return
			}
			log.Printf("authenticate member: %v", err)
			http.Error(w, "could not log in", http.StatusInternalServerError)
			return
		}

		token, err := s.sessions.Create(member.ID)
		if err != nil {
			http.Error(w, "could not create session", http.StatusInternalServerError)
			return
		}

		http.SetCookie(w, &http.Cookie{
			Name:     s.sessions.CookieName(),
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})

		if err := service.UpdateMemberLastLogin(r.Context(), s.db, member.ID, time.Now().UTC()); err != nil {
			log.Printf("update last login member=%d: %v", member.ID, err)
		}

		s.clearPostAuthRedirectCookie(w)
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
		s.setPostAuthRedirectCookie(w, next)
	}

	switch r.Method {
	case http.MethodGet:
		render(w, r, templates.Signup(s.currentUserEmailPtr(r), "", "", next))
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
			s.setPostAuthRedirectCookie(w, next)
		}

		firstName := strings.TrimSpace(r.FormValue("first_name"))
		lastName := strings.TrimSpace(r.FormValue("last_name"))
		email := strings.TrimSpace(r.FormValue("email"))
		password := r.FormValue("password")
		preferredContactMethod := strings.TrimSpace(r.FormValue("preferred_contact_method"))
		city := strings.TrimSpace(r.FormValue("city"))
		state := strings.TrimSpace(r.FormValue("state"))
		interests := strings.TrimSpace(r.FormValue("interests"))

		if firstName == "" || lastName == "" {
			render(w, r, templates.Signup(s.currentUserEmailPtr(r), "", "Please enter your first and last name.", next))
			return
		}
		if preferredContactMethod != types.ContactMethodEmail && preferredContactMethod != types.ContactMethodPhone && preferredContactMethod != types.ContactMethodOther {
			render(w, r, templates.Signup(s.currentUserEmailPtr(r), "", "Please choose a preferred contact method.", next))
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
			render(w, r, templates.Signup(s.currentUserEmailPtr(r), "", "We could not process your request right now. Please try again.", next))
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
				render(w, r, templates.Signup(s.currentUserEmailPtr(r), "", "We could not process your request right now. Please try again.", next))
				return
			}

			member := types.Member{
				FirstName:              firstName,
				LastName:               lastName,
				Username:               username,
				Email:                  email,
				PasswordHash:           passwordHash,
				PreferredContactMethod: preferredContactMethod,
				PreferredContact:       email,
				City:                   strPtr(city),
				State:                  strPtr(state),
				Interests:              strPtr(interests),
				Enabled:                true,
				Verified:               false,
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
			log.Printf("create member failed: %v", createErr)
			render(w, r, templates.Signup(s.currentUserEmailPtr(r), "", "We could not process your request right now. Please try again.", next))
			return
		}

		log.Printf("signup request: first_name=%q last_name=%q username=%q email=%q preferred_contact_method=%q city=%q state=%q interests=%q member_id=%d", firstName, lastName, username, email, preferredContactMethod, city, state, interests, created.ID)
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
		s.setPostAuthRedirectCookie(w, next)
	}
	loginHref := "/login"
	if next != "" {
		loginHref += "?next=" + url.QueryEscape(next)
	}
	render(w, r, templates.SignupSuccess(s.currentUserEmailPtr(r), loginHref))
}

func (s *Server) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		render(w, r, templates.ForgotPassword(s.currentUserEmailPtr(r), false, ""))
	case http.MethodPost:
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
			token := s.issueResetToken(member.ID)
			link := "/reset-password?token=" + token
			log.Printf("password reset link for %s: %s", email, link)
			render(w, r, templates.ForgotPassword(s.currentUserEmailPtr(r), true, link))
			return
		}

		render(w, r, templates.ForgotPassword(s.currentUserEmailPtr(r), true, ""))
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
		if _, ok := s.peekResetToken(token); !ok {
			render(w, r, templates.ResetPassword(s.currentUserEmailPtr(r), "", "Invalid or expired token.", ""))
			return
		}
		render(w, r, templates.ResetPassword(s.currentUserEmailPtr(r), token, "", ""))
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		token := r.FormValue("token")
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

		memberID, ok := s.consumeResetToken(token)
		if !ok {
			render(w, r, templates.ResetPassword(s.currentUserEmailPtr(r), token, "Invalid or expired token.", ""))
			return
		}

		if err := service.UpdateMemberPassword(r.Context(), s.db, memberID, newHash); err != nil {
			log.Printf("reset password update failed: %v", err)
			render(w, r, templates.ResetPassword(s.currentUserEmailPtr(r), token, "Could not reset password right now.", ""))
			return
		}

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
		http.SetCookie(w, &http.Cookie{
			Name:     s.sessions.CookieName(),
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) issueResetToken(memberID int64) string {
	token := randomToken()
	s.resetMu.Lock()
	s.resetTokens[token] = memberID
	s.resetMu.Unlock()
	return token
}

func (s *Server) peekResetToken(token string) (int64, bool) {
	s.resetMu.RLock()
	memberID, ok := s.resetTokens[token]
	s.resetMu.RUnlock()
	return memberID, ok
}

func (s *Server) consumeResetToken(token string) (int64, bool) {
	s.resetMu.Lock()
	memberID, ok := s.resetTokens[token]
	if ok {
		delete(s.resetTokens, token)
	}
	s.resetMu.Unlock()
	return memberID, ok
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
					if member, err := service.GetMemberByID(r.Context(), s.db, memberID); err == nil {
						ctx := context.WithValue(r.Context(), userContextKey, &member)
						ctx = context.WithValue(ctx, adminContextKey, s.admins.IsAdmin(member.Username))
						r = r.WithContext(ctx)
					}
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
			token := ensureCSRFCookie(w, r)
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

func ensureCSRFCookie(w http.ResponseWriter, r *http.Request) string {
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

func (s *Server) setPostAuthRedirectCookie(w http.ResponseWriter, next string) {
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
	})
}

func (s *Server) clearPostAuthRedirectCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     postAuthRedirectCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}
