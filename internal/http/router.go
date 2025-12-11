package http

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

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
	srv.register(mux, "/organizations", srv.handleOrganizations, srv.requireAuth(), srv.requireMethod(http.MethodGet))
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

func (s *Server) handleCreateOrganization(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if _, err := service.PrimaryOwnedOrganization(r.Context(), s.db, user.ID); err == nil {
		http.Redirect(w, r, "/organizations/manage?error="+url.QueryEscape("You already manage an organization."), http.StatusSeeOther)
		return
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		log.Printf("check primary organization: %v", err)
		http.Error(w, "could not load organization", http.StatusInternalServerError)
		return
	}

	successMsg := r.URL.Query().Get("success")
	errorMsg := r.URL.Query().Get("error")

	switch r.Method {
	case http.MethodGet:
		render(w, r, templates.CreateOrganization(s.currentUserEmailPtr(r), "", "", successMsg, errorMsg))
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		name := strings.TrimSpace(r.FormValue("name"))
		logo := strings.TrimSpace(r.FormValue("logo_url"))
		var logoPtr *string
		if logo != "" {
			logoPtr = &logo
		}

		org, err := service.CreateOrganization(r.Context(), s.db, name, user.ID, logoPtr)
		if err != nil {
			log.Printf("create organization failed: %v", err)
			msg := "Could not create organization right now."
			switch {
			case errors.Is(err, service.ErrMissingOrgName):
				msg = "Organization name is required."
			case errors.Is(err, service.ErrMissingMemberID):
				msg = "A member is required to create an organization."
			case errors.Is(err, service.ErrAlreadyPrimaryOwner):
				msg = "You already manage an organization."
			}
			render(w, r, templates.CreateOrganization(s.currentUserEmailPtr(r), name, logo, "", msg))
			return
		}

		success := fmt.Sprintf("Created %s and set you as primary owner.", org.Name)
		http.Redirect(w, r, "/my-hopshare?success="+url.QueryEscape(success), http.StatusSeeOther)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleOrganizations(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	orgs, err := service.ListOrganizations(r.Context(), s.db)
	if err != nil {
		log.Printf("list organizations: %v", err)
		http.Error(w, "could not load organizations", http.StatusInternalServerError)
		return
	}

	successMsg := r.URL.Query().Get("success")
	errorMsg := r.URL.Query().Get("error")

	render(w, r, templates.Organizations(&user.Email, orgs, successMsg, errorMsg))
}

func (s *Server) handleRequestMembership(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	orgIDStr := r.FormValue("org_id")
	orgID, err := strconv.ParseInt(orgIDStr, 10, 64)
	if err != nil || orgID <= 0 {
		http.Redirect(w, r, "/organizations?error="+url.QueryEscape("Invalid organization."), http.StatusSeeOther)
		return
	}

	var orgName string
	if org, err := service.GetOrganizationByID(r.Context(), s.db, orgID); err == nil {
		orgName = org.Name
	}

	if err := service.RequestMembership(r.Context(), s.db, user.ID, orgID, nil); err != nil {
		log.Printf("request membership member=%d org=%d: %v", user.ID, orgID, err)
		msg := "Could not submit request."
		switch {
		case errors.Is(err, service.ErrMissingMemberID):
			msg = "You need a member record to request membership."
		case errors.Is(err, service.ErrMissingOrgID):
			msg = "Invalid organization."
		case errors.Is(err, service.ErrRequestAlreadyExists):
			msg = "You already have a pending request for this organization."
		}
		http.Redirect(w, r, "/organizations?error="+url.QueryEscape(msg), http.StatusSeeOther)
		return
	}

	success := "Membership request submitted."
	if orgName != "" {
		success = fmt.Sprintf("Requested membership in %s.", orgName)
	}
	http.Redirect(w, r, "/organizations?success="+url.QueryEscape(success), http.StatusSeeOther)
}

func (s *Server) handleManageOrganization(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	primaryOrg, err := service.PrimaryOwnedOrganization(r.Context(), s.db, user.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Redirect(w, r, "/organizations/create?error="+url.QueryEscape("You need to create an organization first."), http.StatusSeeOther)
			return
		}
		log.Printf("load primary organization: %v", err)
		http.Error(w, "could not load organization", http.StatusInternalServerError)
		return
	}

	requests, err := service.PendingMembershipRequests(r.Context(), s.db, primaryOrg.ID)
	if err != nil {
		log.Printf("load pending requests: %v", err)
		http.Error(w, "could not load requests", http.StatusInternalServerError)
		return
	}

	members, err := service.OrganizationMembers(r.Context(), s.db, primaryOrg.ID)
	if err != nil {
		log.Printf("load organization members: %v", err)
		http.Error(w, "could not load members", http.StatusInternalServerError)
		return
	}

	logoVal := ""
	if primaryOrg.LogoURL != nil {
		logoVal = *primaryOrg.LogoURL
	}

	successMsg := r.URL.Query().Get("success")
	errorMsg := r.URL.Query().Get("error")

	switch r.Method {
	case http.MethodGet:
		render(w, r, templates.ManageOrganizationWithMembers(s.currentUserEmailPtr(r), primaryOrg, logoVal, requests, members, successMsg, errorMsg))
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}

		name := strings.TrimSpace(r.FormValue("name"))
		logo := strings.TrimSpace(r.FormValue("logo_url"))
		var logoPtr *string
		if logo != "" {
			logoPtr = &logo
		}
		orgIDStr := strings.TrimSpace(r.FormValue("org_id"))

		orgID, err := strconv.ParseInt(orgIDStr, 10, 64)
		if err != nil || orgID <= 0 || orgID != primaryOrg.ID {
			render(w, r, templates.ManageOrganizationWithMembers(s.currentUserEmailPtr(r), primaryOrg, logoVal, requests, members, "", "You can only manage your organization."))
			return
		}

		updateOrg := types.Organization{
			ID:      primaryOrg.ID,
			Name:    name,
			LogoURL: logoPtr,
			Enabled: primaryOrg.Enabled,
		}
		if err := service.UpdateOrganization(r.Context(), s.db, updateOrg); err != nil {
			log.Printf("update organization %d: %v", primaryOrg.ID, err)
			render(w, r, templates.ManageOrganizationWithMembers(s.currentUserEmailPtr(r), primaryOrg, logoVal, requests, members, "", "Could not update organization."))
			return
		}
		http.Redirect(w, r, "/organizations/manage?success="+url.QueryEscape("Organization updated."), http.StatusSeeOther)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleManageMembershipRequest(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	org, err := service.PrimaryOwnedOrganization(r.Context(), s.db, user.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Redirect(w, r, "/organizations/create?error="+url.QueryEscape("You need to create an organization first."), http.StatusSeeOther)
			return
		}
		log.Printf("load primary organization: %v", err)
		http.Error(w, "could not load organization", http.StatusInternalServerError)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	reqID, err := strconv.ParseInt(r.FormValue("request_id"), 10, 64)
	if err != nil || reqID <= 0 {
		http.Redirect(w, r, "/organizations/manage?error="+url.QueryEscape("Invalid request."), http.StatusSeeOther)
		return
	}
	action := strings.ToLower(strings.TrimSpace(r.FormValue("action")))

	var reqOrgID int64
	if err := s.db.QueryRowContext(r.Context(), `
		SELECT organization_id FROM membership_requests WHERE id = $1
	`, reqID).Scan(&reqOrgID); err != nil {
		msg := "Request not found."
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("lookup membership request %d: %v", reqID, err)
			msg = "Could not load request."
		}
		http.Redirect(w, r, "/organizations/manage?error="+url.QueryEscape(msg), http.StatusSeeOther)
		return
	}
	if reqOrgID != org.ID {
		http.Redirect(w, r, "/organizations/manage?error="+url.QueryEscape("You can only manage your organization's requests."), http.StatusSeeOther)
		return
	}

	switch action {
	case "accept":
		if err := service.ApproveMembershipRequest(r.Context(), s.db, reqID, user.ID); err != nil {
			log.Printf("approve request %d: %v", reqID, err)
			http.Redirect(w, r, "/organizations/manage?error="+url.QueryEscape("Could not approve request."), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/organizations/manage?success="+url.QueryEscape("Membership approved."), http.StatusSeeOther)
	case "deny":
		if err := service.DenyMembershipRequest(r.Context(), s.db, reqID, user.ID); err != nil {
			log.Printf("deny request %d: %v", reqID, err)
			http.Redirect(w, r, "/organizations/manage?error="+url.QueryEscape("Could not deny request."), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/organizations/manage?success="+url.QueryEscape("Membership denied."), http.StatusSeeOther)
	default:
		http.Redirect(w, r, "/organizations/manage?error="+url.QueryEscape("Unknown action."), http.StatusSeeOther)
	}
}

func (s *Server) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	org, err := service.PrimaryOwnedOrganization(r.Context(), s.db, user.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Redirect(w, r, "/organizations/create?error="+url.QueryEscape("You need to create an organization first."), http.StatusSeeOther)
			return
		}
		log.Printf("load primary organization: %v", err)
		http.Error(w, "could not load organization", http.StatusInternalServerError)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	memberID, err := strconv.ParseInt(r.FormValue("member_id"), 10, 64)
	if err != nil || memberID <= 0 {
		http.Redirect(w, r, "/organizations/manage?error="+url.QueryEscape("Invalid member."), http.StatusSeeOther)
		return
	}

	if err := service.RemoveOrganizationMember(r.Context(), s.db, org.ID, memberID, user.ID); err != nil {
		log.Printf("remove member %d from org %d: %v", memberID, org.ID, err)
		msg := "Could not remove member."
		if errors.Is(err, service.ErrMembershipNotFound) {
			msg = "Membership not found."
		}
		http.Redirect(w, r, "/organizations/manage?error="+url.QueryEscape(msg), http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/organizations/manage?success="+url.QueryEscape("Member removed."), http.StatusSeeOther)
}

func (s *Server) handleChangeMemberRole(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	org, err := service.PrimaryOwnedOrganization(r.Context(), s.db, user.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Redirect(w, r, "/organizations/create?error="+url.QueryEscape("You need to create an organization first."), http.StatusSeeOther)
			return
		}
		log.Printf("load primary organization: %v", err)
		http.Error(w, "could not load organization", http.StatusInternalServerError)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	memberID, err := strconv.ParseInt(r.FormValue("member_id"), 10, 64)
	if err != nil || memberID <= 0 {
		http.Redirect(w, r, "/organizations/manage?error="+url.QueryEscape("Invalid member."), http.StatusSeeOther)
		return
	}
	action := strings.ToLower(strings.TrimSpace(r.FormValue("action")))

	switch action {
	case "make_owner":
		if err := service.UpdateOrganizationMemberRole(r.Context(), s.db, org.ID, memberID, true); err != nil {
			log.Printf("make owner member=%d org=%d: %v", memberID, org.ID, err)
			http.Redirect(w, r, "/organizations/manage?error="+url.QueryEscape("Could not update member role."), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/organizations/manage?success="+url.QueryEscape("Member promoted to owner."), http.StatusSeeOther)
	case "revoke_owner":
		if err := service.UpdateOrganizationMemberRole(r.Context(), s.db, org.ID, memberID, false); err != nil {
			log.Printf("revoke owner member=%d org=%d: %v", memberID, org.ID, err)
			http.Redirect(w, r, "/organizations/manage?error="+url.QueryEscape("Could not update member role."), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/organizations/manage?success="+url.QueryEscape("Owner role revoked."), http.StatusSeeOther)
	default:
		http.Redirect(w, r, "/organizations/manage?error="+url.QueryEscape("Unknown action."), http.StatusSeeOther)
	}
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

	_, err := service.PrimaryOwnedOrganization(r.Context(), s.db, user.ID)
	hasPrimary := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		log.Printf("load primary organization for member %d: %v", user.ID, err)
	}

	orgs, err := service.ActiveOrganizationsForMember(r.Context(), s.db, user.ID)
	if err != nil {
		log.Printf("load organizations for member %d: %v", user.ID, err)
		http.Error(w, "could not load organizations", http.StatusInternalServerError)
		return
	}

	var orgNames []string
	for _, o := range orgs {
		orgNames = append(orgNames, o.Name)
	}

	render(w, r, templates.MyHopshare(user.Email, orgNames, hasPrimary, successMsg, errorMsg))
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
