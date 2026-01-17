package http

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/a-h/templ"

	"hopshare/internal/auth"
	"hopshare/internal/service"
	"hopshare/internal/types"
	"hopshare/web/templates"
)

// Server bundles dependencies for HTTP handlers.
type Server struct {
	db          *sql.DB
	sessions    *auth.SessionManager
	resetTokens map[string]int64
	resetMu     sync.RWMutex
}

type HandlerFunc func(http.ResponseWriter, *http.Request)
type Middleware func(HandlerFunc) HandlerFunc

type contextKey string

const userContextKey contextKey = "currentUser"

// NewRouter wires the base HTTP routes.
func NewRouter(db *sql.DB) http.Handler {
	srv := &Server{
		db:          db,
		sessions:    auth.NewSessionManager(),
		resetTokens: make(map[string]int64),
	}

	mux := http.NewServeMux()
	staticFS := http.FileServer(http.Dir("web/static"))
	mux.Handle("/static/", http.StripPrefix("/static/", staticFS))
	srv.register(mux, "/", srv.handleLanding, srv.requireMethod(http.MethodGet))
	srv.register(mux, "/login", srv.handleLogin, srv.requireMethod(http.MethodGet, http.MethodPost))
	srv.register(mux, "/signup", srv.handleSignup, srv.requireMethod(http.MethodGet, http.MethodPost))
	srv.register(mux, "/signup-success", srv.handleSignupSuccess, srv.requireMethod(http.MethodGet))
	srv.register(mux, "/forgot-password", srv.handleForgotPassword, srv.requireMethod(http.MethodGet, http.MethodPost))
	srv.register(mux, "/reset-password", srv.handleResetPassword, srv.requireMethod(http.MethodGet, http.MethodPost))
	srv.register(mux, "/my-hopshare", srv.handleMyHopshare, srv.requireAuth(), srv.requireMethod(http.MethodGet))
	srv.register(mux, "/my-hops", srv.handleMyHops, srv.requireAuth(), srv.requireMethod(http.MethodGet))
	srv.register(mux, "/messages", srv.handleMessages, srv.requireAuth(), srv.requireMethod(http.MethodGet))
	srv.register(mux, "/messages/unread-count", srv.handleUnreadMessageCount, srv.requireAuth(), srv.requireMethod(http.MethodGet))
	srv.register(mux, "/messages/delete", srv.handleDeleteMessage, srv.requireAuth(), srv.requireMethod(http.MethodPost))
	srv.register(mux, "/messages/reply", srv.handleReplyMessage, srv.requireAuth(), srv.requireMethod(http.MethodPost))
	srv.register(mux, "/messages/action", srv.handleMessageAction, srv.requireAuth(), srv.requireMethod(http.MethodPost))
	srv.register(mux, "/hops/create", srv.handleCreateHop, srv.requireAuth(), srv.requireMethod(http.MethodPost))
	srv.register(mux, "/hops/offer", srv.handleOfferHop, srv.requireAuth(), srv.requireMethod(http.MethodPost))
	srv.register(mux, "/hops/cancel", srv.handleCancelHop, srv.requireAuth(), srv.requireMethod(http.MethodPost))
	srv.register(mux, "/hops/complete", srv.handleCompleteHop, srv.requireAuth(), srv.requireMethod(http.MethodPost))
	srv.register(mux, "/organizations", srv.handleOrganizations, srv.requireAuth(), srv.requireMethod(http.MethodGet))
	srv.register(mux, "/organizations/logo", srv.handleOrganizationLogo, srv.requireAuth(), srv.requireMethod(http.MethodGet))
	srv.register(mux, "/organizations/create", srv.handleCreateOrganization, srv.requireAuth(), srv.requireMethod(http.MethodGet, http.MethodPost))
	srv.register(mux, "/organizations/manage", srv.handleManageOrganization, srv.requireAuth(), srv.requireMethod(http.MethodGet, http.MethodPost))
	srv.register(mux, "/organizations/manage/request", srv.handleManageMembershipRequest, srv.requireAuth(), srv.requireMethod(http.MethodPost))
	srv.register(mux, "/organizations/manage/member/remove", srv.handleRemoveMember, srv.requireAuth(), srv.requireMethod(http.MethodPost))
	srv.register(mux, "/organizations/manage/member/role", srv.handleChangeMemberRole, srv.requireAuth(), srv.requireMethod(http.MethodPost))
	srv.register(mux, "/organizations/request", srv.handleRequestMembership, srv.requireAuth(), srv.requireMethod(http.MethodPost))
	srv.register(mux, "/logout", srv.handleLogout, srv.requireMethod(http.MethodPost, http.MethodGet))
	mux.HandleFunc("/healthz", handleHealthz)

	return mux
}

func (s *Server) handleLanding(w http.ResponseWriter, r *http.Request) {
	render(w, r, templates.Landing(s.currentUserEmailPtr(r)))
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if s.currentUser(r) != nil {
			http.Redirect(w, r, "/my-hopshare", http.StatusSeeOther)
			return
		}
		successMsg := r.URL.Query().Get("success")
		render(w, r, templates.Login(nil, "", successMsg))
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		email := strings.TrimSpace(r.FormValue("email"))
		password := r.FormValue("password")

		member, err := service.AuthenticateMember(r.Context(), s.db, email, password)
		if err != nil {
			if errors.Is(err, service.ErrInvalidCredentials) {
				render(w, r, templates.Login(nil, "Invalid email or password.", ""))
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

		http.Redirect(w, r, "/my-hopshare", http.StatusSeeOther)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		render(w, r, templates.Signup(s.currentUserEmailPtr(r), "", ""))
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}

		name := strings.TrimSpace(r.FormValue("name"))
		email := strings.TrimSpace(r.FormValue("email"))
		password := r.FormValue("password")
		preferredContactMethod := strings.TrimSpace(r.FormValue("preferred_contact_method"))
		city := strings.TrimSpace(r.FormValue("city"))
		state := strings.TrimSpace(r.FormValue("state"))
		interests := strings.TrimSpace(r.FormValue("interests"))

		if preferredContactMethod != types.ContactMethodEmail && preferredContactMethod != types.ContactMethodPhone && preferredContactMethod != types.ContactMethodOther {
			render(w, r, templates.Signup(s.currentUserEmailPtr(r), "", "Please choose a preferred contact method."))
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
			render(w, r, templates.Signup(s.currentUserEmailPtr(r), "", "We could not process your request right now. Please try again."))
			return
		}

		member := types.Member{
			Username:               deriveUsername(name, email),
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

		created, err := service.CreateMember(r.Context(), s.db, member)
		if err != nil {
			log.Printf("create member failed: %v", err)
			render(w, r, templates.Signup(s.currentUserEmailPtr(r), "", "We could not process your request right now. Please try again."))
			return
		}

		log.Printf("signup request: name=%q email=%q preferred_contact_method=%q city=%q state=%q interests=%q member_id=%d", name, email, preferredContactMethod, city, state, interests, created.ID)
		http.Redirect(w, r, "/signup-success", http.StatusSeeOther)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSignupSuccess(w http.ResponseWriter, r *http.Request) {
	render(w, r, templates.SignupSuccess(s.currentUserEmailPtr(r)))
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

	displayName := strings.TrimSpace(user.Username)
	if displayName == "" {
		displayName = user.Email
	}
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
	var hopsToHelp []types.Hop
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
		hopsToHelp, err = service.ListHopsToHelp(r.Context(), s.db, currentOrgID, user.ID)
		if err != nil {
			log.Printf("load hops to help org=%d member=%d: %v", currentOrgID, user.ID, err)
			http.Error(w, "could not load hops", http.StatusInternalServerError)
			return
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
		hopsToHelp,
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
	if err := component.Render(r.Context(), w); err != nil {
		log.Printf("templ render error: %v", err)
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func (s *Server) register(mux *http.ServeMux, path string, h HandlerFunc, middlewares ...Middleware) {
	// withUser runs first so downstream middleware/handlers can rely on context user.
	all := append([]Middleware{s.withUser()}, middlewares...)
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
						r = r.WithContext(ctx)
					}
				}
			}
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

func currentUserFromContext(ctx context.Context) *types.Member {
	if ctx == nil {
		return nil
	}
	if u, ok := ctx.Value(userContextKey).(*types.Member); ok {
		return u
	}
	return nil
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
